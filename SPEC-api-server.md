# Spec: `api-server`

Module id: `api-server` (see [CAPABILITY-MAP.md](CAPABILITY-MAP.md))

## 1. Objective

Expose `core-model` over a clean, documented HTTP REST + Server-Sent Events (SSE) API and single-binary server (`cmd/netplane`) that runs both standalone and inside Home Assistant via Ingress (`X-Ingress-Path`):

1. **HTTP REST API (`/api/v1/...`):** Serve devices, per-device detail (links, LAGs, segments, zones, gateways, VPNs, firewall, radios, clients), rolling link telemetry samples, solved topology, redacted targets, on-demand target poll triggers, and diagnostics execution/history.
2. **Built-in cross-vendor `fdb_lookup` diagnostic:** Answer "where is MAC/IP X connected?" instantaneously across all managed switches, APs, ARP/NDP tables, and DHCP leases even when a device lacks an RPC diagnostic driver.
3. **Live event stream (`/api/v1/events/stream`):** Server-Sent Events (SSE) forwarding `events.Bus` notifications with periodic keepalive comments for both the Web UI and the thin Python HA integration.
4. **Home Assistant Ingress compatibility:** Support `X-Ingress-Path` transparently so all API calls, SSE streams, and SPA assets work behind `/api/hassio_ingress/<token>/` without hardcoded root paths.
5. **Configuration & secret loading (`internal/config`):** Parse YAML config files (`netplane.yaml`) resolving `SecretRef` values from environment variables (`${ENV_VAR}` / named env lookup) without ever exposing secret values in HTTP responses or logs.
6. **Embedded OpenAPI 3.1 & SPA hosting:** Serve `schema/openapi.json` at `/api/v1/openapi.json` and host the compiled React/TypeScript SPA from `embed.FS` with client-side router fallback.

## 2. Tech Stack

- **HTTP router:** Go stdlib `net/http` (`http.ServeMux` with method+path patterns).
- **Config parsing:** `gopkg.in/yaml.v3`.
- **Auth:** Optional static bearer token (config `server.api_token_secret`, else the `NETPLANE_API_TOKEN` environment variable; the add-on sets it from its `api_token` option). The browser UI sends it as a `SameSite=Strict` `netplane_token` cookie, because `EventSource` cannot set headers. Optional Ingress trusted-proxy bypass only for requests from Home Assistant Supervisor (`172.30.32.2`, overridable with `server.trusted_ingress_cidrs`) that carry an `X-Ingress-Path` under `/api/hassio_ingress/`; loopback is not trusted by default. Unknown configuration keys are rejected at startup, except inside a target's `settings`; a secret named `NETPLANE_API_TOKEN` is rejected in add-on options. Browser guards (except for requests the trusted ingress proxy forwards with `X-Ingress-Path`): a write (any method but GET, HEAD, OPTIONS) that Go's `http.CrossOriginProtection` rejects (`Sec-Fetch-Site` other than `same-origin`/`none`, else an `Origin` whose host[:port] is not exactly the `Host` header), and whose origin is not listed in `server.trusted_origins` (canonicalized at startup: lowercase, no default port), gets 403 `CROSS_ORIGIN`; without an API token, a request whose `Host` is not an IP address, a single-label name, a private suffix (`.local`, `.lan`, `.home.arpa`, `.internal`, `.localhost`) or listed in `server.allowed_hosts` gets 421 `HOST_NOT_ALLOWED` (DNS-rebinding defence). `allowed_hosts` entries must be bare host names; others are rejected at startup.
- **Wire contract:** `schema/openapi.json` (`Apache-2.0`).

## 3. Commands

```bash
go test -race -cover ./internal/config ./internal/api ./cmd/...
go vet ./...
CGO_ENABLED=0 go build -o /tmp/netplane ./cmd/netplane
```

## 4. Project Structure

```text
schema/
  openapi.json                 # Apache-2.0 OpenAPI 3.1 contract
internal/
  config/
    config.go                  # YAML config loader + named secret env resolver
    config_test.go
  api/
    server.go                  # HTTP mux, handlers, JSON envelopes, error helpers
    ingress.go                 # X-Ingress-Path middleware, auth middleware, SPA handler
    sse.go                     # /api/v1/events/stream Server-Sent Events handler
    server_test.go             # httptest integration tests (REST, SSE, Ingress, auth, fdb_lookup)
cmd/
  netplane/
    main.go                    # CLI entrypoint (serve, version)
```

## 5. API Contract Summary

