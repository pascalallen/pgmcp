// Package mcpserver assembles the pgmcp MCP server: it wires the diagnostics
// port and the SQL guard parser into the tool catalogue, the resources and the
// prompts, and installs the receiving middleware stack every call passes
// through. It is the one place that knows what a complete pgmcp server is.
package mcpserver

import (
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/middleware"
	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/prompt"
	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/resource"
	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/tool"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
)

const (
	// serverName is the programmatic identity clients match on.
	serverName = "pgmcp"
	// serverTitle is the human-readable identity clients display.
	serverTitle = "pgmcp — Postgres ops/DBA diagnostics"
	// websiteURL points a curious client at the project.
	websiteURL = "https://github.com/pascalallen/pgmcp"
)

// instructions is the standing brief handed to every connecting client. It
// says what the server is (read-only), where to start (the overview resource),
// and what the one open-ended tool will and will not do.
const instructions = `pgmcp exposes a PostgreSQL server for read-only diagnosis: every tool and resource runs inside a READ ONLY transaction with a statement timeout, so nothing here can change data, schema, or server configuration.
Start with the pgmcp://overview resource for a snapshot of the server, read pgmcp://settings for its configuration, then reach for the diagnostic tools — top_queries, explain, index_health, table_health, lock_waits, connections, replication and config_check — to investigate what the snapshot points at.
The query tool is a bounded escape hatch for ad hoc SELECTs: it is row- and time-limited, rejects any statement that is not read-only, may be restricted to specific schemas, and may be switched off entirely by the operator.`

// Params are everything New needs to build a fully wired server.
type Params struct {
	Version         string                  // build version reported to clients
	Diag            diagnostics.Diagnostics // the read-only diagnostics port
	Parser          sqlguard.Parser         // the SQL guard's parser
	Log             *slog.Logger            // structured logger, may be nil
	CallTimeout     time.Duration           // per-call ceiling
	RateLimitPerMin int                     // per-principal call allowance
	MaxOutputBytes  int                     // ceiling on a tool result's structured content
	Tools           tool.Options            // operator choices about the catalogue
	HTTP            bool                    // true when serving over HTTP rather than stdio
}

// New builds the pgmcp MCP server: identity, instructions, the receiving
// middleware stack, and the full tool/resource/prompt surface.
//
// Middleware is listed outermost first — the SDK applies it right to left, so
// Recover wraps Logging wraps Timeout wraps OutputCap (wraps RateLimit). That
// order is deliberate: a panic anywhere below is caught, every call including
// a rejected one is recorded, and the cap applies to whatever result survives.
// Rate limiting is installed only for the HTTP transport, where callers are
// remote and plural; a stdio server has exactly one caller — the parent
// process that launched it — and throttling it would only throttle its own
// operator.
func New(p Params) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:       serverName,
		Title:      serverTitle,
		Version:    p.Version,
		WebsiteURL: websiteURL,
	}, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       p.Log,
	})

	stack := []mcp.Middleware{
		middleware.Recover(p.Log),
		middleware.Logging(p.Log),
		middleware.Timeout(p.CallTimeout),
		middleware.OutputCap(p.MaxOutputBytes),
	}
	if p.HTTP {
		stack = append(stack, middleware.RateLimit(p.RateLimitPerMin, rateLimitBurst(p.RateLimitPerMin)))
	}
	s.AddReceivingMiddleware(stack...)

	tool.Register(s, tool.Deps{Diag: p.Diag, Parser: p.Parser, Log: p.Log}, p.Tools)
	resource.Register(s, p.Diag)
	prompt.Register(s)

	return s
}

// rateLimitBurst sizes the token bucket at a full minute's allowance. A model
// working a diagnosis fires several tool calls back to back and then thinks;
// letting it spend the minute's budget in one burst matches that shape, while
// the per-minute refill still bounds sustained throughput.
func rateLimitBurst(perMinute int) int {
	if perMinute < 1 {
		return 1
	}

	return perMinute
}
