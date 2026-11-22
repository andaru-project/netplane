# Implementation Plan: `core-model`

Traces to: [CAPABILITY-MAP.md](../CAPABILITY-MAP.md), [SPEC-core-model.md](../SPEC-core-model.md)

## Component Order & Dependencies

1. **Module bootstrap & wire schemas (`go.mod`, `schema/`):**
   - Initialize `github.com/andaru-project/netplane` (Go 1.27.4+, matching the `go` directive in `go.mod`, `CGO_ENABLED=0`, `modernc.org/sqlite`).
   - Author Apache-2.0 JSON Schema 2020-12 wire contracts (`device`, `topology`, `event`).
2. **Canonical domain types (`internal/model`):**
   - Pure Go types with zero external dependencies, strict SPDX headers, MAC/media normalizers, SFP optical threshold checks, and wrap/reset-safe bitrate delta helpers.
3. **Driver contract & registry (`internal/driver`):**
   - `Driver`, `DiagnosticDriver`, `ConfigDriver`, `Snapshot`, `TargetConfig` (with redacted `SecretRef` / `slog.LogValuer`), thread-safe `Registry`.
4. **SQLite WAL store (`internal/store`):**
   - Embedded `0001_init.sql` schema, WAL + `foreign_keys = ON`, transactional per-target snapshot replacement, counter-delta rate persistence, automatic `link_samples` retention pruning, topology & diagnostic storage.
5. **Topology solver (`internal/topology`):**
   - Pure, deterministic `Solve(NetworkState)` supporting LAG collapse, LLDP matching, wireless mesh uplinks, FDB cardinality heuristic, leaf access-port wired client attachment, wireless client attachment, and root gateway orientation.
6. **Event bus & diff engine (`internal/events`):**
   - Non-blocking typed pub/sub bus (`Subscribe`/`Publish`) + diff generator detecting device online/offline, link up/down, STP state change, SFP optical warning transitions, VPN tunnel up/down, and gateway failover.
7. **Concurrent polling scheduler (`internal/poller`):**
   - Per-target goroutines with isolated error handling, exponential backoff, jitter, store write → diff/events → topology recompute pipeline, and fast graceful cancellation.

## Risks & Mitigations

- **SQLite write contention under concurrent pollers:** Use WAL mode, `busy_timeout=5000`, and single-writer connection pool (`SetMaxOpenConns(1)` for SQLite file DBs) so concurrent target polls never fail with `SQLITE_BUSY`.
- **Counter resets / 32-bit SNMP wraparound:** Handle both 32-bit wrap (when counters are small/32-bit) and reboot drops cleanly; never persist or return negative bitrates.
- **Non-deterministic topology output:** Sort every node, edge, and member list deterministically by canonical ID so snapshots and UI layouts stay stable across polls.
- **License boundary hygiene:** Keep `schema/` strictly `Apache-2.0` and all Go packages under `internal/` strictly `AGPL-3.0-or-later`.

## Verification Checkpoints

- After each task: `go test -race ./...` and `go vet ./...`.
- Final gate: ≥ 85% statement coverage across `internal/{model,driver,store,topology,events,poller}`, `gofmt -l .` clean, all acceptance criteria in `SPEC-core-model.md` verified.
