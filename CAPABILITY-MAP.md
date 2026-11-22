# Capability Map: `netplane`

This document is the canonical module index, dependency graph, license boundary, and build order for `netplane`. Each module is specified in `SPEC-<module-id>.md` before implementation.

## Architectural Alignment

`netplane` models every managed device around three explicit planes (matching [Andaru](https://github.com/andaru-project/andaru)'s safety model and bringing it across heterogeneous network gear):

1. **Observed state:** Live kernel/hardware facts (carrier, speed, media, SFP DOM, counters, FDB, LLDP, ARP/NDP neighbors, DHCP leases, wireless associations, VPN handshakes, chassis sensors).
2. **Live configuration:** Currently active settings running on the device.
3. **Saved / desired configuration (Phase 2):** Version-checked desired changes (`expectedVersion` / `If-Match`) with authoritative preview/diff and timed rollback requiring explicit confirmation before committing risky network edits.

## Module Table

| Module id | Responsibility | License | Depends on |
|---|---|---|---|
| `core-model` | Canonical capability-based domain model (`Device`, `Link`/physical port, `LagGroup`, `Segment`/VLAN+L3 interface, `Zone`, `NeighborEntry` ARP/NDP, `FdbEntry`, `LldpNeighbor`, `DhcpLease`, `GatewayStatus`, `Route`, `VpnTunnel`, `FirewallState`, `WirelessRadio`/`Ssid`, `Client`, `SensorReading`, `Diagnostic`), SQLite WAL store, named secret refs, live event bus, cross-vendor L2/L3/Wi-Fi topology solver, driver/config/poller interfaces | `AGPL-3.0-or-later` (`schema/` `Apache-2.0`) | — |
| `api-server` | Go HTTP REST + SSE/WebSocket server, OpenAPI 3.1 contract, version-checked writes, auth + Home Assistant Ingress base-path middleware, live diagnostic RPC runner, embedded SPA host | `AGPL-3.0-or-later` | `core-model` |
| `driver-openwrt` | OpenWrt / DSA switch & router driver (API-first via `ubus`/HTTP JSON-RPC or optional lightweight agent, SSH fallback): links, SFP DOM, EEE, flow control, LAG, STP, bridge FDB/VLAN, LLDP, wireless, diagnostics; Phase 2: `uci` preview/apply/rollback | `AGPL-3.0-or-later` | `core-model` |
| `driver-rtlplayground` | Switches on the RTLPlayground firmware (RTL837x/RTL836x on an 8051 MCU): links, admin state, speed, counters, SFP DOM and temperatures from its Prometheus exporter; with a password, LLDP, LAG and FDB from its JSON API on slow schedules with a single login session; Phase 2: port/VLAN/EEE/Pause writes | `AGPL-3.0-or-later` | `core-model` |
| `driver-andaru` | Andaru firewall driver (native OpenAPI 3.1 REST + SSE): observed links/addresses, segments, zones, versioned saved config, policy/NAT/DHCP/DNS summaries, preview/apply-with-rollback-timer, backups | `AGPL-3.0-or-later` | `core-model` |
| `driver-pfsense` | pfSense gateway driver (API-first via REST API package / official API with scoped least-privilege tokens, optional SSH fallback): interfaces/segments, VLANs, gateways, DHCP/ARP/NDP, WireGuard/OpenVPN/IPsec, `pf` state & services (Unbound, Kea, pfBlockerNG), diagnostics | `AGPL-3.0-or-later` | `core-model` |
| `driver-opnsense` | OPNsense gateway driver (native core REST API with key/secret): interfaces/segments, VLANs, gateways, DHCP/ARP/NDP, WireGuard/VPN, `pf` state & services, diagnostics | `AGPL-3.0-or-later` | `core-model` |
| `driver-unifi` | UniFi Network controller driver (REST + WebSocket API; supports both `uosserver` and standalone paths): APs, switches, gateways, links, PoE, LAG, STP, WLANs, mesh uplinks, clients, LLDP; Phase 2: port/PoE/EEE/WLAN writes (optional native `TNBU` driver later) | `AGPL-3.0-or-later` | `core-model` |
| `driver-snmp` | Standards-based SNMP v2c & SNMP v3 driver (`IF-MIB`, `BRIDGE-MIB`, `Q-BRIDGE-MIB`, `LLDP-MIB`, `IEEE8023-LAG-MIB`, `POWER-ETHERNET-MIB`, `ENTITY-SENSOR-MIB`/DOM): universal managed switch/router coverage | `AGPL-3.0-or-later` | `core-model` |
| `web-ui` | Ingress-ready React/TypeScript/Vite/Mantine SPA (embedded into Go binary, zero runtime CDN deps): unified inventory, live port faceplate + SFP DOM/EEE/Pause/STP inspector, cross-vendor LLDP/FDB/Wi-Fi topology map, client lookup, diagnostics console, Phase 2 diff/apply/confirm UI | `AGPL-3.0-or-later` | `api-server` |
| `ha-addon` | Multi-arch (`amd64`/`arm64`) container image + Home Assistant Add-on manifest (`ingress: true`) | `AGPL-3.0-or-later` | `api-server`, `web-ui` |
| `ha-integration` | Upstreamable async Python API client (`clients/python/`) + Home Assistant integration (`ha-integration/`): device/WAN health, PoE switches, SSID toggles, SFP optical warning sensors, VPN status, `device_tracker` | `Apache-2.0` | `api-server` (HTTP/WS contract only) |

## Dependency Direction

```text
core-model
 ├── api-server
 │    ├── web-ui
 │    │    └── ha-addon (also depends on api-server)
 │    └── ha-integration (over HTTP/WS wire contract only)
 ├── driver-openwrt
 ├── driver-rtlplayground
 ├── driver-andaru
 ├── driver-pfsense
 ├── driver-opnsense
 ├── driver-unifi
 └── driver-snmp
```

No circular dependencies: drivers never import `api-server` or each other; `ha-integration` never links Go/AGPL code.

## Phased Build Order

- **Phase 1 — Info, Topology, Diagnostics, HA Ingress:**
  1. `core-model` (`SPEC-core-model.md`)
  2. `api-server` (`SPEC-api-server.md`)
  3. Initial drivers + UI in parallel: `driver-openwrt`, `driver-rtlplayground`, `driver-pfsense`, `driver-unifi`, `driver-snmp`, `web-ui` (followed directly by `driver-andaru` and `driver-opnsense` against the same contract)
  4. `ha-addon`, `ha-integration`
- **Phase 2 — Active Control Plane (Save → Preview → Apply w/ Timed Rollback & Confirm):**
  - Desired-state edits with optimistic version checks (`expectedVersion` / `If-Match`), authoritative preview/diff, and connectivity-confirmed rollback for ports (admin state, speed, VLAN PVID/tagged, copper EEE & flow control with strict SFP guard, PoE), WLANs, and native Andaru/pfSense/OPNsense config areas.
