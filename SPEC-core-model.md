# Spec: `core-model`

Module id: `core-model` (see [CAPABILITY-MAP.md](CAPABILITY-MAP.md))

## 1. Objective

Provide the vendor-agnostic foundation of `netplane` capable of faithfully representing **UniFi**, **OpenWrt/DSA**, **RTLPlayground**, **Andaru**, **pfSense**, **OPNsense**, and **SNMP v2c/v3** hardware without faking vendor models or losing platform-specific operational depth:

1. **Three-state separation (`observed`, `live`, `saved`):** Distinguish live hardware/kernel observations from currently active configuration and pending version-checked saved changes (supporting preview, timed rollback, and explicit confirmation cleanly in Phase 2—matching Andaru's safety model natively).
2. **Canonical capability-based network model:** Model L1/L2 physical links, LAG/LACP trunks, STP state, SFP optical DOM/DDMI, PoE budgets, L3 logical segments/VLANs (dual-stack IPv4 + IPv6), security zones, FDB, LLDP, ARP/NDP neighbors, DHCP leases, gateways, routes, VPN tunnels, firewall/service runtime state, wireless radios/SSIDs/mesh uplinks, clients, multi-sensor chassis health, and diagnostics.
3. **Driver & polling contract:** Define the Go interfaces (`Driver`, optional `DiagnosticDriver`, optional `MinIntervalDriver`, optional `ConfigDriver` for Phase 2) and concurrent polling scheduler.
4. **Embedded persistence (`SQLite` WAL):** Persist targets (with named secret references), latest normalized snapshots, rolling port/sensor telemetry samples, topology graphs, and diagnostic history in a single zero-dependency database file.
5. **Cross-vendor L2/L3/Wi-Fi topology solver:** Correlate LLDP, LAG trunks, switch FDB, wireless uplinks, wireless client associations, and ARP/NDP/DHCP tables across mixed vendors into a deterministic directed graph with evidence and confidence scores.
6. **Live event bus:** Fan out typed state transitions (device online/offline, link up/down, STP state change, SFP optical threshold alarm, VPN tunnel down, gateway failover, topology change, client join/leave) without blocking publishers.

**Phase 1 non-goals:** Executing active config mutations on devices (Phase 2 implements `ConfigDriver` methods on drivers; `core-model` defines the interfaces and immutable saved/preview types now so Phase 2 requires zero schema rewrites).

## 2. Tech Stack

- **Language:** Go `1.24+` (developed on `1.27.4`, cross-compiled with `CGO_ENABLED=0` for `linux/amd64`, `linux/arm64`, `darwin/arm64`).
- **Database:** Pure-Go SQLite (`modernc.org/sqlite`) with WAL mode, foreign keys enabled, and embedded versioned SQL migrations (`embed.FS`).
- **Logging:** Go stdlib `log/slog`.
- **Schema contract:** JSON Schema 2020-12 / OpenAPI 3.1 definitions under `schema/` (`Apache-2.0`).
- **External dependencies policy:** Stdlib-first; `modernc.org/sqlite` is the only external Go dependency in `core-model`.

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
schema/                        # Apache-2.0 JSON Schema 2020-12 wire contracts
  device.schema.json
  topology.schema.json
  event.schema.json
internal/
  model/                       # Canonical types, Capability enum, normalizers, validation
    device.go                  # Device, FirmwareInfo, PoEBudget, SensorReading, SecretRef
    link.go                    # Link (physical L1/L2 port), LagGroup, StpState, SfpDom, PortPoE, Counters
    segment.go                 # Segment (VLAN + logical L3 interface), IPAddress, Zone, HAState
    l2l3.go                    # FdbEntry, LldpNeighbor, NeighborEntry (ARP/NDP), DhcpLease, GatewayStatus, Route
    security.go                # VpnTunnel, FirewallState, ServiceStatus
    wireless.go                # WirelessRadio, Ssid, WirelessUplink, Client
    diagnostic.go              # DiagnosticKind, DiagnosticRequest, DiagnosticResult
    config.go                  # StatePlane (observed/live/saved), ChangePlan, ApplySession (Phase 2 contract)
    normalize.go               # Canonical MAC ("aa:bb:cc:dd:ee:ff"), media/fiber helpers, rate delta math
    normalize_test.go
  driver/                      # Driver, DiagnosticDriver, ConfigDriver, Registry, TargetConfig, Snapshot
    driver.go
    registry.go
    registry_test.go
  store/                       # SQLite WAL persistence, embedded migrations, repositories, rolling pruning
    store.go
    migrations/
      0001_init.sql
    store_test.go
  topology/                    # Pure-function cross-vendor L2/L3/Wi-Fi topology solver (LAG + mesh aware)
    solver.go
    solver_test.go
  events/                      # Non-blocking typed pub/sub EventBus + state diff detector
    bus.go
    diff.go
    bus_test.go
  poller/                      # Concurrent driver polling scheduler with backoff, jitter & error isolation
    poller.go
    poller_test.go
```

## 5. Canonical Data Contracts

### 5.1 Capabilities

Every `Device` advertises an explicit list of `Capability` constants so UI screens, diagnostics, HA entity discovery, and Phase 2 control actions only expose what the hardware/driver actually supports:

- **L1 / L2 Switching:** `cap:links`, `cap:sfp_dom`, `cap:eee`, `cap:flow_control`, `cap:poe`, `cap:lag`, `cap:stp`, `cap:vlans`, `cap:fdb`, `cap:lldp`
- **L3 / Routing / Firewall:** `cap:segments`, `cap:zones`, `cap:neighbors` (ARP/NDP), `cap:dhcp`, `cap:gateway`, `cap:routes`, `cap:vpn`, `cap:firewall`, `cap:ha` (CARP/VRRP), `cap:dns`
- **Wireless:** `cap:wireless`, `cap:wireless_mesh`
- **Hardware / System:** `cap:sensors`, `cap:firmware_info`
- **Diagnostics:** `cap:diag_ping`, `cap:diag_traceroute`, `cap:diag_cable_test`, `cap:diag_fdb_lookup` (a driver advertises the kinds its `RunDiagnostic` implements; the device's account may still lack the permission)
- **Phase 2 Control:** `cap:config_preview`, `cap:config_apply`, `cap:config_rollback_timer`, `cap:config_backup`

### 5.2 Core Types

- **`Device`**:
  - `ID` (stable canonical ID, e.g. `driver:primary-mac` or `driver:target-key`), `Driver` (`openwrt`, `rtlplayground`, `andaru`, `pfsense`, `opnsense`, `unifi`, `snmp`), `TargetID`, `Name`, `Hostname`, `Vendor`, `Model`, `Role` (`switch`, `router`, `gateway`, `firewall`, `ap`, `combo`, `unknown`), `PrimaryMAC`, `MACs []string` (all owned interface MACs for topology joins), `MgmtIP`, `Firmware FirmwareInfo` (`Current`, `Available`, `HardwareRev`, `Serial`), `UptimeSec`, `Online bool`, `LastSeen time.Time`, `Capabilities []Capability`, `SystemStats` (`CPUPct`, `MemUsedBytes`, `MemTotalBytes`, `Load1`), `Sensors []SensorReading` (`Name`, `Kind`: `temp_c|fan_rpm|voltage_v|power_w|psu_ok`, `Value float64`, `Unit`, `Status`: `ok|warn|crit`), `PoEBudget *PoEBudget` (`TotalWatts`, `UsedWatts`), `PendingChanges *bool`, `Extensions map[string]any`.
- **`Link`** (physical/L2 switch or NIC port):
  - `DeviceID`, `Index int` (1-based faceplate order), `IfName` (`lan1`, `sfp1`, `igc0`, `eth0`), `Label`, `MAC`, `AdminUp bool`, `LinkUp bool`, `OperState` (`up|down|no_carrier|disabled|missing|unknown`), `SpeedMbps int`, `Duplex` (`full|half|unknown`), `Autoneg *bool`, `MTU *int`, `Media` (`FE|GE|2.5GE|5GE|10GE|25GE|40GE|100GE|SFP|SFP+|SFP28|QSFP+|QSFP28|other`), `IsFiber bool`, `Isolated *bool`, `LagGroupID string`, `StpState` (`forwarding|blocking|discarding|learning|listening|disabled|""`), `StpRole` (`root|designated|alternate|backup|""`), `EEE *bool`, `FlowControl *FlowControlState` (`Rx`, `Tx`, `Autoneg`), `PVID *int`, `TaggedVLANs []int`, `PoE *PortPoE` (`Supported`, `Enabled`, `State`, `Watts`, `Class`), `SFP *SfpDom` (`Present`, `Vendor`, `PartNumber`, `Serial`, `WavelengthNm`, `TempC`, `VoltageV`, `BiasMa`, `TxPowerDbm`, `RxPowerDbm`, `AlertState`: `ok|warn|crit`), `Counters *PortCounters` (`RxBytes`, `TxBytes`, `RxPackets`, `TxPackets`, `RxErrors`, `TxErrors`, `RxDropped`, `TxDropped`, `RxPause`, `TxPause`, `CrcErrors`), `Rates *PortRates` (`RxBps`, `TxBps`).
- **`LagGroup`**:
  - `DeviceID`, `ID`, `Name` (`bond0`, `lag1`, `lagg0`), `Mode` (`lacp|static`), `MemberLinks []string`, `LinkUp bool`, `SpeedMbps int`.
- **`Segment`** (VLAN / logical L3 interface — maps cleanly to Andaru segments, pfSense/OPNsense interfaces, OpenWrt `network.*` interfaces, UniFi networks):
  - `DeviceID`, `ID`, `Name` (`wan`, `lan`, `vlan20-iot`, `wg0`), `Kind` (`ethernet|vlan|bridge|lag|pppoe|wireguard|loopback|other`), `ParentLink` (e.g. `igc0`, `bond0`), `VLANID *int`, `ZoneID string`, `MTU *int`, `AdminUp bool`, `OperState`, `Addresses []IPAddress` (`Family: ipv4|ipv6`, `Address`, `PrefixLength`, `Scope`, `Source: static|dhcp|dhcpv6|slaac|pppoe|carp`), `HAState *HAState` (`Protocol: carp|vrrp`, `VHID`, `State: master|backup|init`, `VIP`), `IsManagement bool`.
- **`Zone`**:
  - `DeviceID`, `ID`, `Name` (`wan`, `lan`, `iot`, `dmz`), `SegmentIDs []string`.
- **`L2 / L3 / Security / Wireless / Client`**:
  - `FdbEntry`: `DeviceID`, `LinkName`, `MAC`, `VLAN`, `IsLocal bool`, `AgeSec *int`.
  - `LldpNeighbor`: `DeviceID`, `LocalLink`, `ChassisID`, `RemotePort`, `RemoteSysName`, `RemoteMgmtIP`.
  - `NeighborEntry` (dual-stack ARP + IPv6 NDP): `DeviceID`, `Family` (`ipv4|ipv6`), `IP`, `MAC`, `SegmentName`, `State` (`reachable|stale|incomplete|permanent|unknown`).
  - `DhcpLease`: `DeviceID`, `Family` (`ipv4|ipv6`), `IP`, `MAC`, `Hostname`, `ExpiresAt *time.Time`, `Pool`, `Static bool`.
  - `GatewayStatus`: `DeviceID`, `Name`, `SegmentName`, `Family` (`ipv4|ipv6`), `GatewayIP`, `MonitorIP`, `Online bool`, `RTTMs *float64`, `LossPct *float64`, `IsDefault bool`.
  - `Route`: `DeviceID`, `Family` (`ipv4|ipv6`), `Destination` (CIDR), `Gateway`, `SegmentName`, `Protocol` (`static|connected|bgp|ospf|kernel|other`), `Metric int`.
  - `VpnTunnel`: `DeviceID`, `ID`, `Name`, `Kind` (`wireguard|openvpn|ipsec|tailscale|other`), `Peer`, `Endpoint`, `Up bool`, `LastHandshake *time.Time`, `RxBytes uint64`, `TxBytes uint64`.
  - `FirewallState`: `DeviceID`, `Engine` (`nftables|pf|iptables|other`), `StatesUsed *int64`, `StatesMax *int64`, `PolicyRuleCount *int`, `NatRuleCount *int`, `PendingSavedChanges *bool`, `SavedVersion string`, `Services []ServiceStatus` (`Name`, `State: running|stopped|degraded|unknown`, `Summary`).
  - `WirelessRadio`: `DeviceID`, `Name`, `Band` (`2.4GHz|5GHz|6GHz`), `Channel`, `WidthMHz`, `TxPowerDbm`, `NoiseFloorDbm *int`, `UtilizationPct *float64`, `SSIDs []Ssid` (`Name`, `Enabled`, `VLANID *int`, `Security`, `ClientCount`).
  - `WirelessUplink`: `DeviceID`, `PeerDeviceID`, `PeerMAC`, `Band`, `SignalDbm`, `TxRateMbps`, `RxRateMbps`.
  - `Client`: `MAC`, `IPs []string` (IPv4 + IPv6), `Hostname`, `VendorOUI`, `Online bool`, `ConnectionType` (`wired|wireless`), `AttachedDeviceID`, `AttachedLink`, `SSID`, `VLAN *int`, `SignalDbm *int`, `RxRateMbps *int`, `TxRateMbps *int`, `LastSeen time.Time`.
- **`DiagnosticRequest` / `DiagnosticResult`**:
  - `Kind` (`ping|traceroute|cable_test|fdb_lookup`), `DeviceID`, `Target`, `Params map[string]string` → `OK bool`, `DurationMs int64`, `Summary string`, `Structured any`, `RawOutput string`, `Error string`.
- **Named Secret Reference (`SecretRef`):**
  - Config objects reference secrets by name (`{"secret":"PFSENSE_API_KEY"}`), never embedding raw credentials in snapshots or API read responses. Read-back metadata exposes only `IsSet`, `UpdatedAt`, and `Version`.

### 5.3 Driver & Optional Control Interfaces

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
package driver

import (
	"context"
	"time"

	"github.com/andaru-project/netplane/internal/model"
)

// Inventory is the observed state a snapshot carries. store.NetworkState embeds it too,
// aggregating every target's snapshot with Append and FillNil (nil slices encode as []).
type Inventory struct {
	Devices         []model.Device
	Links           []model.Link
	LagGroups       []model.LagGroup
	Segments        []model.Segment
	Zones           []model.Zone
	FDB             []model.FdbEntry
	LLDP            []model.LldpNeighbor
	Neighbors       []model.NeighborEntry
	DHCP            []model.DhcpLease
	Gateways        []model.GatewayStatus
	Routes          []model.Route
	VPNs            []model.VpnTunnel
	Firewalls       []model.FirewallState
	Radios          []model.WirelessRadio
	WirelessUplinks []model.WirelessUplink
	Clients         []model.Client
}

// Snapshot is an atomic point-in-time observed/live read returned by a Driver.
// One TargetID (for example, a UniFi controller) may yield multiple devices.
type Snapshot struct {
	TargetID    string
	CollectedAt time.Time
	Inventory

	Unavailable   []Section             // sections the driver could not refresh (MarkUnavailable)
	Stale         map[Section]time.Time // carried sections -> when their data was last fresh
	MarkedOffline bool                  // rewritten by MarkTargetOffline; never a carry-over source
	PollInterval  time.Duration         // set by the poller; sizes the one-time carry window
}

// Section names a snapshot slice by its JSON field ("segments", "zones", "fdb", "lldp",
// "neighbors", "dhcp", "gateways", "vpns", "firewalls", "radios", "clients").
type Section string

// CarryOver (called by the store before saving) fills unavailable sections from the previous
// snapshot: data fresh there is carried once (if that poll is no older than three poll intervals or 15 minutes, whichever is longer); data it had
// itself carried keeps its original freshness and expires after 15 minutes.
func (s *Snapshot) CarryOver(prev *Snapshot, maxAge time.Duration)

// Driver is implemented by every platform integration.
type Driver interface {
	Kind() string
	ValidateConfig(cfg TargetConfig) error
	Poll(ctx context.Context, cfg TargetConfig) (*Snapshot, error)
}

// DiagnosticDriver is implemented by drivers supporting device-side diagnostics.
type DiagnosticDriver interface {
	RunDiagnostic(ctx context.Context, cfg TargetConfig, req model.DiagnosticRequest) (*model.DiagnosticResult, error)
}

// MinIntervalDriver is implemented by drivers whose devices must not be polled more often
// than a floor; the scheduler raises a target's configured interval to it (manual polls are not limited).
type MinIntervalDriver interface {
	MinPollInterval() time.Duration
}

// ConfigDriver is the Phase 2 contract for drivers supporting safe save -> preview -> apply -> confirm/rollback.
type ConfigDriver interface {
	PreviewChange(ctx context.Context, cfg TargetConfig, change model.ChangeRequest) (*model.ChangePreview, error)
	ApplyChange(ctx context.Context, cfg TargetConfig, change model.ChangeRequest) (*model.ApplySession, error)
	ConfirmApply(ctx context.Context, cfg TargetConfig, sessionID string) error
	RollbackApply(ctx context.Context, cfg TargetConfig, sessionID string) error
}
```

### 5.4 Topology Solver Algorithm (Deterministic Pure Function)

`topology.Solve(state)` builds nodes (`Device` + optionally `Client`) and directed edges with explicit `Kind` (`wired`, `lag`, `wireless_uplink`, `wireless_client`), `Evidence` (`lldp`, `fdb_unique`, `fdb_inferred`, `wireless_uplink`, `wireless_client`), and `Confidence` (`0.0–1.0`):

1. **LAG-aware link normalization:** Map member `Link` names in `LLDP` / `FDB` onto their parent `LagGroup` when applicable so bonded uplinks collapse into a single `lag` edge (listing constituent member links) rather than duplicate parallel edges.
2. **Direct LLDP edges (`confidence: 1.0`):** Match `LldpNeighbor.ChassisID` / `RemoteMgmtIP` / `RemoteSysName` against known `Device.MACs` / `MgmtIP` / `Hostname`. Deduplicate bidirectional pairs cleanly. Distinct port pairs between the same two devices stay separate edges.
3. **Wireless mesh uplinks (`confidence: 0.95`):** Attach APs reporting `WirelessUplink` to their peer AP.
4. **FDB infrastructure edges (`confidence: 0.85`):** For known device `B` appearing in `A`'s FDB (ignoring `IsLocal` entries and ports already linked to a downstream switch that sees `B`), select the port with the smallest downstream MAC cardinality.
5. **Wireless client attachment (`confidence: 0.95`):** Attach wireless `Client` records to their reporting AP + SSID.
6. **Wired client attachment (`confidence: 0.80`):** Attach wired MACs (from `Neighbors` / `DHCP` / `FDB` not matching infrastructure `Device.MACs`) to the leaf access link (no downstream infrastructure device) where the MAC appears in `FDB`. FDB and neighbor records of an offline device place clients but never mark them online.
7. **Root orientation:** Prefer `Role` in `{gateway, firewall}` (or `cap:gateway`), breaking ties by reachability, and sort all nodes/edges deterministically by ID.

### 5.5 SQLite Storage & Rolling Telemetry

- `0001_init.sql` (WAL mode, `PRAGMA foreign_keys = ON`):
  - `targets` (driver kind, display name, non-secret JSON config, secret refs, poll interval, enabled flag)
  - Normalized state tables replaced transactionally per `target_id` on poll: `devices`, `links`, `lag_groups`, `segments`, `zones`, `fdb_entries`, `lldp_neighbors`, `neighbors`, `dhcp_leases`, `gateways`, `routes`, `vpn_tunnels`, `firewalls`, `radios`, `wireless_uplinks`, `clients`
  - `link_samples` (rolling time-series of `rx_bps`, `tx_bps`, `rx_errors`, `tx_errors`, `sfp_rx_dbm`, `sfp_temp_c`, `poe_watts` — default 24h retention, pruned automatically)
  - `topology_snapshots` (latest solved graph JSON + timestamp)
  - `diagnostic_runs` (audit/history of executed diagnostic requests and results, pruned with the same retention as `link_samples`)
- Counter delta bitrate math handles 32-bit/64-bit counter wraparound and device reboot resets without ever emitting negative rates.
- Default SFP optical warning thresholds (used only when transceiver doesn't expose its own alarm flags, overridable per link): `RxPowerDbm < -20.0` or `RxPowerDbm > 0.0`, or `TempC > 70.0`.

## 6. Code Style

- Every Go file starts with `// SPDX-License-Identifier: AGPL-3.0-or-later`.
- Every JSON Schema file under `schema/` carries `"$comment": "SPDX-License-Identifier: Apache-2.0"`.
- Normalize all MAC addresses via `model.NormalizeMAC` (`aa:bb:cc:dd:ee:ff`); a malformed MAC never fails a poll: optional MAC fields are cleared and MAC-keyed FDB/client entries are dropped. Empty slices serialize as `[]` rather than `null` for consistent wire contracts.
- A driver that cannot refresh one section (a best-effort call failed) marks it in `Snapshot.Unavailable` instead of dropping it or failing the poll; the store carries that section over from the previous snapshot (once for data fresh in that poll, at most 15 minutes for data already carried), recording when it was last fresh in `Snapshot.Stale`. Carried clients keep their last online state for that time. Device fields derived from a failed call (role, capabilities) are still computed from what the poll got. One exception fails the poll instead: a failed SNMP `ifXTable` walk, because ifName is the link identity (link names, counter history, FDB and LLDP ports). So a failed client list or neighbour table does not look like everything leaving.
- A target whose polls fail twice in a row has its stored devices and clients marked offline (emitting `device.offline` / `client.left`); the stored config, snapshot and device rows of targets removed from or disabled in the config (and the stored topology) are pruned at startup, while their link and diagnostic history ages out through retention.
- Optional hardware telemetry fields use pointers (`*bool`, `*float64`, `*SfpDom`, `*PortPoE`) so absence means "unsupported/unavailable", never a false zero.

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
package model

import (
	"fmt"
	"net"
	"strings"
)

// NormalizeMAC parses colon, hyphen, Cisco-dot, or bare 12-hex MAC strings
// into lowercase colon-separated "aa:bb:cc:dd:ee:ff".
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

- **Framework:** Go stdlib `testing` with `-race`.
- **Unit & integration coverage inside `core-model`:**
  1. `internal/model`: table-driven tests for MAC normalization, `IsFiber` classification, SFP optical alert evaluation, bitrate delta calculation (normal, 32-bit wrap, 64-bit wrap, reboot reset), and JSON serialization (`[]` never `null` on slices).
  2. `internal/store`: temp-dir SQLite WAL tests for migration idempotency, transactional snapshot replacement across all tables, rate calculation persistence, and retention pruning.
  3. `internal/topology`: deterministic multi-vendor fixtures:
     - 4-vendor chain: `Andaru/pfSense (firewall)` → (LAG trunk) `RTLPlayground/OpenWrt switch` → `UniFi AP` → mesh AP + wired/wireless clients.
     - Mixed LLDP + FDB-only hops, STP-blocked redundant loop links (no panic, no duplicate edges).
  4. `internal/events`: typed pub/sub, non-blocking slow-subscriber drop safety, diff detection (device up/down, link up/down, STP change, SFP optical warning, VPN down, gateway failover).
  5. `internal/poller`: concurrent mock drivers, per-target failure isolation, exponential backoff, clean `ctx.Done()` shutdown.
- **Target coverage:** ≥ 85% statement coverage across `internal/{model,store,topology,events,poller}`.

## 8. Boundaries

- **Always:**
  - Run `go test -race ./...` and `go vet ./...` before committing.
  - Keep `SPDX-License-Identifier` headers accurate per license boundary.
  - Keep `topology.Solve` deterministic and side-effect free.
- **Ask first:**
  - Adding Go dependencies beyond `modernc.org/sqlite`.
  - Breaking changes to `schema/` wire contracts or SQLite migrations once released.
- **Never:**
  - Leak secret values into logs, SQLite snapshot JSON, or API DTOs.
  - Import AGPL code into `schema/`, `clients/python/`, or `ha-integration/`.
  - Skip or weaken failing tests.

## 9. Success Criteria

`core-model` is complete when:

1. `go test -race ./...` and `go vet ./...` pass with ≥ 85% coverage and zero race warnings.
2. `schema/` contains valid Apache-2.0 JSON Schema 2020-12 files aligned with the canonical Go structs.
3. SQLite WAL store migrates cleanly, persists/reloads multi-vendor snapshots transactionally, computes non-negative port bitrates across wrap/reset boundaries, and prunes expired telemetry samples.
4. `topology.Solve` deterministically reconstructs the multi-vendor LAG + LLDP + FDB + Wi-Fi mesh + dual-stack client fixture with exact edges, confidence scores, and zero cycles/duplicates.
5. `events.Bus` + diff engine emit typed state-change events reliably without blocking slow subscribers.
6. `poller.Scheduler` isolates per-target failures with bounded backoff and shuts down within 100ms on cancellation.
