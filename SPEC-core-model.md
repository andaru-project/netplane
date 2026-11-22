# Spec: `core-model`

Module id: `core-model` (see [CAPABILITY-MAP.md](CAPABILITY-MAP.md))

## 1. Objective

Provide the vendor-agnostic foundation of `netplane`:

1. **Canonical capability-based network model:** Represent heterogeneous switches, routers, gateways, access points, and clients accurately without faking vendor hardware or forcing unsupported attributes (for example, non-PoE switches never report fake PoE budgets; SFP ports carry real DOM/DDMI optics telemetry and never silently advertise copper-only features).
2. **Driver & polling contract:** Define the Go interface every platform driver (`driver-openwrt`, `driver-pfsense`, `driver-unifi`, `driver-snmp`) implements to report state snapshots, emit real-time updates, and execute on-demand diagnostics (with clean extension points for Phase 2 desired-state diff/apply/rollback).
3. **Embedded persistence (`SQLite` WAL):** Persist configured driver targets, latest normalized device snapshots, port/counter history rollups, topology snapshots, and diagnostic run logs in a single zero-dependency database file.
4. **Cross-vendor L2/L3 topology solver:** Correlate `LLDP` neighbors, switch `FDB` (MAC forwarding tables), gateway `ARP`/`DHCP` tables, and AP wireless client associations across mixed vendors (for example, pfSense → OpenWrt switch → UniFi AP → Wi-Fi client) into a deterministic directed graph with confidence scores.
5. **Live event bus:** Fan out state changes (device online/offline, port link transitions, SFP optical threshold alerts, topology changes, client joins/leaves) to `api-server` WebSockets and downstream HA bridges.

**Phase 1 non-goals:** Active configuration writes/rollbacks (Phase 2), multi-tenant RBAC, external time-series DB backends.

## 2. Tech Stack

- **Language:** Go `1.24+` (developed on `1.27`, cross-compiled with `CGO_ENABLED=0` for `linux/amd64`, `linux/arm64`, `darwin/arm64`).
- **Database:** Pure-Go SQLite (`modernc.org/sqlite`) with WAL mode, foreign keys enabled, and embedded versioned SQL migrations (`embed.FS`).
- **Logging:** Go stdlib `log/slog` (structured JSON/text logging).
- **Schema contract:** JSON Schema / OpenAPI definitions under `schema/` (`Apache-2.0`).
- **External dependencies policy:** Minimal stdlib-first footprint; only `modernc.org/sqlite` is required by `core-model`.

## 3. Commands

```bash
# Run all unit tests with race detector and coverage
go test -race -coverprofile=coverage.txt ./internal/...

# Inspect per-function coverage
go tool cover -func=coverage.txt

# Static analysis
go vet ./...

# Format check
gofmt -l .
```

## 4. Project Structure

```text
schema/                        # Apache-2.0 wire JSON Schema definitions (device, port, topology, events)
internal/
  model/                       # Canonical types, Capability enum, MAC/IP normalizers, validation
    device.go
    port.go
    l2l3.go                    # Vlan, FdbEntry, ArpEntry, LldpNeighbor, DhcpLease, GatewayStatus
    wireless.go                # WirelessRadio, Ssid, Client
    diagnostic.go              # DiagnosticKind, DiagnosticRequest, DiagnosticResult
    normalize.go               # Canonical MAC ("aa:bb:cc:dd:ee:ff"), media types, speed helpers
    normalize_test.go
  driver/                      # Driver interfaces, Registry, TargetConfig, Snapshot
    driver.go
    registry.go
    registry_test.go
  store/                       # SQLite WAL persistence, embedded migrations, repositories
    store.go
    migrations/
      0001_init.sql
    store_test.go
  topology/                    # Pure-function cross-vendor L2/L3/Wi-Fi topology solver
    solver.go
    solver_test.go
  events/                      # Non-blocking typed pub/sub EventBus + diff detector
    bus.go
    diff.go
    bus_test.go
  poller/                      # Concurrent driver polling scheduler with backoff & jitter
    poller.go
    poller_test.go
```

## 5. Canonical Data Contracts

### 5.1 Capabilities

Every `Device` advertises an explicit set of `Capability` flags so the UI, diagnostics runner, and Phase 2 control plane know exactly what it supports:

