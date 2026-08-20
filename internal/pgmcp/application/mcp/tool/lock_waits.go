package tool

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// LockWaitsIn is the input of the lock_waits tool.
type LockWaitsIn struct {
	MinWaitMs int64 `json:"min_wait_ms,omitempty" jsonschema:"ignore waits shorter than this many milliseconds, to filter out momentary contention (default 0)"`
}

// LockWaitsOut is the output of the lock_waits tool.
type LockWaitsOut struct {
	Meta
	Edges  []diagnostics.LockEdge `json:"edges" jsonschema:"one entry per blocked session, naming the session that blocks it and the lock they contend for"`
	Cycles [][]int                `json:"cycles" jsonschema:"process id cycles in the wait graph; a non-empty cycle is a deadlock"`
}

// LockWaits reports which sessions are blocked and what blocks them.
func LockWaits(d diagnostics.Diagnostics) (*mcp.Tool, mcp.ToolHandlerFor[LockWaitsIn, LockWaitsOut]) {
	tool := &mcp.Tool{
		Name:        "lock_waits",
		Description: "Reports the current lock wait graph: which sessions are blocked, which sessions block them, the lock and relation they contend for, and any cycles that amount to a deadlock. Use it when queries hang, statements time out, or a migration will not acquire its lock. An empty edge list means nothing is currently blocked.",
		Annotations: readOnly("Lock waits"),
	}

	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in LockWaitsIn) (*mcp.CallToolResult, LockWaitsOut, error) {
		minWaitMs := in.MinWaitMs
		if minWaitMs < 0 {
			minWaitMs = 0
		}

		result, err := d.LockWaits(ctx, diagnostics.LockWaitsParams{MinWaitMs: minWaitMs})
		if err != nil {
			return nil, LockWaitsOut{}, err
		}
		if result == nil {
			result = &diagnostics.LockWaitsResult{}
		}

		out := LockWaitsOut{Meta: newMeta(ctx, d), Edges: result.Edges, Cycles: result.Cycles}
		if out.Edges == nil {
			out.Edges = []diagnostics.LockEdge{}
		}
		if out.Cycles == nil {
			out.Cycles = [][]int{}
		}

		return nil, out, nil
	}

	return tool, handler
}
