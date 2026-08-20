# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
  cutting all of it from a `v*` tag (#14).
- `server.json`, the MCP Registry manifest, published from the release workflow
  with `mcp-publisher` over GitHub OIDC (#14).
- `docs/DEPLOYING.md`: run modes, the full flag/env table, the read-only role
  setup SQL, client wiring, and the operational notes an operator needs (#14).