- `cap:ports` — Physical/logical switch or router interfaces
- `cap:sfp_dom` — Optical transceiver DDMI/DOM telemetry (Tx/Rx dBm, temperature, voltage, bias, vendor/PN)
- `cap:eee` — Energy-Efficient Ethernet status (and Phase 2 control)
- `cap:flow_control` — IEEE 802.3x Pause / Flow Control status (and Phase 2 control)
- `cap:poe` — PoE budget and per-port power draw / control
- `cap:vlans` — 802.1Q VLAN membership (PVID + tagged)
- `cap:fdb` — L2 MAC forwarding table
- `cap:lldp` — LLDP/CDP neighbor table
- `cap:arp` — L3 ARP / neighbor table
- `cap:dhcp` — DHCP lease table
- `cap:gateway` — WAN/gateway status, routes, VPN tunnels
- `cap:wireless` — Radios, SSIDs, and wireless station associations
- `cap:diag_ping`, `cap:diag_traceroute`, `cap:diag_cable_test` — On-demand device-side diagnostics

### 5.2 Core Types

- **`Device`**:
  - `ID` (stable canonical ID, e.g. `driver:normalized-primary-mac` or `driver:unique-key`), `Driver` (`openwrt`, `pfsense`, `unifi`, `snmp`), `TargetID` (configured source), `Name`, `Hostname`, `Vendor`, `Model`, `Role` (`switch`, `router`, `gateway`, `ap`, `combo`, `unknown`), `PrimaryMAC`, `MACs []string` (all owned interface MACs for topology resolution), `MgmtIP`, `Firmware`, `UptimeSec`, `Online bool`, `LastSeen time.Time`, `Capabilities []Capability`, `SystemStats` (`CPUPct`, `MemUsedBytes`, `MemTotalBytes`, `TempC *float64`), `Metadata map[string]string`.
- **`Port`**:
  - `DeviceID`, `Index int` (1-based display order), `IfName` (e.g. `lan1`, `sfp1`, `eth0`, `igc0`), `Label`, `AdminUp bool`, `LinkUp bool`, `SpeedMbps int`, `Duplex` (`full`, `half`, `unknown`), `Autoneg *bool`, `Media` (`FE`, `GE`, `2.5GE`, `5GE`, `10GE`, `SFP`, `SFP+`, `SFP28`, `QSFP28`, `other`), `IsFiber bool` (derived/explicit guard for SFP/fiber media), `EEE *bool`, `FlowControl *FlowControlState` (`Rx`, `Tx`, `Autoneg`), `PVID *int`, `TaggedVLANs []int`, `PoE *PortPoE` (`Supported`, `Enabled`, `State`, `Watts`, `Class`), `SFP *SfpDom` (`Present`, `Vendor`, `PartNumber`, `Serial`, `WavelengthNm`, `TempC`, `VoltageV`, `BiasMa`, `TxPowerDbm`, `RxPowerDbm`, `RxWarning`), `Counters *PortCounters` (`RxBytes`, `TxBytes`, `RxPackets`, `TxPackets`, `RxErrors`, `TxErrors`, `RxDropped`, `TxDropped`, `RxPause`, `TxPause`, `CrcErrors`), `Rates *PortRates` (`RxBps`, `TxBps` computed from counter deltas).
- **`L2 / L3 / Wireless / Client` tables**:
  - `Vlan`: `ID`, `Name`, `Subnet`, `GatewayIP`, `DeviceIDs`.
  - `FdbEntry`: `DeviceID`, `PortName`, `MAC`, `VLAN`, `IsLocal bool`, `AgeSec *int`.
  - `LldpNeighbor`: `DeviceID`, `LocalPort`, `ChassisID`, `RemotePort`, `RemoteSysName`, `RemoteMgmtIP`.
  - `ArpEntry`: `DeviceID`, `IP`, `MAC`, `Interface`, `State`.
  - `DhcpLease`: `DeviceID`, `IP`, `MAC`, `Hostname`, `ExpiresAt *time.Time`, `Pool`.
  - `GatewayStatus`: `DeviceID`, `Name`, `Interface`, `GatewayIP`, `MonitorIP`, `Online bool`, `RTTMs *float64`, `LossPct *float64`, `IsDefault bool`.
  - `WirelessRadio` / `Ssid`: `DeviceID`, `Band` (`2.4GHz`, `5GHz`, `6GHz`), `Channel`, `WidthMHz`, `TxPowerDbm`, `UtilizationPct`, `SSIDs`.
  - `Client`: `MAC`, `IPs []string`, `Hostname`, `VendorOUI`, `Online bool`, `ConnectionType` (`wired`, `wireless`), `AttachedDeviceID`, `AttachedPort`, `SSID`, `VLAN *int`, `SignalDbm *int`, `RxRateMbps *int`, `TxRateMbps *int`, `LastSeen`.
- **`DiagnosticRequest` / `DiagnosticResult`**:
  - `Kind` (`ping`, `traceroute`, `cable_test`, `fdb_lookup`), `DeviceID`, `Target` (host/IP/port/MAC), `Params map[string]string` → `OK bool`, `DurationMs int64`, `Summary string`, `Structured any`, `RawOutput string`, `Error string`.

