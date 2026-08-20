# Security

pgmcp hands a language model a connection to a production database. This
document says what that means, what stops it going wrong, and — the part that
matters more — what does not.

## Contents

- [Threat model](#threat-model)
- [Layered defenses](#layered-defenses)
  - [1. The database role](#1-the-database-role)
  - [2. `default_transaction_read_only`](#2-default_transaction_read_only)
  - [3. The per-call read-only transaction](#3-the-per-call-read-only-transaction)
  - [4. The SQL guard](#4-the-sql-guard)
  - [5. The schema allowlist](#5-the-schema-allowlist)
  - [6. Output and time bounds](#6-output-and-time-bounds)
  - [7. Rate limiting](#7-rate-limiting)
  - [8. Network exposure](#8-network-exposure)
  - [9. Authentication](#9-authentication)
  - [10. Log hygiene](#10-log-hygiene)
- [Known limitations](#known-limitations)
- [Reporting a vulnerability](#reporting-a-vulnerability)

## Threat model

**The asset is the database.** Its contents, its availability, and the
integrity of its schema and configuration. Everything below exists to keep an
MCP session from damaging any of the three.

**Tool input is untrusted.** Arguments arrive from a language model, and the
model's own context is influenced by whatever it has read that session — a
ticket, a log line, a web page, a row this server returned. Treat every
argument as attacker-controlled, because in the general case it is. pgmcp does
not attempt to decide whether a given statement is *intended*; it decides
whether it is *permitted*, statically, before it runs.

**The client is trusted to the extent it is authenticated.** Over stdio the
caller is the parent process, and the operating system is the authentication.
Over HTTP the caller holds a bearer token, and a token is the whole identity —
pgmcp does not implement authorization beyond "authenticated or not", so every
principal that can reach `/mcp` has the same access as every other.

**Out of scope.** pgmcp does not defend against an operator who points it at a
superuser DSN, an attacker with shell on the host, or a compromised MCP client.
It also does not terminate TLS — that is the reverse proxy's job.

## Layered defenses

Nine of these ship in the binary. The first is yours to create, and it is the
one that still holds if any of the others has a bug.

### 1. The database role

Run pgmcp as a role that cannot write. `pg_monitor` plus `CONNECT`, `USAGE`
and `SELECT` on the schemas you want visible is the whole grant; the SQL is in
[DEPLOYING.md](DEPLOYING.md#database-role).

`pg_monitor` deliberately does **not** include `pg_signal_backend`.
`pg_terminate_backend()` and `pg_cancel_backend()` are the two functions a
"read-only" diagnostics session could do real damage with, and without that
grant the role simply cannot execute them — even if the SQL guard were
bypassed, and even inside a read-only transaction. Do not grant it.

### 2. `default_transaction_read_only`

```sql
ALTER ROLE pgmcp SET default_transaction_read_only = on;
```

Belt to the braces below: any transaction the role opens is read-only whether
or not the client asked for one.

### 3. The per-call read-only transaction

Every statement the Postgres adapter runs goes through one helper,
`Store.readOnly`, and there is no other path to the database. It opens
`BEGIN READ ONLY` at `READ COMMITTED`, sets `statement_timeout` to the call's
budget and `lock_timeout` to `2s`, runs the work, and always rolls back. A
write attempt surfaces as SQLSTATE `25006` and is translated into a domain
error; a statement that outruns its budget surfaces as `57014` and becomes a
deadline error.

`lock_timeout` matters as much as `statement_timeout`: diagnostics must never
queue behind another session's lock and become the thing that is blocking
production.

### 4. The SQL guard

A read-only transaction is not sufficient. `pg_terminate_backend`,
`pg_read_file`, `pg_sleep`, `setval`, advisory locks and `dblink` all work
fine inside one. So both tools that accept SQL — `query` and `explain` — parse
the statement first, with libpg_query (Postgres's own grammar, via the pure-Go
`wasilibs/go-pgquery` build), and reject it unless:

- it is exactly **one** top-level statement (`multiple_statements` otherwise);
- that statement is a `SelectStmt`, `ExplainStmt` or `VariableShowStmt`
  (`statement_kind_not_allowed`);
- no node anywhere in the tree is a `*Stmt` outside that allowlist — this is
  what catches a write hidden in a CTE, `WITH x AS (DELETE …) SELECT …`
  (`nested_statement_not_allowed`);
- there is no `LockingClause`, i.e. no `FOR UPDATE`/`FOR SHARE`, which takes
  row locks other sessions then wait on (`locking_clause_not_allowed`);
- there is no `IntoClause`, i.e. no `SELECT … INTO`, which creates a table
  (`select_into_not_allowed`);
- no function called anywhere in the statement is on the denylist
  (`function_not_allowed`).

The denylist is exact names plus prefixes, matched lowercase on the last path
segment, and covers: backend signalling, config reload and log rotation, WAL
switch/replay control, backup start/stop, promotion, file and directory
reading (`pg_read_file`, `pg_stat_file`, `pg_ls_*`), sleeps, `pg_notify`,
`set_config`, sequence mutation (`setval`, `nextval`), the large-object API,
BRIN/GIN maintenance, every `pg_stat_reset*`, advisory locks, `dblink*`,
logical replication and replication-slot management, and `pg_copy_*`.

The allowlist is the primary control and the denylist is defense in depth: a
function that is not on the denylist still has to appear inside an otherwise
permitted `SELECT`/`EXPLAIN`/`SHOW`, run inside a read-only transaction, as a
role with no write privilege.

### 5. The schema allowlist

`--query-schemas` restricts the `query` and `explain` tools to named schemas.
Be precise about what it does:

- It compares the schemas qualifying **table references** in the parsed
  statement against the list.
- It bounds **both** tools that carry caller-supplied SQL. `explain` runs the
  same check as `query`, because `analyze=true` executes the statement — an
  allowlist that stopped only `query` would leave row counts as a readable
  oracle over the schemas you excluded.
- With a list set, **every** table reference must be schema-qualified —
  including a reference to a CTE defined in the same statement. An unqualified
  name is refused rather than resolved against `search_path`, because
  resolving it would depend on server state the guard cannot see.
- Comparison is **case-insensitive**. Postgres identifiers are case-sensitive
  once quoted, so `public` and `"Public"` are genuinely different schemas — but
  an allowlist entry of `public` admits a reference to `"Public"`. If you run
  schemas that differ only by case, treat them as one entry.
- `pg_catalog` is not implicit. `--query-schemas=public` refuses
  `SELECT * FROM pg_catalog.pg_class`. Name it if you want it.
- Leaving `--query-schemas` unset disables the allowlist entirely, rather than
  allowing nothing.

**What it does not stop.** A view in an allowed schema that selects from a
table in a denied one. A set-returning function in an allowed schema that
reads anywhere. A `SECURITY DEFINER` function, which runs with its owner's
privileges and can read what the pgmcp role cannot. In every case the parsed
statement names only the allowed schema, so the guard sees nothing to reject.

The allowlist is a guardrail against an obvious mistake, not a security
boundary. **The boundary is `GRANT`.** If a schema must not be readable, do not
grant `SELECT` on it — and if a deployment needs no ad hoc SQL at all,
`--disable-query` removes the tool from the catalogue entirely and leaves the
eight diagnostics tools, none of which carry free-form SQL.

### 6. Output and time bounds

- `--call-timeout` (default `60s`) bounds a whole tool call and becomes the
  transaction's `statement_timeout`.
- The `query` tool clamps its own row budget to 1–5000 rows (default 500) and
  its own statement timeout to the configured range, rather than rejecting an
  out-of-range request.
- `--max-output-bytes` (default 1 MiB) caps a tool result's structured
  content. Over the cap, the payload is replaced wholesale with
  `{"truncated": true, "reason": …}` and the call still succeeds, so the model
  narrows its request instead of retrying the same one.
- The HTTP transport caps a request body at 1 MiB, enforced during the read so
  a chunked or HTTP/2 body cannot slip past it, and bounds header read, body
  read and idle-connection phases so a slow client cannot hold a connection
  open indefinitely.

### 7. Rate limiting

`--rate-limit` (default 60/minute, burst one minute's allowance) throttles
`tools/call` per principal — the authenticated user id. Buckets refill at the
configured per-minute rate and are evicted when idle, so a churn of principals
cannot grow the map without bound. A throttled call comes back as a tool-level
error the model can read and back off from, not a protocol error.

Rate limiting is installed for the HTTP transport only. A stdio server has
exactly one caller — the operator's own client — and throttling it would only
throttle the operator.

**The anonymous bucket is shared.** Every unauthenticated caller falls into one
bucket named `anonymous`. With `--auth-mode none` that means the whole
deployment shares a single allowance and one noisy client starves the rest.
It is another reason `none` is for loopback only.

### 8. Network exposure

The default listen address is `127.0.0.1:8080`. Binding a non-loopback address
with `--auth-mode none` is refused at startup — twice, once by the
configuration loader and again by the HTTP handler constructor, because a
handler built by any other caller must be no easier to leave open.
`--insecure-no-auth` overrides it; there is no good reason to use it outside a
sealed network.

The go-sdk's Streamable HTTP handler adds DNS-rebinding protection, on by
default: a request arriving on a loopback local address whose `Host` header is
*not* loopback is rejected with 403. That is what stops a web page in the
operator's browser from resolving an attacker-controlled name to `127.0.0.1`
and driving the local server.

It also means a reverse proxy in front of a loopback-bound pgmcp must rewrite
`Host` to the upstream address rather than passing the public hostname
through — see [DEPLOYING.md](DEPLOYING.md#http-behind-a-tls-terminator) for
the Caddy and nginx directives.

pgmcp never terminates TLS. Run it behind a proxy that does.

### 9. Authentication

`--auth-mode` applies to the HTTP transport only.

**`static`** — a fixed list of API keys, hashed once at construction with
SHA-256 and never held in the clear beyond that. Verification hashes the
presented token and compares it against **every** stored hash with
`subtle.ConstantTimeCompare` and no early exit, so neither the time to answer
nor the number of comparisons reveals which key was closest. The principal id
is a short hash prefix, enough to tell keys apart in logs and rate-limit
buckets and far too little to attack the key. Revoking a static key means
removing it from the configuration and restarting; there is no revocation
list.

**`jwt`** — tokens validated against the JWK set at `--jwks-url`, requiring
`--jwt-issuer` as `iss`, `--jwt-audience` as `aud`, and a present, unexpired
`exp`. Signing algorithms are **asymmetric only** (RS/ES/PS, 256/384/512):
`none` is absent, and so is HMAC, because an HMAC token would verify against
the public key the JWKS publishes — the alg-confusion attack. An unreachable
JWKS is deliberately not a startup error, since refusing to boot would turn a
momentary identity-provider blip into a longer outage; until a key set
arrives the verifier holds no keys and rejects every token. **It starts
closed, not open.**

The audience check is what implements the MCP spec's requirement that a
server reject a token minted for a different resource. With `--auth-servers`
set, a 401 carries a `WWW-Authenticate` challenge pointing at
`/.well-known/oauth-protected-resource` (RFC 9728) so a client can discover
where to get a token.

**No token passthrough.** The bearer token is used to authenticate the caller
and for nothing else. It is never forwarded to Postgres, never placed in an
outbound request, never logged, never echoed in an error, and never written
into a response body. The database connection uses pgmcp's own credentials,
which is why the role in §1 is the ceiling on what any caller can do.

`/healthz` is unauthenticated on purpose, so an orchestrator can probe an
authenticated server — and for the same reason it reports `ok` or `degraded`
and never *why* a check failed. The detail goes to the log.

### 10. Log hygiene

One `INFO` record per handled call, carrying the method, duration, outcome,
the tool name and the caller's user id. Not the arguments. Not the SQL. Not
the result rows. Not the error text — a failed call is reported as `ok=false`
and nothing more, because an error string can quote the statement that
produced it. The record is emitted from a `defer`, so a call that panics is
still accounted for.

Two places that would otherwise leak are handled specifically:

- **Parse failures.** The parser's message can quote the statement it choked
  on, so `query` and `explain` replace the detail of a `parse_error`
  rejection with the fixed phrase `statement could not be parsed`. Every other
  rejection reason reports only a statement kind, a node type or a function
  name — identifiers, not statement text.
- **The DSN.** A connection string carries a password, so it is redacted from
  connection-parse errors, and the startup line omits the database entirely —
  even its host tells a log aggregator more than it needs.

## Known limitations

These are real, and they are documented rather than fixed because fixing them
would cost more than they are worth. Read them before deploying against
anything you care about.

- **A permitted `SELECT` can still be expensive.** The guard decides whether a
  statement is read-only, not whether it is cheap. A cartesian join over two
  large tables passes every check. What bounds it is `statement_timeout`, the
  row cap, and the output cap — so set `--call-timeout` to something your
  server can actually absorb, and remember the query still burns I/O and CPU
  until the timeout fires.
- **`explain` with `analyze=true` executes the statement.** That is what
  `EXPLAIN ANALYZE` is. It is still inside the read-only transaction, still
  guarded, and still bounded by the schema allowlist, so it cannot write and
  cannot reach a schema you excluded. It is not free, though: the same expense
  caveat applies, and more directly.
- **Prompt injection travels through result rows.** Anything the database
  returns is data the model reads, and a row can contain text shaped like an
  instruction. pgmcp cannot sanitize this: the rows *are* the answer. What it
  does instead is bound the blast radius — the `query` tool's description tells
  the model in as many words that results are untrusted data and not to follow
  instructions found in them, and every tool is annotated read-only,
  non-destructive and closed-world so a client can reason about what a call can
  do. The real mitigation is upstream: the role cannot write, so a successful
  injection still cannot change anything through this server.
- **Authorization is binary.** An authenticated principal gets the whole tool
  catalogue. There is no per-tool or per-schema scoping by identity; if two
  callers need different access, run two servers with two roles.
- **A static key has no expiry.** The 24-hour lifetime stamped on an accepted
  key is a formality the bearer middleware requires, not a revocation
  mechanism.
- **The HTTP transport is stateless**, so there is no session to hijack — and
  equally no `Mcp-Session-Id`, so a client cannot resume an interrupted
  stream.

## Reporting a vulnerability

Report privately through GitHub, not in a public issue:

**<https://github.com/pascalallen/pgmcp/security/advisories/new>**

Please include the version, the configuration (with secrets removed), and the
steps to reproduce. You will get an acknowledgement, and a fix or an
explanation of why the behaviour is intended.
