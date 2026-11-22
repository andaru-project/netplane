# Repository Instructions (`netplane`)

## License Boundary (Strict)

- **AGPL-3.0-or-later** (`LICENSE` at repo root): Go core server (`cmd/`, `internal/`), drivers (`internal/driver/`), embedded web UI (`web/`), CLI, Docker / Home Assistant Add-on packaging (`ha-addon/`).
- **Apache-2.0** (`schema/LICENSE`, `clients/python/LICENSE`, `ha-integration/LICENSE`):
  - `schema/`: OpenAPI / JSON Schema wire contracts.
  - `clients/python/`: Async Python API client library (PyPI-ready for Home Assistant Core).
  - `ha-integration/`: Home Assistant integration (HACS first, upstreamable to Home Assistant Core).
- **Never** import or copy AGPL-licensed code into `schema/`, `clients/python/`, or `ha-integration/`. They communicate with `netplane` strictly over the documented HTTP/WebSocket wire protocol.
- Every source file must carry an `SPDX-License-Identifier:` header matching its directory's license.

## Architecture & Spec Workflow

- `CAPABILITY-MAP.md` is the index of all modules, their dependency direction, and build order.
- Each module has its own spec (`SPEC-<module-id>.md`) approved before implementation.
- Update the relevant spec whenever data models, API contracts, or driver interfaces change.

## Safety & Hygiene

- Never commit real credentials, SSH keys, API tokens, SNMP community strings/auth keys, or private LAN dumps/IPs.
- All driver unit tests must run offline against deterministic fixtures (captured/sanitized command or API outputs). Live device integration tests are opt-in via environment variables.
- Never mutate production network gear during read-only Phase 1 verification.