### 5.3 Driver Interface

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
package driver

import (
	"context"
	"time"

	"github.com/andrebrait/netplane/internal/model"
)

// Snapshot is an atomic point-in-time read returned by a Driver.
// A single driver target (for example, a UniFi controller) may manage multiple devices.
type Snapshot struct {
	TargetID    string
	CollectedAt time.Time
	Devices     []model.Device
	Ports       []model.Port
	VLANs       []model.Vlan
	FDB         []model.FdbEntry
	LLDP        []model.LldpNeighbor
	ARP         []model.ArpEntry
	DHCP        []model.DhcpLease
	Gateways    []model.GatewayStatus
	Radios      []model.WirelessRadio
	Clients     []model.Client
}

// Driver is implemented by every platform integration (OpenWrt, pfSense, UniFi, SNMP).
type Driver interface {
	Kind() string
	ValidateConfig(cfg TargetConfig) error
	Poll(ctx context.Context, cfg TargetConfig) (*Snapshot, error)
}

// DiagnosticDriver is an optional interface for drivers supporting device-side diagnostics.
type DiagnosticDriver interface {
	RunDiagnostic(ctx context.Context, cfg TargetConfig, req model.DiagnosticRequest) (*model.DiagnosticResult, error)
}
```

### 5.4 Topology Solver Algorithm (Deterministic Pure Function)

Given the merged network state across all drivers, `topology.Solve(state)` builds nodes (`Device` + optionally `Client`) and directed edges (`Uplink` / `Peer` / `WirelessClient`) with explicit `Evidence` (`lldp`, `fdb_unique`, `fdb_inferred`, `wireless`, `arp_gateway`) and `Confidence` (`0.0–1.0`):

1. **Direct LLDP edges (`confidence: 1.0`):** Match `LldpNeighbor.ChassisID` / `RemoteMgmtIP` / `RemoteSysName` against known `Device.MACs` / `MgmtIP` / `Hostname`. Deduplicate bidirectional LLDP pairs into one canonical link preserving both endpoints' local port names.
2. **FDB switch-to-switch / switch-to-gateway edges (`confidence: 0.85`):** For any known device `B` whose `MACs` appear in device `A`'s FDB on port `p` (excluding `IsLocal` entries and ports already linked by LLDP to a downstream switch that itself sees `B`), select the port with the **smallest downstream MAC cardinality** (the standard spanning-tree leaf-closest heuristic).
3. **Wireless client attachment (`confidence: 0.95`):** Attach wireless `Client` records directly to their reporting AP `DeviceID` + `SSID`.
4. **Wired client attachment (`confidence: 0.80`):** Attach wired MACs (from `ARP` / `DHCP` / `FDB` not matching any infrastructure `Device.MACs`) to the **access port** (a port with no downstream infrastructure device attached) where that MAC appears in `FDB`.
5. **Root identification:** Prefer devices with `Role == gateway` (or `cap:gateway`), breaking ties by highest LLDP/FDB reachability, to orient uplink directions cleanly.

### 5.5 SQLite Storage & Rolling Telemetry

- Tables in `0001_init.sql`:
  - `targets` (configured driver targets; secret fields stored encrypted-at-rest or referenced via env/file, never returned in API responses)
  - `devices`, `ports`, `vlans`, `fdb_entries`, `lldp_neighbors`, `arp_entries`, `dhcp_leases`, `gateways`, `radios`, `clients` (latest normalized state per `target_id` replaced transactionally on each poll)
  - `port_samples` (rolling time-series of `rx_bps`, `tx_bps`, `rx_errors`, `tx_errors`, `sfp_rx_dbm`, `sfp_temp_c`, `poe_watts` with configurable retention, default 24 hours, pruned automatically)
  - `topology_snapshots` (latest solved graph JSON + timestamp)
  - `diagnostic_runs` (audit/history of executed diagnostic commands)
- Counter delta rate calculation (`RxBps`, `TxBps`) handles 32-bit/64-bit wraparound and device reboot resets cleanly (never emits negative rates).

## 6. Code Style

- Every Go file begins with `// SPDX-License-Identifier: AGPL-3.0-or-later`.
- Canonicalize all MAC addresses on ingest via `model.NormalizeMAC` (`aa:bb:cc:dd:ee:ff` lowercase colon-separated). Invalid MACs fail validation cleanly rather than polluting FDB/topology joins.
- Optional hardware telemetry fields use pointers (`*bool`, `*float64`, `*SfpDom`, `*PortPoE`) so `null`/omitted means "not supported or unavailable" rather than misleading zero values.
- Example idiomatic helper:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
package model

import (
	"fmt"
	"net"
	"strings"
)

