# pgmcp

[![Go Reference](https://pkg.go.dev/badge/github.com/pascalallen/pgmcp.svg)](https://pkg.go.dev/github.com/pascalallen/pgmcp)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/pascalallen/pgmcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/pascalallen/pgmcp)](https://goreportcard.com/report/github.com/pascalallen/pgmcp)
![GitHub Workflow Status (with branch)](https://img.shields.io/github/actions/workflow/status/pascalallen/pgmcp/go.yml?branch=main)
![GitHub](https://img.shields.io/github/license/pascalallen/pgmcp)
![GitHub code size in bytes](https://img.shields.io/github/languages/code-size/pascalallen/pgmcp)

pgmcp is a read-only PostgreSQL ops/DBA server for the Model Context Protocol, built on the official [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk). It answers the questions a DBA asks during an incident — which statements are slow, why this plan is slow, which indexes are dead weight, which tables autovacuum has fallen behind on, who is blocking whom, how far the standby is lagging — and it is read-only by construction rather than by convention: a dedicated database role that holds no write privilege, a `BEGIN READ ONLY` transaction with a statement timeout around every single statement, and a SQL parser guard that rejects anything that is not one `SELECT`/`EXPLAIN`/`SHOW` and every function that could mutate state from inside a read-only transaction. Tested against PostgreSQL 16; requires 13 or later.

## Installation

Download a binary for your platform from [Releases](https://github.com/pascalallen/pgmcp/releases) — `darwin`, `linux` and `windows`, `amd64` and `arm64`, with checksums.

Or build from source with the Go CLI tool [go](https://go.dev/dl/):

```bash
go install github.com/pascalallen/pgmcp/cmd/pgmcp@latest
```

Or run the released image, which is distroless, non-root and multi-arch:

```bash
docker run --rm -i -e PGMCP_DATABASE_URL='postgres://…' ghcr.io/pascalallen/pgmcp
```

pgmcp is listed in the MCP Registry as `io.github.pascalallen/pgmcp`.

Before you point it at anything, create the read-only role — see [Database role](docs/DEPLOYING.md#database-role). It is the layer that still holds if the other two have a bug.

## Usage

One MCP surface, two transports. Which one you run is configuration, not a different build.

**Claude Code, stdio** — the client launches the binary and talks over stdin/stdout:

```bash
claude mcp add pgmcp --transport stdio \
  --env PGMCP_DATABASE_URL='postgres://pgmcp:…@db.internal:5432/app?sslmode=require' \
  -- pgmcp
```

**Claude Desktop, stdio** — the same thing in `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "pgmcp": {
      "command": "pgmcp",
      "env": {
        "PGMCP_DATABASE_URL": "postgres://pgmcp:…@db.internal:5432/app?sslmode=require"
      }
    }
  }
}
```

**HTTP** — Streamable HTTP behind a static bearer key, for a shared deployment. pgmcp speaks plain HTTP and never terminates TLS itself; run it on loopback behind a reverse proxy.

```bash
PGMCP_DATABASE_URL='postgres://pgmcp:…@db.internal:5432/app?sslmode=require' \
PGMCP_AUTH_MODE=static \
PGMCP_API_KEYS="$(openssl rand -hex 32)" \
  pgmcp --transport http --listen 127.0.0.1:8080
```

```bash
claude mcp add pgmcp --transport http https://pgmcp.example.com/mcp \
  --header "Authorization: Bearer <key>"
```

TLS termination, the proxy settings the streaming transport needs, JWT auth against an identity provider, and wiring pgmcp up as a claude.ai custom connector are all in [docs/DEPLOYING.md](docs/DEPLOYING.md).

## Tools

| Tool | The question it answers |
| --- | --- |
| `top_queries` | Which statements are slow or expensive server-wide? Ranks `pg_stat_statements` by total time, mean time, calls, rows or blocks read. |
| `explain` | Why is *this* statement slow? Plan tree, the nodes burning the most self time, plan warnings, and a stable `plan_hash` to diff against a later run. |
| `index_health` | Which indexes can I drop, and which are missing their job? Never-scanned, duplicate, invalid and bloated indexes. |
| `table_health` | Where is autovacuum falling behind? Dead-tuple ratio, last vacuum/analyze, sequential versus index scans and estimated bloat, per table. |
| `lock_waits` | Why is this query hanging? The current lock wait graph — who is blocked, who blocks them, and any cycle that amounts to a deadlock. |
| `connections` | What is the server doing right now, and how close is it to `max_connections`? Backends grouped by state, wait event, application, user or database, including idle-in-transaction sessions. |
| `replication` | How far behind is the standby, and which slot is retaining WAL? Primary/standby role, per-standby lag in bytes and milliseconds, slots and the current WAL rate. |
| `config_check` | Is this server tuned sanely? `pg_settings` against memory, autovacuum, WAL and connection heuristics, with an `ok`/`review`/`warn` verdict and a note per setting. |
| `query` | Everything the other eight do not cover. One read-only `SELECT`/`EXPLAIN`/`SHOW` in a `READ ONLY` transaction, bounded by a row cap and a statement timeout, with `$1..$n` bind parameters. |

Every tool is annotated `readOnlyHint: true`, `destructiveHint: false`, `idempotentHint: true`, `openWorldHint: false`, and returns a typed output schema.

`query` is the only tool that carries free-form SQL, and it is optional. `--disable-query` drops it from the catalogue entirely — a deployment that only needs the eight diagnostics tools can run without any ad hoc SQL surface at all. `--query-schemas=public,app` restricts it to named schemas instead — and bounds `explain` with it, since `analyze=true` executes the statement; read what that does and does not stop in [docs/SECURITY.md](docs/SECURITY.md).

## Resources & prompt

| Resource | Contents |
| --- | --- |
| `pgmcp://overview` | The server snapshot to start from: version, uptime, recovery state, installed extensions, per-database sizes, cache hit ratio, and connections against `max_connections`. Cacheable for 30s. |
| `pgmcp://settings` | The raw `pg_settings` rows. Cacheable for 5m. |

| Prompt | Arguments | Purpose |
| --- | --- | --- |
| `diagnose_slow_query` | `sql` (required) | A four-step investigation: `explain` the statement, check `index_health`/`table_health` on every schema in the plan's hot nodes, look for it in `top_queries`, then summarize root cause, evidence and a recommended index or rewrite — written out as text only, never run. |

## Configuration

Every setting has a `--flag` and a `PGMCP_<KEY>` environment variable. Flags win over the environment, which wins over the default. Configuration errors exit `2` with every offending key named in one message; runtime failures exit `1`.

| Flag | Env | Default | Meaning |
| --- | --- | --- | --- |
| `--database-url` | `PGMCP_DATABASE_URL` | — (**required**) | Postgres connection string |
| `--transport` | `PGMCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `--listen` | `PGMCP_LISTEN` | `127.0.0.1:8080` | HTTP listen address |
| `--resource-url` | `PGMCP_RESOURCE_URL` | — | Public origin this server is reachable at, for OAuth resource metadata |
| `--auth-mode` | `PGMCP_AUTH_MODE` | `none` | `none`, `static` or `jwt` (HTTP only) |
| `--api-keys` | `PGMCP_API_KEYS` | — | Comma-separated static API keys, required for `static` |
| `--jwks-url` | `PGMCP_JWKS_URL` | — | JWK set URL, required for `jwt` |
| `--jwt-issuer` | `PGMCP_JWT_ISSUER` | — | Required `iss` claim, required for `jwt` |
| `--jwt-audience` | `PGMCP_JWT_AUDIENCE` | — | Required `aud` claim, required for `jwt` |
| `--auth-servers` | `PGMCP_AUTH_SERVERS` | — | Comma-separated OAuth authorization servers to advertise via RFC 9728 |
| `--disable-query` | `PGMCP_DISABLE_QUERY` | `false` | Drop the ad hoc `query` tool entirely |
| `--query-schemas` | `PGMCP_QUERY_SCHEMAS` | — | Comma-separated schemas the `query` and `explain` tools may read; unset disables the allowlist |
| `--max-conns` | `PGMCP_MAX_CONNS` | `4` | Maximum Postgres connections |
| `--call-timeout` | `PGMCP_CALL_TIMEOUT` | `60s` | Per-tool-call timeout |
| `--rate-limit` | `PGMCP_RATE_LIMIT` | `60` | Tool calls per principal per minute (HTTP only) |
| `--max-output-bytes` | `PGMCP_MAX_OUTPUT_BYTES` | `1048576` | Cap on a tool call's structured content |
| `--log-level` | `PGMCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `--log-format` | `PGMCP_LOG_FORMAT` | `text` | `text` or `json` |
| `--insecure-no-auth` | `PGMCP_INSECURE_NO_AUTH` | `false` | Allow `auth-mode=none` on a non-loopback listen address |
| `--version` | — | — | Print the version and exit |

The auth block applies to the HTTP transport only. Over stdio the operating system decides who the caller is: the parent process that launched the binary, and nobody else.

## Security model

- **Read-only three independent ways.** A dedicated role with no write privilege (`pg_monitor` plus `SELECT`, and deliberately *not* `pg_signal_backend`); `BEGIN READ ONLY` with `SET LOCAL statement_timeout` and `lock_timeout = '2s'` around every statement the adapter runs, always rolled back; and a parser-level guard, because a read-only transaction alone does not stop `pg_terminate_backend`, `pg_read_file`, `pg_sleep` or `setval`.
- **The SQL guard is allow-list first.** One top-level statement, and it must be a `SELECT`, `EXPLAIN` or `SHOW`; no nested write statement anywhere in the tree; no `FOR UPDATE`/`FOR SHARE` locking clause; no `SELECT INTO`; and no call to a denied function — file access, backup and WAL control, replication slots, advisory locks, `dblink`, sequence mutation, stats resets.
- **The schema allowlist is a guardrail, not a boundary.** `--query-schemas` matches the schemas qualifying *table references* in the parsed statement, case-insensitively, and it bounds both tools that carry caller-supplied SQL — `query` and `explain`, so `explain` with `analyze=true` cannot execute against a schema you excluded. A view, a set-returning function, or a `SECURITY DEFINER` function inside an allowed schema can still read outside it. Database privileges are the boundary; the allowlist just narrows the obvious path.
- **Authenticated, fail-closed, over HTTP.** Static keys are compared in constant time against every stored hash without an early exit; JWTs are validated against a JWK set with asymmetric algorithms only (no `alg=none`, no HMAC confusion) and a required `iss`, `aud` and `exp`, and the verifier holds no keys until the JWKS arrives, so it starts closed rather than open. RFC 9728 protected resource metadata advertises where to get a token. The server refuses to start on a non-loopback address with auth off.
- **Bounded.** Per-principal rate limiting, a per-call timeout, a statement timeout and lock timeout inside the transaction, a row cap on the `query` tool, a cap on a result's structured content, and a 1 MiB request body limit.
- **Nothing sensitive is logged.** A tool call logs its name, duration, outcome and the caller's user id — never arguments, SQL text, result rows or error text. Parse failures come back as a fixed phrase rather than echoing the statement, and the DSN is redacted from connection errors.

The threat model, the full enumeration of the layers, and the limitations each one does *not* cover are in [docs/SECURITY.md](docs/SECURITY.md).

## Testing

Run the test suite with the race detector and coverage:

```bash
go test -race -cover ./...
```

Integration tests need a Postgres database and are skipped when `PGMCP_TEST_DSN` is unset. To run them against a scratch Postgres with `pg_stat_statements` preloaded:

```bash
docker run -d --rm --name pg -e POSTGRES_PASSWORD=postgres -p 5544:5432 postgres:16 \
  -c shared_preload_libraries=pg_stat_statements -c pg_stat_statements.track=all
docker exec pg psql -U postgres -c "CREATE EXTENSION IF NOT EXISTS pg_stat_statements"
PGMCP_TEST_DSN="postgres://postgres:postgres@localhost:5544/postgres?sslmode=disable" go test -race -cover ./...
```

Create and view a coverage profile:

```bash
go test -covermode=count -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

Drive a running server through the official MCP conformance suite, or smoke it with the Inspector:

```bash
npx -y @modelcontextprotocol/conformance server --url http://127.0.0.1:8080/mcp \
  --expected-failures .github/conformance-expected-failures.yaml
npx @modelcontextprotocol/inspector --cli http://127.0.0.1:8080/mcp --transport http --method tools/list
```

## Contributing

Pull requests are welcome. For major changes, please open an issue first
to discuss what you would like to change.

Please make sure to update tests as appropriate.

## License

[MIT](LICENSE)
