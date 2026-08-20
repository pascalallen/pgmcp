package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// Defaults the tool applies when the caller leaves a field unset.
const (
	defaultConnectionsGroupBy        = diagnostics.GroupBy("state")
	defaultConnectionsIdleInTxMinSec = 60
)

// connectionsGroupings are the dimensions the tool can group backends by, in
// the order the description advertises them.
var connectionsGroupings = []diagnostics.GroupBy{"state", "wait_event", "application", "user", "database"}

// ConnectionsIn is the input of the connections tool.
type ConnectionsIn struct {
	GroupBy        string `json:"group_by,omitempty" jsonschema:"dimension to group client backends by: state, wait_event, application, user or database (default state)"`
	IdleInTxMinSec int    `json:"idle_in_tx_min_s,omitempty" jsonschema:"only list sessions that have been idle inside a transaction for at least this many seconds (default 60)"`
}

// ConnectionsOut is the output of the connections tool.
type ConnectionsOut struct {
	Meta
	Groups            []diagnostics.ConnGroup `json:"groups" jsonschema:"backend counts per key of the chosen grouping, busiest first"`
	Total             int                     `json:"total" jsonschema:"client backends currently connected"`
	MaxConnections    int                     `json:"max_connections" jsonschema:"the max_connections ceiling those backends compete for"`
	UsedPct           float64                 `json:"used_pct" jsonschema:"percentage of the connection ceiling in use"`
	IdleInTransaction []diagnostics.IdleInTx  `json:"idle_in_transaction" jsonschema:"sessions holding a transaction open while idle, oldest first"`
}

// Connections reports how client backends are distributed and how much of the
// connection ceiling they use.
func Connections(d diagnostics.Diagnostics) (*mcp.Tool, mcp.ToolHandlerFor[ConnectionsIn, ConnectionsOut]) {
	tool := &mcp.Tool{
		Name:        "connections",
		Description: "Summarises client backends grouped by state, wait event, application, user or database, next to the max_connections ceiling and how much of it is used. Use it for connection exhaustion, pool sizing, or working out what the server is waiting on right now. It also lists sessions sitting idle inside a transaction, a common cause of blocked vacuum and stuck locks.",
		Annotations: readOnly("Connections"),
	}

	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in ConnectionsIn) (*mcp.CallToolResult, ConnectionsOut, error) {
		groupBy, err := connectionsGroupBy(in.GroupBy)
		if err != nil {
			return nil, ConnectionsOut{}, err
		}

		idleInTxMinSec := in.IdleInTxMinSec
		if idleInTxMinSec <= 0 {
			idleInTxMinSec = defaultConnectionsIdleInTxMinSec
		}

		result, err := d.Connections(ctx, diagnostics.ConnectionsParams{GroupBy: groupBy, IdleInTxMinSec: idleInTxMinSec})
		if err != nil {
			return nil, ConnectionsOut{}, err
		}
		if result == nil {
			result = &diagnostics.ConnectionsResult{}
		}

		out := ConnectionsOut{
			Meta:              newMeta(ctx, d),
			Groups:            result.Groups,
			Total:             result.Total,
			MaxConnections:    result.MaxConnections,
			UsedPct:           result.UsedPct,
			IdleInTransaction: result.IdleInTransaction,
		}
		if out.Groups == nil {
			out.Groups = []diagnostics.ConnGroup{}
		}
		if out.IdleInTransaction == nil {
			out.IdleInTransaction = []diagnostics.IdleInTx{}
		}

		return nil, out, nil
	}

	return tool, handler
}

// connectionsGroupBy resolves the requested grouping, defaulting when it is
// absent and rejecting anything outside the set so the model is told what it
// may ask for instead of silently getting a different grouping.
func connectionsGroupBy(requested string) (diagnostics.GroupBy, error) {
	if requested == "" {
		return defaultConnectionsGroupBy, nil
	}

	for _, grouping := range connectionsGroupings {
		if diagnostics.GroupBy(requested) == grouping {
			return grouping, nil
		}
	}

	allowed := make([]string, 0, len(connectionsGroupings))
	for _, grouping := range connectionsGroupings {
		allowed = append(allowed, string(grouping))
	}

	return "", fmt.Errorf("connections: unsupported group_by %q; expected one of %s", requested, strings.Join(allowed, ", "))
}
