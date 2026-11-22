# Task List: `core-model`

- [x] Task 1: Bootstrap `go.mod` and Apache-2.0 JSON Schemas (`schema/*.schema.json`)
  - Acceptance: `go.mod` pins `modernc.org/sqlite`; `schema/{device,topology,event}.schema.json` parse as valid JSON Schema 2020-12 with Apache-2.0 SPDX comment.
  - Verify: `python3 -m json.tool` on all schema files; `go mod tidy` succeeds.
  - Files: `go.mod`, `go.sum`, `schema/device.schema.json`, `schema/topology.schema.json`, `schema/event.schema.json`

- [x] Task 2: Implement `internal/model` canonical types and normalizers + unit tests
  - Acceptance: All types from `SPEC-core-model.md` implemented; `NormalizeMAC`, `IsFiberMedia`, `EvaluateSfpAlert`, `ComputeBitrate` pass edge-case tests; slices serialize as `[]` not `null`.
  - Verify: `go test -race -cover ./internal/model`
  - Files: `internal/model/{device,link,segment,l2l3,security,wireless,diagnostic,config,normalize}.go`, `internal/model/model_test.go`

- [x] Task 3: Implement `internal/driver` interfaces, `Snapshot`, redacted `TargetConfig`, and `Registry` + tests
  - Acceptance: `Driver`, `DiagnosticDriver`, `ConfigDriver` match spec; `TargetConfig` never prints secret values in `slog` or JSON; `Registry` rejects duplicate kinds cleanly.
  - Verify: `go test -race -cover ./internal/driver`
  - Files: `internal/driver/{driver,registry}.go`, `internal/driver/registry_test.go`

- [x] Task 4: Implement `internal/store` SQLite WAL store, migrations, bitrate calculation, and pruning + tests
  - Acceptance: Migrations idempotent; transactional per-target snapshot save/load round-trips all entity tables; positive-only bitrates stored in `link_samples`; expired samples pruned.
  - Verify: `go test -race -cover ./internal/store`
  - Files: `internal/store/migrations/0001_init.sql`, `internal/store/store.go`, `internal/store/store_test.go`

- [x] Task 5: Implement `internal/topology` pure-function cross-vendor topology solver + multi-vendor fixture tests
  - Acceptance: Deterministically resolves 4-vendor chain (firewall → LAG → switch → AP → mesh AP + wired/wireless clients), LLDP + FDB-only hops, and redundant STP loops without cycles/duplicates.
  - Verify: `go test -race -cover ./internal/topology`
  - Files: `internal/topology/solver.go`, `internal/topology/solver_test.go`

- [x] Task 6: Implement `internal/events` non-blocking EventBus and state diff engine + tests
  - Acceptance: Slow subscribers drop cleanly without blocking publishers; diff engine emits typed events for device, link, STP, SFP DOM, VPN, and gateway transitions.
  - Verify: `go test -race -cover ./internal/events`
  - Files: `internal/events/{bus,diff}.go`, `internal/events/bus_test.go`

- [x] Task 7: Implement `internal/poller` concurrent scheduler + mock driver tests
  - Acceptance: Polls multiple targets concurrently, isolates errors with bounded backoff, saves to store, emits events, updates topology, and stops within 100ms on cancellation.
  - Verify: `go test -race -cover ./internal/poller`
  - Files: `internal/poller/poller.go`, `internal/poller/poller_test.go`

- [x] Task 8: Final `core-model` verification gate (`-race`, ≥ 85% coverage, `go vet`, `gofmt`) + commit & push
  - Acceptance: All `SPEC-core-model.md` Section 9 success criteria green.
  - Verify: `go test -race -coverprofile=coverage.txt ./internal/... && go tool cover -func=coverage.txt && go vet ./...`
