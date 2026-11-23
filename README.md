# Andaru Netplane

Andaru Netplane is part of the [Andaru Project](https://github.com/andaru-project). Its identifiers stay as they were: the binary, container image, add-on slug and Home Assistant domain are `netplane`, and the Python package is `netplane-client`.

An open, driver-based **network configuration, information, and diagnostics plane** designed to manage heterogeneous networks (OpenWrt/DSA switches and routers, pfSense and OPNsense gateways, UniFi gear, and SNMP v2c/v3 managed switches) from a single fast UI—standalone or directly inside Home Assistant via Ingress.

## Architecture

`netplane` follows the **Frigate / Zigbee2MQTT / Music Assistant** pattern:

1. **Standalone Core + Embedded Web UI** (Go + React/TypeScript SPA, single static binary/container, embedded SQLite in WAL mode):
   - Capability-based canonical data model (devices, ports, media/SFP DOM, EEE/Flow Control, VLANs, FDB, ARP, LLDP, wireless radios/SSIDs, clients, gateways, DHCP leases).
   - Native API/SSH drivers (**OpenWrt**, **pfSense**, **OPNsense**, **UniFi**) plus a standards-based **SNMP v2c/v3** driver.
   - Cross-vendor L2/L3 topology solver (LLDP + FDB + ARP + wireless associations) and diagnostics: a cross-vendor FDB/MAC/IP lookup (`fdb_lookup`), ping and traceroute run on OpenWrt devices (the account needs LuCI's `luci-mod-network-diagnostics` ACL grant), and ping run on pfSense (REST API v2.7.0 or later, with the `api-v2-diagnostics-ping-post` privilege) and on OPNsense (the `page-diagnostics-ping` privilege). Andaru, UniFi, SNMP and RTLPlayground run no device-side diagnostic, and cable tests are planned.
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

#### OPNsense

The `opnsense` driver reads OPNsense 25.1 or later through its core REST API. Create an API key for a dedicated user and set the `api_key` and `api_secret` secrets. The user needs these privileges. OPNsense grants whole pages, so several also allow changes on that page, as noted:

- **Required:** `Lobby: Dashboard` (identity, uptime, memory, pf states) and `Status: Interfaces` (also allows reloading an interface).
- **Optional, each adding its section:**
  - `Diagnostics: ARP Table` and `Diagnostics: NDP Table`.
  - `System: Gateways` (also allows editing gateways).
  - One of these per DHCP server in use, each of which also allows editing that server: `Services: DHCP: Kea(v4)`, `Services: Dnsmasq DNS/DHCP: Settings`, or `Status: DHCP leases` for ISC dhcpd.
  - `VPN: WireGuard: Status` (25.7 or later; on 25.1 `VPN: WireGuard` grants it, together with WireGuard configuration).
  - `Diagnostics: Ping` for the ping diagnostic.
- **Missing privileges and services:** a section whose privilege is missing, or whose service is not installed, stays empty. Any other failure keeps the section's last data.
- **Ping:** it runs as an OPNsense ping job, which the driver removes afterwards. A user with `System: Deny config write` cannot create the job, and a DNS name is pinged over IPv4.

#### RTLPlayground switches

The `rtlplayground` driver is built for the firmware's small 8051 MCU, which keeps one web connection (shared with its web UI), one exporter connection (shared with Prometheus) and four login sessions:

- Enable the exporter on the switch (`prometheus on`). Every poll reads ports, counters, SFP DOM, temperatures and identity from `http://<host>:9100/metrics`, one request, without logging in.
- Optionally set the `password` secret to add LLDP, LAG, the management IP and the forwarding table. The driver then keeps a single login session and refreshes that data every 5 to 15 minutes. The firmware has no TLS, so this also needs `allow_insecure_http: true`.
- Scheduled polls run at most every 30 seconds (a manual poll from the API is not limited), one request at a time, and the driver never calls endpoints that change the switch's state. A wrong password costs one refused login every 2 minutes.
- Limits of the firmware: `/lldp.json` lists only as many neighbours as fit its 2.5 kB reply (about 4 to 5 with long names). The driver's session lives in memory, so each netplane restart takes a new login slot; the old one ages out after the switch's `session_timeout`.

### Container / Home Assistant Add-on

```bash
docker build -t netplane:local .
docker run --rm -p 8099:8099 -v netplane-data:/data \
  -v "$PWD/netplane.local.yaml:/config/netplane.yaml:ro" \
  --env-file netplane.env \
  netplane:local
```

The container reads its configuration from `/config/netplane.yaml`. Secrets the configuration references as `${NAME}` are resolved from environment variables, so pass them with `--env-file` (or `-e NAME=value`); in the Home Assistant Add-on, set them in the add-on's `secrets` option instead.

To install the add-on, add `https://github.com/andaru-project/netplane` under Settings → Add-ons → Add-on store → Repositories, then install Andaru Netplane. Supervisor pulls the published multi-arch image `ghcr.io/andaru-project/netplane` (amd64 and arm64) at the add-on's version; the same image runs standalone with `docker run`.

Releasing: Supervisor offers whatever `version` the default branch holds, so publish the image before the version bump reaches `main`:

1. Open a pull request that sets `version` in `ha-addon/config.yaml`.
2. Push the tag `v<version>` on that pull request's head commit. The [Add-on image](.github/workflows/addon-image.yml) workflow checks the tag against the file and publishes the image.
3. Merge the pull request. The package inherits the repository's public visibility, so Supervisor can pull it anonymously.

Building the image locally needs BuildKit (the default since Docker 23).

Releasing the Python client: PyPI never accepts a version twice, so only a commit on `main` is published, and the integration asks for a version only once it is on PyPI:

1. Open a pull request that sets `version` in `clients/python/pyproject.toml`, and merge it.
2. Push the tag `client-v<version>` on the merge commit. The [PyPI](.github/workflows/pypi.yml) workflow checks that the tag matches the file and is on `main`, builds and tests the wheel, and publishes it through PyPI trusted publishing. The trusted publisher (workflow `pypi.yml`, environment `pypi`) must be registered on PyPI first.
3. Once the version is on PyPI, open a pull request that sets the `netplane-client==<version>` requirement in `ha-integration/custom_components/netplane/manifest.json`.

Home Assistant Add-on metadata lives in [ha-addon/config.yaml](ha-addon/config.yaml) (`ingress: true`, port `8099`), and the upstreamable Apache-2.0 Home Assistant integration lives in [ha-integration/custom_components/netplane](ha-integration/custom_components/netplane).

## Licensing

- **Core server, drivers, Web UI, CLI, and HA Add-on packaging:** [GNU Affero General Public License v3.0 or later](LICENSE) (`AGPL-3.0-or-later`).
- **Wire schemas (`schema/`), Python API client (`clients/python/`), and Home Assistant integration (`ha-integration/`):** [Apache License 2.0](ha-integration/LICENSE) (`Apache-2.0`), keeping the Home Assistant integration cleanly upstreamable to Home Assistant Core.
