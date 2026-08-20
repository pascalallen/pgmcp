// Package container is pgmcp's composition root. It is the only place that
// knows how the configuration loader, the logger, the Postgres adapter, the
// MCP server and the HTTP transport fit together, so main stays a thin shell
// around a signal context and an exit code.
package container

import (
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/config"
	"github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/postgres"
)

//go:generate go tool wire

// ConfigError reports a failure to resolve the runtime configuration, as
// opposed to a failure to build anything from it. It exists so main can tell
// an operator's mistake (exit 2) from a runtime failure (exit 1) without
// matching on message text.
type ConfigError struct {
	Err error
}

func (e *ConfigError) Error() string { return e.Err.Error() }

func (e *ConfigError) Unwrap() error { return e.Err }

// Container holds the fully wired application. HTTP is nil when the
// configured transport is stdio: an stdio server has no HTTP surface at all,
// and building one would start a listener nothing serves.
type Container struct {
	Config *config.Config
	Log    *slog.Logger
	Store  *postgres.Store
	MCP    *mcp.Server
	HTTP   http.Handler
}
