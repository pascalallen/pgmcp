# Deploying pgmcp

pgmcp serves one MCP surface over two transports. Which one you run is a
configuration choice, not a different build: `stdio` for a client that launches
the server as a subprocess, `http` (Streamable HTTP) for a shared deployment
behind TLS.

Everything below assumes you have already created the read-only role — see
[Database role](#database-role). Do that first; it is the part that actually
keeps the server read-only.

## Contents

- [Database role](#database-role)
- [Configuration](#configuration)
- [Run modes](#run-modes)
  - [stdio, under Claude Code or Claude Desktop](#stdio-under-claude-code-or-claude-desktop)
  - [http, behind a TLS terminator](#http-behind-a-tls-terminator)
  - [Docker](#docker)
  - [Docker Compose](#docker-compose)
- [Connecting clients](#connecting-clients)
- [Releasing](#releasing)
- [Operational notes](#operational-notes)

## Database role

pgmcp enforces read-only three independent ways — a `BEGIN READ ONLY`
transaction per call, a parser-level statement and function guard, and the
database role itself. The first two live in the binary; the third is yours to
create, and it is the one that still holds if the other two have a bug.

```sql
CREATE ROLE pgmcp LOGIN PASSWORD 'change-me';

-- pg_monitor grants the read access the diagnostics tools need:
-- pg_stat_statements, pg_stat_activity's full query text, pg_stat_replication,
-- and the restricted GUCs config_check reads.
GRANT pg_monitor TO pgmcp;

-- Per-database read access, for the query tool and the table/index health
-- tools. Repeat the last three statements for every schema you want visible.
GRANT CONNECT ON DATABASE app TO pgmcp;
GRANT USAGE ON SCHEMA public TO pgmcp;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO pgmcp;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO pgmcp;
```

`pg_stat_statements` must be preloaded for the `top_queries` tool to return
anything:

```sql
-- postgresql.conf: shared_preload_libraries = 'pg_stat_statements'  (needs a restart)
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

**`pg_monitor` does not include `pg_signal_backend`.** That is deliberate and
worth keeping: `pg_terminate_backend()` and `pg_cancel_backend()` are the two
functions a "read-only" diagnostics session could do real damage with, and the
role simply cannot execute them. Even if the SQL guard were bypassed, and even
inside a read-only transaction, those calls fail with a permissions error. Do
not grant `pg_signal_backend` to this role to "make lock_waits more useful".

Belt and braces, set the role's default transaction mode too:

```sql
ALTER ROLE pgmcp SET default_transaction_read_only = on;
```

## Configuration

Every setting has a `--flag` and a `PGMCP_<KEY>` environment variable. Flags win
over the environment, which wins over the default.

| Flag | Env | Default | Meaning |
| --- | --- | --- | --- |
| `--database-url` | `PGMCP_DATABASE_URL` | — (**required**) | Postgres connection string |
| `--transport` | `PGMCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `--listen` | `PGMCP_LISTEN` | `127.0.0.1:8080` | HTTP listen address |
| `--resource-url` | `PGMCP_RESOURCE_URL` | — | Public URL for OAuth resource metadata |
| `--auth-mode` | `PGMCP_AUTH_MODE` | `none` | `none`, `static`, or `jwt` (HTTP only) |
| `--api-keys` | `PGMCP_API_KEYS` | — | Comma-separated keys for `static` |
| `--jwks-url` | `PGMCP_JWKS_URL` | — | JWK set URL, required for `jwt` |
| `--jwt-issuer` | `PGMCP_JWT_ISSUER` | — | Required `iss`, required for `jwt` |
| `--jwt-audience` | `PGMCP_JWT_AUDIENCE` | — | Required `aud`, required for `jwt` |
| `--auth-servers` | `PGMCP_AUTH_SERVERS` | — | Comma-separated OAuth servers to advertise |
| `--disable-query` | `PGMCP_DISABLE_QUERY` | `false` | Drop the ad hoc `query` tool entirely |
| `--query-schemas` | `PGMCP_QUERY_SCHEMAS` | — | Schemas the `query` tool may read |
| `--max-conns` | `PGMCP_MAX_CONNS` | `4` | Maximum Postgres connections |
| `--call-timeout` | `PGMCP_CALL_TIMEOUT` | `60s` | Per-tool-call timeout |
| `--rate-limit` | `PGMCP_RATE_LIMIT` | `60` | Calls per caller per minute |
| `--max-output-bytes` | `PGMCP_MAX_OUTPUT_BYTES` | `1048576` | Cap on a tool call's response |
| `--log-level` | `PGMCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--log-format` | `PGMCP_LOG_FORMAT` | `text` | `text` or `json` |
| `--insecure-no-auth` | `PGMCP_INSECURE_NO_AUTH` | `false` | Allow `auth-mode=none` off loopback |
| `--version` | — | — | Print the version and exit |

`--auth-mode` and everything below it in the auth block apply to the HTTP
transport only. Over stdio the operating system decides who the caller is: the
parent process that launched the binary, and nobody else.

Configuration errors exit `2`; runtime failures exit `1`. Every offending key is
named in a single message, so a bad deployment tells you all of what is wrong at
once.

## Run modes

### stdio, under Claude Code or Claude Desktop

The default. The client launches the binary and talks over stdin/stdout.

```bash
claude mcp add pgmcp \
  --env PGMCP_DATABASE_URL='postgres://pgmcp:...@db.internal:5432/app?sslmode=require' \
  -- pgmcp
```

Or with the released image, so there is nothing to install:

```bash
claude mcp add pgmcp \
  -- docker run --rm -i \
     -e PGMCP_DATABASE_URL='postgres://pgmcp:...@db.internal:5432/app?sslmode=require' \
     ghcr.io/pascalallen/pgmcp:1.0.0
```

`docker run -i` is not optional here: stdio is the transport.

For Claude Desktop, the same thing in `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "pgmcp": {
      "command": "pgmcp",
      "env": {
        "PGMCP_DATABASE_URL": "postgres://pgmcp:...@db.internal:5432/app?sslmode=require"
      }
    }
  }
}
```

### http, behind a TLS terminator

pgmcp speaks plain HTTP and never terminates TLS itself. Run it on loopback and
put a reverse proxy in front.

```bash
PGMCP_DATABASE_URL='postgres://pgmcp:...@db.internal:5432/app?sslmode=require' \
PGMCP_AUTH_MODE=static \
PGMCP_API_KEYS="$(openssl rand -hex 32)" \
PGMCP_RESOURCE_URL=https://pgmcp.example.com \
  pgmcp --transport http --listen 127.0.0.1:8080
```

Caddy:

```caddyfile
pgmcp.example.com {
	reverse_proxy 127.0.0.1:8080 {
		# The Streamable HTTP transport keeps an SSE stream open; without this
		# Caddy buffers the response and the client sees nothing until the end.
		flush_interval -1
	}
}
```

nginx:

```nginx
location / {
	proxy_pass http://127.0.0.1:8080;
	proxy_http_version 1.1;
	proxy_set_header Connection "";
	proxy_buffering off;      # same reason as Caddy's flush_interval
	proxy_read_timeout 3600s; # an idle SSE stream is not a dead one
}
```

Three endpoints are exposed:

| Path | Auth | Purpose |
| --- | --- | --- |
| `POST /mcp` | bearer token | The Streamable HTTP transport |
| `GET /.well-known/oauth-protected-resource` | none | RFC 9728 metadata, only when `--auth-servers` is set |
| `GET /healthz` | none | `200 {"status":"ok"}`, `503 {"status":"degraded"}` |

`/healthz` is unauthenticated on purpose so an orchestrator can probe it, and
for the same reason it never reports *why* a check failed — the detail goes to
the log.

If you bind a non-loopback address with `--auth-mode none`, the server refuses
to start. `--insecure-no-auth` overrides that; there is no good reason to use it
outside a sealed network.

### Docker

The released image is `gcr.io/distroless/static-debian12:nonroot` with the
binary on it — no shell, no package manager, non-root by default, ~22 MB.

```bash
docker run --rm -p 8080:8080 \
  -e PGMCP_DATABASE_URL='postgres://pgmcp:...@db.internal:5432/app?sslmode=require' \
  -e PGMCP_AUTH_MODE=static \
  -e PGMCP_API_KEYS=your-key \
  ghcr.io/pascalallen/pgmcp:1.0.0 --transport http --listen 0.0.0.0:8080

curl -fsS http://127.0.0.1:8080/healthz
```

Binding `0.0.0.0` inside the container is correct — the container's network
namespace is the boundary — but it means `--auth-mode none` will be refused, as
it should be. Publish the port only to where the proxy can reach it.

Images are multi-arch (`linux/amd64`, `linux/arm64`); `:latest` follows the most
recent release, and pinning `:1.0.0` is what you want in a deployment.

### Docker Compose

The repository's `compose.yaml` builds from source and brings up Postgres with
`pg_stat_statements` preloaded — a development loop, not a deployment target:

```bash
bin/up      # build and start, follow logs
bin/down    # tear down, including volumes
```

## Connecting clients

**Claude Code, HTTP:**

```bash
claude mcp add --transport http pgmcp https://pgmcp.example.com/mcp \
  --header "Authorization: Bearer <key>"
```

**claude.ai custom connector:** point it at `https://pgmcp.example.com/mcp` and
run `--auth-mode jwt`, with `--jwks-url`, `--jwt-issuer` and `--jwt-audience`
matching your identity provider and `--auth-servers` set so the 401 challenge
advertises where to get a token.

`--resource-url` must be a **bare origin** — `https://pgmcp.example.com`, with
no path. The protected resource metadata URL is composed as
`<resource-url>/.well-known/oauth-protected-resource`, so a path-bearing value
like `https://pgmcp.example.com/mcp` produces
`https://pgmcp.example.com/mcp/.well-known/oauth-protected-resource`, which is
not where RFC 9728 says a client should look. Known limitation; use the origin.

## Releasing

Tagging is the whole release process. `.github/workflows/release.yml` runs on
`v*` tags and does three things: goreleaser cuts the GitHub release with six
binaries and their checksums, pushes the multi-arch image to
`ghcr.io/pascalallen/pgmcp`, and then `mcp-publisher` publishes `server.json` to
the MCP Registry.

```bash
git tag -a v1.0.0 -m "v1.0.0" && git push origin v1.0.0
```

goreleaser's `{{ .Version }}` is the tag with the leading `v` stripped, so
`v1.0.0` produces `ghcr.io/pascalallen/pgmcp:1.0.0`, and the publish job rewrites
`server.json`'s `version` and package `identifier` to match before publishing.
The three must agree; nothing else keeps them in step.

**One-time setup, before the first tag:** a package pushed to GHCR is private by
default, and the MCP Registry verifies OCI ownership by pulling the image and
reading its `io.modelcontextprotocol.server.name` label. Push the image once,
then set the package to public in
`https://github.com/users/pascalallen/packages/container/pgmcp/settings`. Until
you do, `publish-registry` fails at the ownership check.

Registry authentication is GitHub OIDC (`mcp-publisher login github-oidc`),
which the registry supports for `io.github.*` namespaces. It mints a token from
the workflow's own identity, so there is no publishing secret in this repo.

**Manual fallback**, if the job fails and you want to publish by hand:

```bash
brew install mcp-publisher                  # or download from the registry releases
VERSION=1.0.0
jq --arg v "$VERSION" '.version = $v | .packages[0].identifier = "ghcr.io/pascalallen/pgmcp:" + $v' \
  server.json > server.json.tmp && mv server.json.tmp server.json
mcp-publisher validate server.json
mcp-publisher login github                  # browser OAuth
mcp-publisher publish
```

Do not commit the rewritten `server.json` — the committed copy stays at the
version of the last release, and CI templates it per tag.

## Operational notes

- **The anonymous rate-limit bucket is shared.** Throttling is per principal —
  the authenticated user id — and every unauthenticated caller falls into one
  bucket named `anonymous`. With `--auth-mode none` that means the whole
  deployment shares a single `--rate-limit` allowance and one noisy client
  starves the rest. It is another reason `none` is for loopback only.
- **`pg_catalog` is not implicitly allowed.** `--query-schemas` is an exact
  allowlist: set it to `public` and `SELECT * FROM pg_catalog.pg_class` is
  refused. If you want catalogue introspection through the `query` tool, name it:
  `--query-schemas=public,pg_catalog`. Leaving `--query-schemas` unset disables
  the allowlist entirely rather than allowing nothing.
- **The allowlist is matched case-insensitively.** Postgres identifier rules are
  case-sensitive once quoted, so `public` and `"Public"` are genuinely different
  schemas — but an allowlist entry of `public` admits a reference to `"Public"`.
  If you run schemas that differ only by case, treat them as one entry.
- **With an allowlist set, every table reference must be schema-qualified**,
  including references to a CTE. An unqualified name is refused rather than
  resolved against `search_path`, because resolving it would depend on server
  state the guard cannot see.
- **`query` can be switched off entirely** with `--disable-query`. The eight
  diagnostics tools carry no free-form SQL, so a deployment that only needs
  diagnostics can drop the one tool that does.
- **Nothing logs SQL, tool arguments, or result rows** — a tool call logs its
  name, duration, outcome and the caller's user id, and that is all. The startup
  line deliberately omits the database, because the DSN carries a password and
  even its host tells a log aggregator more than it needs.
- **The HTTP transport is stateless**: no sessions, no event store, so a
  deployment can be scaled horizontally behind a plain round-robin. A
  consequence is that pgmcp issues no `Mcp-Session-Id`, and clients that want to
  resume a stream cannot.
