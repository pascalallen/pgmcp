package tool

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
)

// queryToolName is the one tool an operator can switch off, so the catalogue
// can leave it out by name.
const queryToolName = "query"

// catalogNames are the tools Register adds, in the order it adds them: the
// question a session usually starts with first, the open-ended escape hatch
// last. The list is fixed rather than derived, so Names answers the same way
// on every call — the SDK sorts its own tools/list response by name, so this
// order is the catalogue's, not the wire's.
var catalogNames = []string{
	"top_queries",
	"explain",
	"index_health",
	"table_health",
	"lock_waits",
	"connections",
	"replication",
	"config_check",
	queryToolName,
}

// Options are the operator's choices about which tools exist and what the ad
// hoc query tool may read.
type Options struct {
	DisableQuery bool     // leave the ad hoc query tool unregistered entirely
	QuerySchemas []string // when set, the query and explain tools may only read tables in these schemas
}

// Deps are the collaborators every tool in the catalogue is built from.
type Deps struct {
	Diag   diagnostics.Diagnostics
	Parser sqlguard.Parser
	Log    *slog.Logger
}

// Register adds the whole tool catalogue to s, in the fixed order of
// catalogNames and skipping the query tool when the operator disabled it.
func Register(s *mcp.Server, deps Deps, opts Options) {
	topQueries, topQueriesHandler := TopQueries(deps.Diag)
	mcp.AddTool(s, topQueries, topQueriesHandler)

	explain, explainHandler := Explain(deps.Diag, deps.Parser, opts.QuerySchemas)
	mcp.AddTool(s, explain, explainHandler)

	indexHealth, indexHealthHandler := IndexHealth(deps.Diag)
	mcp.AddTool(s, indexHealth, indexHealthHandler)

	tableHealth, tableHealthHandler := TableHealth(deps.Diag)
	mcp.AddTool(s, tableHealth, tableHealthHandler)

	lockWaits, lockWaitsHandler := LockWaits(deps.Diag)
	mcp.AddTool(s, lockWaits, lockWaitsHandler)

	connections, connectionsHandler := Connections(deps.Diag)
	mcp.AddTool(s, connections, connectionsHandler)

	replication, replicationHandler := Replication(deps.Diag)
	mcp.AddTool(s, replication, replicationHandler)

	configCheck, configCheckHandler := ConfigCheck(deps.Diag)
	mcp.AddTool(s, configCheck, configCheckHandler)

	if !opts.DisableQuery {
		query, queryHandler := Query(deps.Diag, deps.Parser, opts.QuerySchemas)
		mcp.AddTool(s, query, queryHandler)
	}

	if deps.Log != nil {
		deps.Log.Debug("registered mcp tools", "tools", Names(opts))
	}
}

// Names lists the tools Register adds under opts, in registration order.
func Names(opts Options) []string {
	names := make([]string, 0, len(catalogNames))
	for _, name := range catalogNames {
		if opts.DisableQuery && name == queryToolName {
			continue
		}
		names = append(names, name)
	}

	return names
}
