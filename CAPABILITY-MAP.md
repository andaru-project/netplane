# Capability Map: `netplane`

This document is the canonical module index, dependency graph, license boundary, and build order for `netplane`. Each module is specified in `SPEC-<module-id>.md` before implementation.

## Module Table

| Module id | Responsibility | License | Depends on |
|---|---|---|---|
| `core-model` | Canonical capability-based domain model (devices, ports, SFP DOM, EEE/Pause, VLANs, FDB, ARP, LLDP, wireless, clients, gateways, diagnostics), SQLite WAL store, live event bus, cross-vendor L2/L3 topology solver, driver & poller interfaces | `AGPL-3.0-or-later` (`schema/` `Apache-2.0`) | — |
| `api-server` | Go HTTP REST + WebSocket server, OpenAPI contract, auth + Home Assistant Ingress base-path middleware, live diagnostic RPC runner, embedded SPA host | `AGPL-3.0-or-later` | `core-model` |
| `driver-openwrt` | OpenWrt / DSA switch & router driver (API-first via `ubus`/HTTP JSON-RPC or optional lightweight agent, with SSH fallback): ports, media, SFP DOM, EEE, flow control, bridge FDB/VLAN, LLDP, ping/cable-test; Phase 2: `uci` desired-state diff + atomic apply/rollback | `AGPL-3.0-or-later` | `core-model` |
| `driver-pfsense` | pfSense gateway driver (API-first via REST API package / official API with scoped least-privilege tokens, optional SSH fallback): interfaces, VLANs, gateways, DHCP/ARP leases, WireGuard/VPN, counters, diagnostics; Phase 2: config diff/backup | `AGPL-3.0-or-later` | `core-model` |
| `driver-unifi` | UniFi Network controller driver (REST + WebSocket API; supports both `uosserver` and standalone paths): APs, switches, ports, PoE, WLANs, clients, LLDP/uplinks; Phase 2: port/PoE/EEE/WLAN writes (optional native `TNBU` driver later) | `AGPL-3.0-or-later` | `core-model` |
| `driver-snmp` | Standards-based SNMP v2c & SNMP v3 driver (`IF-MIB`, `BRIDGE-MIB`, `Q-BRIDGE-MIB`, `LLDP-MIB`, `POWER-ETHERNET-MIB`, `ENTITY-MIB`/DOM extensions): universal switch/router ports, counters, FDB, LLDP, PoE | `AGPL-3.0-or-later` | `core-model` |
| `web-ui` | Ingress-ready React/TypeScript SPA (embedded into Go binary): unified inventory, live port faceplate + SFP DOM/EEE/Pause inspector, cross-vendor LLDP/FDB topology graph, client lookup, diagnostics console | `AGPL-3.0-or-later` | `api-server` |
| `ha-addon` | Multi-arch (`amd64`/`arm64`) container image + Home Assistant Add-on manifest (`ingress: true`) | `AGPL-3.0-or-later` | `api-server`, `web-ui` |
| `ha-integration` | Upstreamable async Python API client (`clients/python/`) + Home Assistant integration (`ha-integration/`): device/WAN health, PoE switches, SSID toggles, SFP optical warning sensors, `device_tracker` | `Apache-2.0` | `api-server` (HTTP/WS contract only) |

## Dependency Direction

```text
core-model
 ├── api-server
 │    ├── web-ui
 │    │    └── ha-addon (also depends on api-server)
 │    └── ha-integration (over HTTP/WS wire contract only)
 ├── driver-openwrt
 ├── driver-pfsense
 ├── driver-unifi
 └── driver-snmp
```

No circular dependencies: drivers never import `api-server` or each other; `ha-integration` never links Go/AGPL code.

## Phased Build Order

- **Phase 1 — Info, Topology, Diagnostics, HA Ingress:**
  1. `core-model` (`SPEC-core-model.md`)
  2. `api-server` (`SPEC-api-server.md`)
  3. `driver-openwrt`, `driver-pfsense`, `driver-unifi`, `driver-snmp`, `web-ui` (can proceed in parallel once `api-server` contract is frozen)
  4. `ha-addon`, `ha-integration`
- **Phase 2 — Active Control Plane:**
  - Desired-state diff, dry-run preview, atomic apply, and rollback for port admin state, speed/duplex, VLAN PVID/tagged membership, copper EEE & flow control (with SFP guard), PoE cycle/toggle, and WLAN toggles across `core-model` → `api-server` → drivers (`driver-openwrt`, `driver-unifi`, `driver-snmp`) → `web-ui`.
