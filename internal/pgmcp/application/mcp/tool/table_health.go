package tool

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// Bounds on the size floor the tool passes to the port. The cap is a
// terabyte, far above any useful floor, and exists because the adapter turns
// the value into bytes: an unbounded value would overflow that multiplication
// and turn the floor negative.
const (
	defaultTableHealthMinSizeMB = 1
	maxTableHealthMinSizeMB     = 1 << 20
)

// TableHealthIn is the input of the table_health tool.
type TableHealthIn struct {
	Schema    string `json:"schema,omitempty" jsonschema:"restrict the report to one schema (default: every non-system schema)"`
	MinSizeMB int64  `json:"min_size_mb,omitempty" jsonschema:"ignore tables smaller than this many megabytes, 0 to 1048576 (default 1)"`
}

// TableHealthOut is the output of the table_health tool.
type TableHealthOut struct {
	Meta
	Tables []diagnostics.TableFinding `json:"tables" jsonschema:"one entry per table above the size floor, worst first, each flagged with the conditions it meets"`
}

// TableHealth reports per-table vacuum, bloat and scan health.
func TableHealth(d diagnostics.Diagnostics) (*mcp.Tool, mcp.ToolHandlerFor[TableHealthIn, TableHealthOut]) {
	tool := &mcp.Tool{
		Name:        "table_health",
		Description: "Reports per-table vacuum and scan health for tables above a size floor: dead tuple ratio, when each was last vacuumed and analyzed, sequential versus index scans, and estimated bloat. Each table carries flags such as high_dead_ratio, never_vacuumed, never_analyzed, seq_scan_heavy or bloated. Use it for questions about table bloat, autovacuum falling behind, or a table that keeps being scanned sequentially.",
		Annotations: readOnly("Table health"),
	}

	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in TableHealthIn) (*mcp.CallToolResult, TableHealthOut, error) {
		result, err := d.TableHealth(ctx, diagnostics.TableHealthParams{
			Schema:    in.Schema,
			MinSizeMB: tableHealthMinSizeMB(in.MinSizeMB),
		})
		if err != nil {
			return nil, TableHealthOut{}, err
		}

		out := TableHealthOut{Meta: newMeta(ctx, d), Tables: result}
		if out.Tables == nil {
			out.Tables = []diagnostics.TableFinding{}
		}
		for i := range out.Tables {
			if out.Tables[i].Flags == nil {
				out.Tables[i].Flags = []string{}
			}
		}

		return nil, out, nil
	}

	return tool, handler
}

// tableHealthMinSizeMB defaults an absent size floor, reads a negative one as
// "no floor", and caps an oversized one.
func tableHealthMinSizeMB(requested int64) int64 {
	switch {
	case requested == 0:
		return defaultTableHealthMinSizeMB
	case requested < 0:
		return 0
	case requested > maxTableHealthMinSizeMB:
		return maxTableHealthMinSizeMB
	default:
		return requested
	}
}
