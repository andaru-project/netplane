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
- **Auth:** Optional static bearer token (config `server.api_token_secret`, else the `NETPLANE_API_TOKEN` environment variable; the add-on sets it from its `api_token` option). The browser UI sends it as a `SameSite=Strict` `netplane_token` cookie, because `EventSource` cannot set headers. Optional Ingress trusted-proxy bypass only for requests from Home Assistant Supervisor (`172.30.32.2`, overridable with `server.trusted_ingress_cidrs`) that carry an `X-Ingress-Path` under `/api/hassio_ingress/`; loopback is not trusted by default. Unknown configuration keys are rejected at startup, except inside a target's `settings`; a secret named `NETPLANE_API_TOKEN` is rejected in add-on options. Browser guards (except for requests the trusted ingress proxy forwards with `X-Ingress-Path`): a write (any method but GET, HEAD, OPTIONS) gets 403 `CROSS_ORIGIN` unless it has no `Origin`, carries `Sec-Fetch-Site: same-origin`, or its `Origin` matches the `Host` (default ports filled in) or a `server.allowed_hosts` entry; without an API token, a request whose `Host` is not an IP address, a single-label name, a private suffix (`.local`, `.lan`, `.home.arpa`, `.internal`, `.localhost`) or listed in `server.allowed_hosts` gets 421 `HOST_NOT_ALLOWED` (DNS-rebinding defence). `allowed_hosts` entries must be bare host names; others are rejected at startup.
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
- `GET /api/v1/clients?online=true&q=<query>` → `{"items":[Client...]}`
- `GET /api/v1/targets` → `{"items":[TargetConfig...]}` (strictly redacted; zero secrets)
- `POST /api/v1/targets/{id}/poll` → triggers an immediate poll of an enabled target (409 `TARGET_DISABLED` for a disabled one); returns updated `TargetStatus`
- `POST /api/v1/diagnostics` → runs `DiagnosticRequest` (`fdb_lookup` locally, or delegates to `DiagnosticDriver`), saves to store, returns `DiagnosticResult`; if the save fails it returns 500 `STORE_ERROR` without re-running the diagnostic
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
5. `POST /api/v1/diagnostics` with `kind: "fdb_lookup"` resolves a MAC or IP to its exact switch/port/AP/VLAN across merged multi-vendor state.
6. `CGO_ENABLED=0` `cmd/netplane` compiles cleanly for `linux/amd64` and `linux/arm64`.