- `GET /api/v1/health` → `{"status":"ok","version":"..."}`, plus `"targets":{...}` (per-target polling status) for authenticated callers; never requires a token
- `GET /api/v1/openapi.json` → embedded OpenAPI 3.1 JSON
- `GET /api/v1/state` → full `store.NetworkState`
- `GET /api/v1/devices` → `{"items":[Device...]}`
- `GET /api/v1/devices/{id}` → `DeviceDetail` (device + links, lag_groups, segments, zones, gateways, routes, vpns, firewall, radios, clients, recent_diagnostics)
- `GET /api/v1/devices/{id}/links/{ifName}/samples?since=<RFC3339|duration>` → `{"items":[LinkSample...]}`
- `GET /api/v1/topology` → `topology.Graph`
- `GET /api/v1/vlans/flow` → `vlanflow.Flow`: every VLAN traced across the topology's infrastructure links (each end's mode `tagged`/`untagged`/`none`/`unknown` and the hop's state `carried`/`dead_end`/`mismatch`/`leak`/`unverified`), its gateways, access ports, SSIDs and reached devices, plus `findings` (`leak` for a PVID mismatch, `dead_end`, `tag_mismatch`). Wireless uplinks report no VLAN membership, so they are `unverified`; an unrouted native VLAN (such as VLAN 1) shows its dead ends but raises no finding.
- `GET /api/v1/clients?online=true&q=<query>` → `{"items":[Client...]}`
- `GET /api/v1/targets` → `{"items":[TargetConfig...]}` (strictly redacted; zero secrets)
- `POST /api/v1/targets/{id}/poll` → triggers an immediate poll of an enabled target (409 `TARGET_DISABLED` for a disabled one); returns updated `TargetStatus`
- `POST /api/v1/diagnostics` → runs `DiagnosticRequest` (`fdb_lookup` locally, or delegates to `DiagnosticDriver`), saves to store, returns `DiagnosticResult`; if the save fails it returns 500 `STORE_ERROR` without re-running the diagnostic. An unknown `kind` returns 400 `INVALID_REQUEST`. A driver's invalid-request error (`driver.ErrDiagnosticRequest`) returns 400 `INVALID_REQUEST`, a kind the driver cannot run (`driver.ErrDiagnosticUnsupported`, or no `DiagnosticDriver`) returns 501 `UNSUPPORTED`, and any other driver error returns 502 `DIAG_FAILED`. Device-side kinds:
  - OpenWrt: `ping` and `traceroute` through rpcd `file.exec` (the account needs LuCI's `luci-mod-network-diagnostics` ACL grant). Each run logs in to its own session and destroys it afterwards when the account may call `session.destroy` (LuCI's grants do not include it, so rpcd otherwise expires the session after 300 s).
  - pfSense: `ping` through `POST /api/v2/diagnostics/ping` (REST API package v2.7.0 or later; the API key needs the `api-v2-diagnostics-ping-post` privilege).
  - OPNsense: `ping` through a ping job (`POST /api/diagnostics/ping/set`, `start/{id}`, read `search_jobs` until `count` probes went out, then `stop/{id}` and `remove/{id}`, even after a timeout). The API user needs `page-diagnostics-ping`, and `user-config-readonly` refuses the job.
  - Andaru, UniFi, SNMP and RTLPlayground run no device-side diagnostic: Andaru's API (its embedded OpenAPI document) has no diagnostic route, UniFi's controller API has none netplane uses, SNMP has none, and RTLPlayground's tools sit behind its command endpoint.
  - No driver runs `cable_test` yet. RTLPlayground's cable test is reachable only through its command endpoint, which netplane never calls, and it powers the port down.
  - `target` must be an IP address or a DNS name (never starting with `-`); `params.count` sets ping probes, 1 to 10, default 3. Traceroute probes at most 20 hops, one probe per hop, without reverse DNS.
  - `structured` holds `{transmitted, received, loss_pct, rtt_min_ms, rtt_avg_ms, rtt_max_ms}` for ping and `{destination, hops: [{ttl, address, rtt_ms}]}` for traceroute; `ok` means at least one reply, or that the last hop is the destination.
- `GET /api/v1/diagnostics?device_id=...&limit=...` → `{"items":[DiagnosticResult...]}`
- `GET /api/v1/events/stream` → `text/event-stream` (`event: <type>\ndata: <json>\n\n`)

Error envelope (matching Andaru's clean structure):
`{"error":{"code":"NOT_FOUND","message":"device not found"}}` with `Cache-Control: no-store`.

## 6. Boundaries

- **Always:** Set `Cache-Control: no-store` on API JSON responses; redact secrets everywhere; test Ingress prefix stripping and auth rejection.
- **Never:** Leak `ResolvedSecrets` in `/api/v1/targets`, error bodies, or logs; accept arbitrary shell commands in diagnostic parameters.

## 7. Success Criteria

1. All endpoints verified via `httptest` under `-race` with ≥ 80% coverage across `internal/config` and `internal/api`.
2. `X-Ingress-Path` prefix handling works for both API routes and SPA fallback.
3. Optional bearer auth rejects unauthenticated requests with `401` while allowing authorized tokens and configured Ingress proxy headers.
4. SSE stream delivers live published events and closes cleanly on client disconnect.
5. `POST /api/v1/diagnostics` with `kind: "fdb_lookup"` resolves a MAC or IP to its exact switch/port/AP/VLAN across merged multi-vendor state; `ping` and `traceroute` run on OpenWrt devices and `ping` on pfSense and OPNsense.
6. `CGO_ENABLED=0` `cmd/netplane` compiles cleanly for `linux/amd64` and `linux/arm64`.
