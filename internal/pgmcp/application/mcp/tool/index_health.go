package tool

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// IndexHealthIn is the input of the index_health tool.
type IndexHealthIn struct {
	Schema       string `json:"schema,omitempty" jsonschema:"restrict the report to one schema (default: every non-system schema)"`
	IncludeBloat *bool  `json:"include_bloat,omitempty" jsonschema:"estimate index bloat, which reads the catalogue more heavily (default true)"`
}

// IndexHealthOut is the output of the index_health tool.
type IndexHealthOut struct {
	Meta
	Unused    []diagnostics.IndexFinding `json:"unused" jsonschema:"indexes the planner has never scanned, largest first"`
	Duplicate []diagnostics.IndexFinding `json:"duplicate" jsonschema:"indexes whose leading columns are already covered by the index named in duplicate_of"`
	Invalid   []diagnostics.IndexFinding `json:"invalid" jsonschema:"indexes left invalid by a failed CREATE INDEX CONCURRENTLY; the planner ignores them"`
	Bloated   []diagnostics.IndexFinding `json:"bloated" jsonschema:"indexes whose estimated wasted space is worth a REINDEX; empty when include_bloat is false"`
}

// IndexHealth reports indexes that are unused, duplicated, invalid or bloated.
func IndexHealth(d diagnostics.Diagnostics) (*mcp.Tool, mcp.ToolHandlerFor[IndexHealthIn, IndexHealthOut]) {
	tool := &mcp.Tool{
		Name:        "index_health",
		Description: "Reports the indexes worth acting on: never scanned, duplicated by another index, left invalid by a failed CREATE INDEX CONCURRENTLY, or estimated to be bloated. Use it when asked which indexes can be dropped, why writes are slow, or where disk space has gone. Each finding may carry a drop_candidate_sql string for a human to review — this server only ever reads and never runs it.",
		Annotations: readOnly("Index health"),
	}

	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in IndexHealthIn) (*mcp.CallToolResult, IndexHealthOut, error) {
		includeBloat := true
		if in.IncludeBloat != nil {
			includeBloat = *in.IncludeBloat
		}

		result, err := d.IndexHealth(ctx, diagnostics.IndexHealthParams{Schema: in.Schema, IncludeBloat: includeBloat})
		if err != nil {
			return nil, IndexHealthOut{}, err
		}
		if result == nil {
			result = &diagnostics.IndexHealthResult{}
		}

		out := IndexHealthOut{
			Meta:      newMeta(ctx, d),
			Unused:    findings(result.Unused),
			Duplicate: findings(result.Duplicate),
			Invalid:   findings(result.Invalid),
			Bloated:   findings(result.Bloated),
		}

		return nil, out, nil
	}

	return tool, handler
}

// findings replaces a nil group of index findings with an empty one, so no
// slice in the output is null.
func findings(group []diagnostics.IndexFinding) []diagnostics.IndexFinding {
	if group == nil {
		return []diagnostics.IndexFinding{}
	}

	return group
}
