# netplane

An open, driver-based **network configuration, information, and diagnostics plane** designed to manage heterogeneous networks (OpenWrt/DSA switches and routers, pfSense gateways, UniFi gear, and SNMP v2c/v3 managed switches; OPNsense support is planned) from a single fast UI—standalone or directly inside Home Assistant via Ingress.

## Architecture

`netplane` follows the **Frigate / Zigbee2MQTT / Music Assistant** pattern:

1. **Standalone Core + Embedded Web UI** (Go + React/TypeScript SPA, single static binary/container, embedded SQLite in WAL mode):
   - Capability-based canonical data model (devices, ports, media/SFP DOM, EEE/Flow Control, VLANs, FDB, ARP, LLDP, wireless radios/SSIDs, clients, gateways, DHCP leases).
   - Native API/SSH drivers (**OpenWrt**, **pfSense**, **UniFi**) plus a standards-based **SNMP v2c/v3** driver.
   - Cross-vendor L2/L3 topology solver (LLDP + FDB + ARP + wireless associations) and diagnostics: a cross-vendor FDB/MAC/IP lookup (`fdb_lookup`) today, with device-side ping, traceroute/mtr, cable tests, optical DOM and port counter diagnostics planned.
2. **Home Assistant Add-on with Ingress:**
   - Embeds the full UI directly into the Home Assistant sidebar and Android/iOS Companion Apps with SSO and zero extra exposed ports.
3. **Thin Home Assistant Integration (`ha-integration/`):**
   - Connects to `netplane` over HTTP/WebSocket and exposes only automation-worthy entities (device/WAN health, CPU and PoE draw, SFP optical alarms, `device_tracker` presence; PoE switches and SSID toggles are planned) without entity explosion or Recorder DB bloat.

See [CAPABILITY-MAP.md](CAPABILITY-MAP.md) for the module decomposition and build order.

## Quick Start

### Standalone (Go + Embedded Web UI)

```bash
# Build frontend SPA
(cd web && npm ci && npm run build)

# Build static single binary with embedded UI
CGO_ENABLED=0 go build -tags embedui -o bin/netplane ./cmd/netplane

# Copy and edit config (secrets resolved from environment variables)
cp netplane.example.yaml netplane.local.yaml
export NETPLANE_API_TOKEN="$(openssl rand -hex 32)"  # the example requires a bearer token
./bin/netplane serve --config netplane.local.yaml --db netplane.db --listen :8099
```

The example configuration requires a bearer token (`api_token_secret`). Remove it only when the server listens on a loopback address; in the Home Assistant Add-on, set the `api_token` option instead. Without a token, the server answers only requests addressed to an IP address, a single-label name (`netplane`) or a private suffix (`.local`, `.lan`, `.home.arpa`, `.internal`, `.localhost`), so a malicious web page cannot reach it by DNS rebinding; list any other names under `server.allowed_hosts` (bare host names). This trusts your local resolver and search domain. Writes from another origin are always refused (Go's cross-origin protection: `Sec-Fetch-Site`, else `Origin` against `Host`). A reverse proxy in front of netplane should pass the browser's `Host` through with its port (nginx: `proxy_set_header Host $http_host;`); if it rewrites `Host`, list the public origin under `server.trusted_origins` (for example `https://netplane.example.net`).

Drivers refuse to send credentials to a plain `http://` endpoint unless the target sets `allow_insecure_http: true` under `settings`. Use `https://` (or a `unix://` socket) where the device supports it.

### Container / Home Assistant Add-on

```bash
docker build -t netplane:local .
docker run --rm -p 8099:8099 -v netplane-data:/data \
  -v "$PWD/netplane.local.yaml:/config/netplane.yaml:ro" \
  --env-file netplane.env \
  netplane:local
```

The container reads its configuration from `/config/netplane.yaml`. Secrets the configuration references as `${NAME}` are resolved from environment variables, so pass them with `--env-file` (or `-e NAME=value`); in the Home Assistant Add-on, set them in the add-on's `secrets` option instead.

Home Assistant Add-on metadata lives in [ha-addon/config.yaml](ha-addon/config.yaml) (`ingress: true`, port `8099`), and the upstreamable Apache-2.0 Home Assistant integration lives in [ha-integration/custom_components/netplane](ha-integration/custom_components/netplane).

## Licensing

- **Core server, drivers, Web UI, CLI, and HA Add-on packaging:** [GNU Affero General Public License v3.0 or later](LICENSE) (`AGPL-3.0-or-later`).
- **Wire schemas (`schema/`), Python API client (`clients/python/`), and Home Assistant integration (`ha-integration/`):** [Apache License 2.0](ha-integration/LICENSE) (`Apache-2.0`), keeping the Home Assistant integration cleanly upstreamable to Home Assistant Core.
