# AGENTS.md

doc.go/README are authoritative; this file records what is not derivable from code.

## Commands

```bash
go test -race -cover ./...
go vet ./...
gofmt -l .                                              # any filename printed = unformatted; run before finishing
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go build ./cmd/pgmcp
bin/up                                                  # docker compose up --build -d, follow logs
bin/down                                                # tear down, including volumes
bin/exec <command>                                      # run a command in a throwaway go container
```

Integration tests are skipped unless `PGMCP_TEST_DSN` points at a Postgres with
`pg_stat_statements` preloaded; the scratch-container recipe is in the README's
Testing section. The conformance suite runs against a live HTTP server:

```bash
npx -y @modelcontextprotocol/conformance server --url http://127.0.0.1:8080/mcp \
  --expected-failures .github/conformance-expected-failures.yaml
```

The release pipeline — six binaries, the darwin universal binary, the Claude
Desktop bundle, checksums — can be run end to end without publishing (needs
Node for the pinned `@anthropic-ai/mcpb` CLI):

```bash
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish,docker
```

## Invariants — keep the tests that pin them

Each line is a property the codebase must not lose. If a change makes one of
these tests fail, the change is wrong until proven otherwise.

**Read-only enforcement**

- Every statement the adapter runs is inside a `READ ONLY` transaction, and a write inside it fails with SQLSTATE 25006 — pinned by `TestStoreReadOnly` (`infrastructure/postgres`).
- `statement_timeout` and `lock_timeout` are set on every such transaction, and the transaction is always rolled back — pinned by `TestStoreReadOnly` (`infrastructure/postgres`).
- Only a single top-level `SELECT`/`EXPLAIN`/`SHOW` is allowed; a nested write statement (including one hidden in a CTE), a `FOR UPDATE`/`FOR SHARE` locking clause, a `SELECT INTO`, or a denied function is rejected — pinned by `TestValidate` (`domain/sqlguard`).
- The libpg_query adapter reports the node types, function names and schemas the guard's rules are written against — pinned by `TestParserParse` and `TestParserSatisfiesTheSqlguardParserPort` (`infrastructure/postgres`).

**Nothing leaks back to the model or the log**

- A parse failure never echoes the statement text: the detail is replaced with a fixed phrase, whether the rejection came from the guard or from the port revalidating — pinned by `TestExplain` and `TestQuery` (`application/mcp/tool`).
- The logging middleware records method, duration, ok and user id, and never the arguments or the error text — pinned by `TestLogging` (`application/mcp/middleware`).
- The recover middleware logs the panic's type and stack but never the panic value — pinned by `TestRecover` (`application/mcp/middleware`).
- An unparsable DSN is reported without the connection string in the message — pinned by `TestOpenRejectsAnUnparsableDSNWithoutLeakingIt` (`infrastructure/postgres`).
- `/healthz` reports `degraded` without the failure detail — pinned by `TestHandlerHealth` (`infrastructure/http`).

**Bounds**

- The rate limiter refills per *minute*, not per second, and isolates principals while bucketing every unauthenticated caller as `anonymous` — pinned by `TestRateLimit` (`application/mcp/middleware`).
- Idle principals are evicted, so a churn of callers cannot grow the bucket map without bound — pinned by `TestRateLimit` (`application/mcp/middleware`).
- Rate limiting is installed for HTTP and not for stdio — pinned by `TestNew` (`infrastructure/mcp`).
- Oversized structured content is replaced with a truncation marker on a still-successful call, over a live session as well as in isolation — pinned by `TestOutputCap` (`application/mcp/middleware`).
- Every tool call and resource read gets a bounded context that is cancelled when the handler returns — pinned by `TestTimeout` (`application/mcp/middleware`).
- The HTTP transport refuses a request body over 1 MiB and still answers — pinned by `TestHandlerBodyLimit` (`infrastructure/http`).

**Auth fails secure**

- A static key is compared in constant time against every configured hash with no early exit, and a prefix of a configured key is rejected — pinned by `TestStaticVerifier` (`infrastructure/http`).
- A JWT with the wrong audience, the wrong issuer, a missing or past `exp`, or `alg=none` is rejected — pinned by `TestNewJWTVerifier` (`infrastructure/http`).
- An unreachable JWKS still builds a handler, and that handler rejects every token until a key set arrives — the server starts closed — pinned by `TestNewHandler` (`infrastructure/http`).
- HTTP on a non-loopback listen address with `auth-mode=none` is refused, by the config loader and again by the handler constructor — pinned by `TestLoad` (`infrastructure/config`) and `TestNewHandler` (`infrastructure/http`).
- A request to `/mcp` without a token, or with the wrong one, is refused and points at the resource metadata — pinned by `TestHandlerAuth` (`infrastructure/http`).

**Surface**

- All nine tools register by default, eight with `--disable-query`, and a disabled `query` tool cannot be called at all — pinned by `TestRegister` and `TestNames` (`application/mcp/tool`).
- Every registered tool is annotated read-only, non-destructive, idempotent and closed-world, with a title and an output schema — pinned by `TestRegister` (`application/mcp/tool`).
- The schema allowlist binds `query` **and** `explain`, is matched case-insensitively, refuses an unqualified table reference, and names only the schema back to the caller — pinned by `TestQuerySchemaAllowlist` and `TestExplainSchemaAllowlist` (`application/mcp/tool`).
- Slices in a tool output are empty, never null: plan children, hot nodes and warnings, columns and rows, and the parser's own slices — pinned by `TestExplain`, `TestQuery` (`application/mcp/tool`) and `TestParserNeverReturnsNilSlices` (`infrastructure/postgres`).
- The domain result types marshal the JSON keys the tool output schemas are derived from — pinned by `TestExplainResultMarshalsExpectedTopLevelKeys` and `TestIndexHealthResultMarshalsExpectedTopLevelKeys` (`domain/diagnostics`).
- A configuration error exits 2 and a runtime failure exits 1 — pinned by `TestCommandStartup` (`cmd/pgmcp`).
- The Claude Desktop bundle (`mcpb/manifest.json`) lists exactly the tools the catalogue registers with the same descriptions, launches `server/pgmcp` (`server/pgmcp.exe` on win32) over stdio, takes the DSN as a required sensitive `user_config` field, and sets no environment variable that is not a `PGMCP_*` key fed from `user_config` — pinned by `TestBundleManifest` (`application/mcp/tool`).

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
- Distribution is one `.mcpb` bundle for Claude Desktop (darwin universal + windows/amd64; the MCPB manifest selects by OS, not architecture, and Desktop has no Linux build), packed by `scripts/mcpb-pack.sh` from the `universal_binaries` post hook — the last goreleaser stage before archives/checksums/publish. Unsigned on purpose. The `@anthropic-ai/mcpb` CLI is pinned in the script, not watched by Dependabot. `server.json`'s committed MCPB `fileSha256` is an all-zero placeholder that `release.yml` replaces from the published asset; CI refuses to publish the placeholder.
- Licence MIT `Copyright (c) 2026 Pascal Allen`. README follows the pubsub/pgqueue shape (6 badges, Installation, Usage, Testing, Contributing, License).
