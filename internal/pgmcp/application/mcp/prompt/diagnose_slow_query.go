// Package prompt registers the MCP prompts exposed by pgmcp.
package prompt

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const diagnoseSlowQueryName = "diagnose_slow_query"

// errMissingSQL is returned when a diagnose_slow_query prompt request omits
// the required sql argument.
var errMissingSQL = errors.New("missing required argument: sql")

// diagnoseSlowQueryTemplate is the investigation the model is instructed to
// follow. %s is the caller's sql, embedded verbatim in a fenced block.
const diagnoseSlowQueryTemplate = `Diagnose why the following SQL statement is slow. Work through these steps
in order, using only the read-only pgmcp tools:

1. Call explain with analyze=false first to see the planner's estimate. If
   the plan looks safe to run (no obvious unbounded write amplification, and
   the statement is a SELECT or otherwise side-effect-free), call explain
   again with analyze=true to get actual timings.
2. For each relation named in the plan's hot_nodes, call index_health(schema)
   and table_health(schema) on that relation's schema to check for missing,
   unused, duplicate, invalid, or bloated indexes and vacuum/bloat health.
3. Call top_queries(order_by=mean_time) and look for the same normalized
   statement, to see whether this is a one-off or a chronic offender and how
   it compares to other slow statements on this server.
4. Summarize your findings:
   - Root cause: what the plan and health checks show is actually slow.
   - Evidence: the specific hot_nodes paths, warnings, and index/table
     findings that support the root cause.
   - Recommended index or rewrite, with the DDL written out as TEXT ONLY.
     Never run DDL yourself — only propose it as text for a human to review
     and run.
   - Expected effect of the recommendation.

The SQL statement to diagnose:

` + "```sql\n%s\n```"

// Register adds every pgmcp prompt to s.
func Register(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        diagnoseSlowQueryName,
		Title:       "Diagnose a slow query",
		Description: "Investigate a slow SQL statement using explain, index/table health, and top_queries, and propose a fix as text only.",
		Arguments: []*mcp.PromptArgument{
			{Name: "sql", Required: true, Description: "The SQL statement to diagnose."},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		sql := req.Params.Arguments["sql"]
		if sql == "" {
			return nil, errMissingSQL
		}

		return &mcp.GetPromptResult{
			Description: "Investigation plan for a slow query.",
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: fmt.Sprintf(diagnoseSlowQueryTemplate, sql)},
				},
			},
		}, nil
	})
}