// NormalizeMAC parses any common MAC format (colon, hyphen, Cisco dot, or bare hex)
// and returns lowercase colon-separated "aa:bb:cc:dd:ee:ff".
func NormalizeMAC(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if len(s) == 12 && !strings.ContainsAny(s, ":-.") {
		s = fmt.Sprintf("%s:%s:%s:%s:%s:%s", s[0:2], s[2:4], s[4:6], s[6:8], s[8:10], s[10:12])
	}
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return "", fmt.Errorf("invalid MAC %q", raw)
	}
	return strings.ToLower(hw.String()), nil
}
```

## 7. Testing Strategy

- **Framework:** Go stdlib `testing` with `-race` enabled everywhere.
- **Test levels in `core-model`:**
  1. `internal/model`: table-driven unit tests for MAC normalization (colon, hyphen, dot, bare 12-hex, uppercase, invalid inputs), `IsFiber` media classification (`SFP`/`SFP+`/`SFP28`/`QSFP28` vs `FE`/`GE`/`2.5GE`/`5GE`/`10GE`), and JSON round-trip serialization matching `schema/`.
  2. `internal/store`: in-memory SQLite (`file::memory:?cache=shared` or `t.TempDir()`) tests verifying migration idempotency, transactional snapshot replacement, counter-delta rate calculation (normal increment, reboot reset, counter wrap), and retention pruning.
  3. `internal/topology`: deterministic multi-vendor fixture tests:
     - Linear chain: `pfSense (gateway)` → `OpenWrt LG510S (switch)` → `UniFi UAP (ap)` → wired + wireless clients.
     - Mixed evidence: LLDP on one hop, FDB-only on another (e.g., unmanaged or LLDP-disabled switch), verifying no duplicate edges and accurate leaf access-port client attachment.
     - Loop/redundant link handling (does not panic or infinitely recurse).
  4. `internal/events`: pub/sub delivery, slow-subscriber drop safety (non-blocking), and state diff emission (device up/down, port link up/down, SFP Rx power warning transition).
  5. `internal/poller`: mock driver tests verifying concurrent polling, per-target error isolation (one failing target never blocks others), exponential backoff, and graceful `ctx.Done()` shutdown.
- **Target coverage:** ≥ 85% statement coverage across `internal/{model,store,topology,events,poller}`.

## 8. Boundaries

- **Always:**
  - Run `go test -race ./...` and `go vet ./...` before committing.
  - Include `SPDX-License-Identifier` headers matching the directory license.
  - Keep `topology.Solve` deterministic and side-effect free (stable output sorting).
- **Ask first:**
  - Adding third-party Go dependencies beyond `modernc.org/sqlite`.
  - Breaking changes to `schema/` wire contracts or SQLite migration history once released.
- **Never:**
  - Store plaintext credentials in API-serialized structs or logs (`TargetConfig.Secrets` must carry `json:"-"` on read DTOs and redacted `slog.LogValuer`).
  - Import AGPL code into `schema/`, `clients/python/`, or `ha-integration/`.
  - Skip or weaken failing tests.

## 9. Success Criteria

`core-model` is complete when:

1. `go test -race ./...` and `go vet ./...` pass with ≥ 85% coverage and zero race warnings.
2. `schema/` contains valid Apache-2.0 JSON Schema files matching the canonical Go structs.
3. SQLite WAL store migrates cleanly from empty DB, persists and reloads multi-device snapshots transactionally, computes accurate positive-only port bitrates across consecutive polls (including counter resets), and prunes expired telemetry samples.
4. `topology.Solve` deterministically reconstructs a 3-vendor fixture topology (`pfSense` + `OpenWrt switch` + `UniFi AP` + wired & wireless clients) with exact uplink ports, client access ports, and no cycles or duplicate links.
5. `events.Bus` + diff engine reliably emit typed events on device online/offline, port link change, and SFP Rx power alert transitions without blocking publishers when a subscriber stalls.
6. `poller.Scheduler` runs multiple mock drivers concurrently, isolates target failures with bounded backoff, and shuts down cleanly within 100ms of context cancellation.

## 10. Open Questions

1. **Target secret storage:** Should target credentials (SSH key path / password / API key / SNMP v3 auth+priv keys) live in a YAML config file with `${ENV_VAR}` expansion, encrypted in SQLite via a master key env var, or both (YAML config file for GitOps/declarative setups + SQLite for UI-added targets)? *(Default proposal: support YAML config file with `${ENV_VAR}` expansion first in Phase 1, add UI-managed encrypted SQLite targets in `api-server`.)*
2. **SFP optical alert thresholds:** Use standard defaults when transceiver doesn't report its own alarm flags (e.g., warn when `RxPowerDbm < -20.0` or `> 0.0`, or `TempC > 70.0`), overridable per port?
