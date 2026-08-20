package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// Bounds and defaults the tool applies before the port sees them, so an
// unbounded or missing limit can never reach the database.
const (
	defaultTopQueriesLimit   = 20
	maxTopQueriesLimit       = 100
	defaultTopQueriesOrderBy = diagnostics.OrderByTotalTime
)

// topQueriesOrderings are the rankings the tool accepts, in the order the
// description advertises them.
var topQueriesOrderings = []diagnostics.OrderBy{
	diagnostics.OrderByTotalTime,
	diagnostics.OrderByMeanTime,
	diagnostics.OrderByCalls,
	diagnostics.OrderByRows,
	diagnostics.OrderBySharedBlksRead,
}

// TopQueriesIn is the input of the top_queries tool.
type TopQueriesIn struct {
	OrderBy  string `json:"order_by,omitempty" jsonschema:"ranking column: total_time, mean_time, calls, rows or shared_blks_read (default total_time)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"how many statements to return, 1 to 100 (default 20)"`
	MinCalls int64  `json:"min_calls,omitempty" jsonschema:"ignore statements called fewer times than this, to filter out one-off queries"`
	Database string `json:"database,omitempty" jsonschema:"restrict to statements recorded against this database (default: every database)"`
}

// TopQueriesOut is the output of the top_queries tool.
type TopQueriesOut struct {
	Meta
	Available  bool                        `json:"available" jsonschema:"false when pg_stat_statements is not installed, in which case hint says how to install it"`
	Hint       string                      `json:"hint,omitempty" jsonschema:"how to make the statistics available when they are not"`
	StatsSince *time.Time                  `json:"stats_since,omitempty" jsonschema:"when the statistics were last reset; totals cover the window since then"`
	Statements []diagnostics.StatementStat `json:"statements" jsonschema:"the heaviest statements, ordered by the requested column"`
}

// TopQueries reports the heaviest statements pg_stat_statements has recorded.
func TopQueries(d diagnostics.Diagnostics) (*mcp.Tool, mcp.ToolHandlerFor[TopQueriesIn, TopQueriesOut]) {
	tool := &mcp.Tool{
		Name:        "top_queries",
		Description: "Lists the statements pg_stat_statements has recorded, ranked by total time, mean time, calls, rows or shared blocks read. Start here when the question is which queries are slow or expensive server-wide, then feed a candidate statement into explain. When the extension is missing the result reports available=false and a hint that installs it, rather than failing.",
		Annotations: readOnly("Top queries"),
	}

	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in TopQueriesIn) (*mcp.CallToolResult, TopQueriesOut, error) {
		orderBy, err := topQueriesOrderBy(in.OrderBy)
		if err != nil {
			return nil, TopQueriesOut{}, err
		}

		minCalls := in.MinCalls
		if minCalls < 0 {
			minCalls = 0
		}

		result, err := d.TopQueries(ctx, diagnostics.TopQueriesParams{
			OrderBy:  orderBy,
			Limit:    topQueriesLimit(in.Limit),
			MinCalls: minCalls,
			Database: in.Database,
		})
		if err != nil {
			return nil, TopQueriesOut{}, err
		}
		if result == nil {
			result = &diagnostics.TopQueriesResult{}
		}

		out := TopQueriesOut{
			Meta:       newMeta(ctx, d),
			Available:  result.Available,
			Hint:       result.Hint,
			StatsSince: result.StatsSince,
			Statements: result.Statements,
		}
		if out.Statements == nil {
			out.Statements = []diagnostics.StatementStat{}
		}

		return nil, out, nil
	}

	return tool, handler
}

// topQueriesOrderBy resolves the requested ranking, defaulting when it is
// absent and rejecting anything outside the set so the model is told what it
// may ask for instead of silently getting a different ordering.
func topQueriesOrderBy(requested string) (diagnostics.OrderBy, error) {
	if requested == "" {
		return defaultTopQueriesOrderBy, nil
	}

	for _, ordering := range topQueriesOrderings {
		if diagnostics.OrderBy(requested) == ordering {
			return ordering, nil
		}
	}

	allowed := make([]string, 0, len(topQueriesOrderings))
	for _, ordering := range topQueriesOrderings {
		allowed = append(allowed, string(ordering))
	}

	return "", fmt.Errorf("top_queries: unsupported order_by %q; expected one of %s", requested, strings.Join(allowed, ", "))
}

// topQueriesLimit defaults a missing limit and clamps an oversized one.
func topQueriesLimit(requested int) int {
	switch {
	case requested <= 0:
		return defaultTopQueriesLimit
	case requested > maxTopQueriesLimit:
		return maxTopQueriesLimit
	default:
		return requested
	}
}
