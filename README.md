# netplane

An open, driver-based **network configuration, information, and diagnostics plane** designed to manage heterogeneous networks (OpenWrt/DSA switches and routers, pfSense/OPNsense gateways, UniFi gear, and SNMP v2c/v3 managed switches) from a single fast UI—standalone or directly inside Home Assistant via Ingress.

## Architecture

`netplane` follows the **Frigate / Zigbee2MQTT / Music Assistant** pattern:

1. **Standalone Core + Embedded Web UI** (Go + React/TypeScript SPA, single static binary/container, embedded SQLite in WAL mode):
   - Capability-based canonical data model (devices, ports, media/SFP DOM, EEE/Flow Control, VLANs, FDB, ARP, LLDP, wireless radios/SSIDs, clients, gateways, DHCP leases).
   - Native API/SSH drivers (**OpenWrt**, **pfSense**, **UniFi**) plus a standards-based **SNMP v2c/v3** driver.
   - Cross-vendor L2/L3 topology solver (LLDP + FDB + ARP + wireless associations) and live diagnostics (ping, traceroute/mtr, cable tests, optical DOM, port counters).
2. **Home Assistant Add-on with Ingress:**
   - Embeds the full UI directly into the Home Assistant sidebar and Android/iOS Companion Apps with SSO and zero extra exposed ports.
3. **Thin Home Assistant Integration (`ha-integration/`):**
   - Connects to `netplane` over HTTP/WebSocket and exposes only automation-worthy entities (device/WAN health, PoE switches, SSID toggles, SFP optical alarms, `device_tracker` presence) without entity explosion or Recorder DB bloat.

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
./bin/netplane serve --config netplane.local.yaml --db netplane.db --listen :8099
```

### Container / Home Assistant Add-on

```bash
docker build -t netplane:local .
docker run --rm -p 8099:8099 -v netplane-data:/data netplane:local
```

Home Assistant Add-on metadata lives in [ha-addon/config.yaml](ha-addon/config.yaml) (`ingress: true`, port `8099`), and the upstreamable Apache-2.0 Home Assistant integration lives in [ha-integration/custom_components/netplane](ha-integration/custom_components/netplane).

## Licensing

- **Core server, drivers, Web UI, CLI, and HA Add-on packaging:** [GNU Affero General Public License v3.0 or later](LICENSE) (`AGPL-3.0-or-later`).
- **Wire schemas (`schema/`), Python API client (`clients/python/`), and Home Assistant integration (`ha-integration/`):** [Apache License 2.0](ha-integration/LICENSE) (`Apache-2.0`), keeping the Home Assistant integration cleanly upstreamable to Home Assistant Core.
