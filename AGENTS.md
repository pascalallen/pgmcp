# AGENTS.md

doc.go/README are authoritative; this file records what is not derivable from code.

## Commands

```bash
go test -race -cover ./...
go vet ./...
gofmt -l .                                              # any filename printed = unformatted; run before finishing
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
bin/up                                                  # docker compose up --build -d, follow logs
```

## Invariants — keep the tests that pin them

filled as tasks land.

## Design decisions

- Official `modelcontextprotocol/go-sdk` (v1.7.0) rather than mark3labs/mcp-go — tier-1 SDK, tracks spec releases same-day, ships RFC 9728 bearer middleware and conformance tooling.
- SQL parsing via `github.com/wasilibs/go-pgquery` (pure-Go wazero build of libpg_query) so `CGO_ENABLED=0` holds everywhere; costs ~15 MB of binary.
- Read-only is enforced three independent ways: dedicated `pg_monitor` role, `default_transaction_read_only` + per-call `READ ONLY` transaction, and a parser-level statement/function guard (a read-only tx alone does not stop `pg_terminate_backend`/`pg_read_file`/`pg_sleep`).
- Streamable HTTP runs stateless (spec 2026-07-28); no sessions, no EventStore.

## Conventions

- Module `github.com/pascalallen/pgmcp`; `go 1.25` directive (floor required by go-sdk) — never bump to "newest".
- `CGO_ENABLED=0` for every build; no cgo dependency may be introduced.
- Domain packages (`internal/pgmcp/domain/...`) import **stdlib only** (plus sibling domain packages). Application imports domain + `go-sdk/mcp`. Infrastructure imports anything.
- Every SQL the adapter runs goes through `postgres.Store.readOnly(ctx, timeout, fn)` → `BEGIN READ ONLY` + `SET LOCAL statement_timeout` + `SET LOCAL lock_timeout='2s'` + `ROLLBACK`. No exceptions.
- Every tool: `Annotations{ReadOnlyHint:true, DestructiveHint:&false, IdempotentHint:true, OpenWorldHint:&false, Title}` via the shared `tool.readOnly(title)` helper; typed `In`/`Out`; `Out` embeds `tool.Meta`; slices in `Out` are never nil.
- Never log tool arguments, SQL text, or result rows. Log `{tool, duration_ms, ok, user_id}` only.
- Tests: testify (`require`/`assert`), descriptive names, `t.Run` subtests that read as sentences. Test-first. `go test -race -cover ./...`, `gofmt -l .` empty, `go vet ./...`, `staticcheck ./...` green before every commit.
- Integration tests gate on `PGMCP_TEST_DSN`; `t.Skip` when unset.
- Workflow: GitHub Issue → `feature/<issue#>-<slug>` → PR referencing the issue. **Agents never merge.** No `Co-Authored-By` trailers. Commit subjects imperative, reference issue `(#n)`.
- `wire_gen.go` is generated (`go generate ./internal/pgmcp/infrastructure/container/...`), never hand-edited.
- Licence MIT `Copyright (c) 2026 Pascal Allen`. README follows the pubsub/pgqueue shape (6 badges, Installation, Usage, Testing, Contributing, License).
