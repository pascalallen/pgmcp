# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.1.0] - 2026-08-20

### Added

- A Claude Desktop bundle, `pgmcp_<version>.mcpb`, on every release: one file
  for macOS (universal binary) and Windows (x64) that installs with a double
  click and keeps the connection string in the OS keychain. `server.json` now
  declares it as an MCPB package next to the OCI image, and the publish job
  hashes the published asset into `fileSha256` (#42).

## [1.0.0] - 2026-08-20

### Added

- Repository scaffold, the flag/environment configuration loader with its
  aggregated validation, the slog logger, Docker Compose and the CI workflow
  (#1).
- The `diagnostics` domain: the read-only port every tool is written against,
  and the result types the tool output schemas are derived from (#2).
- `sqlguard`: the parser-neutral read-only statement rules — one top-level
  `SELECT`/`EXPLAIN`/`SHOW`, no nested write statement, no locking clause, no
  `SELECT INTO`, and a denylist of functions that mutate state from inside a
  read-only transaction (#3).
- Plan analysis, lock-cycle detection and the `pg_settings` tuning heuristics,
  as pure domain logic (#4).
- The Postgres store: the `readOnly` helper that runs every statement in a
  `READ ONLY` transaction with a statement and lock timeout, the libpg_query
  parser adapter, and the server info, connections, replication, lock waits,
  settings and overview queries — with the DSN redacted from connection errors
  (#5).
- `pg_stat_statements` and `EXPLAIN` adapters, including graceful degradation
  when the extension is absent (#6).
- Index health, table health and the bounded ad hoc query adapters (#7).
- MCP receiving middleware: recover, logging, per-call timeout, per-principal
  rate limiting and the output cap (#8).
- The `top_queries`, `explain`, `lock_waits`, `connections` and `replication`
  tools, with parse rejections sanitized so no statement text is echoed back
  (#9).
- The `index_health`, `table_health`, `config_check` and `query` tools, the
  `query` schema allowlist, and the tool catalogue with its `--disable-query`
  option (#10).
- The `pgmcp://overview` and `pgmcp://settings` resources and the
  `diagnose_slow_query` prompt (#11).
- MCP server construction, the Streamable HTTP transport behind bearer auth
  (static API keys or JWTs validated against a JWK set), RFC 9728 protected
  resource metadata, and an unauthenticated `/healthz` probe (#12).
- Wire container and `cmd/pgmcp`: configuration loading, dependency graph, and
  both transports end to end, with a bounded graceful shutdown (#13).
- Conformance job in CI, driving the server through the official MCP
  conformance suite over Streamable HTTP against a baseline of expected
  failures (#14).
- Release tooling: `.goreleaser.yaml` producing six binaries, archives and
  checksums; a multi-arch `ghcr.io/pascalallen/pgmcp` image built from
  `Dockerfile.goreleaser` on distroless; and `.github/workflows/release.yml`
  cutting all of it from a `v*` tag, with `mcp-publisher` pinned and
  checksum-verified (#14).
- `server.json`, the MCP Registry manifest, published from the release workflow
  with `mcp-publisher` over GitHub OIDC (#14).
- `docs/DEPLOYING.md`: run modes, the full flag/env table, the read-only role
  setup SQL, client wiring, and the operational notes an operator needs (#14).
- `README.md`, `docs/SECURITY.md` and the AGENTS.md invariant ledger: the tool
  catalogue, the configuration table, the threat model and the layered defenses
  with their limitations, and every invariant named next to the test that pins
  it (#15).
