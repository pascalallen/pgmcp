package tool

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
)

// Bounds on the in-process plan cache that resolves compare_to. Plans are
// kept only long enough for a model to run a second explain and diff it
// against the first.
const (
	planCacheTTL        = time.Hour
	planCacheMaxEntries = 256
)

// planCacheEntry is one remembered plan and the moment it was stored.
type planCacheEntry struct {
	result *diagnostics.ExplainResult
	at     time.Time
}

// planCacheMu guards planCache; explain handlers run concurrently.
var planCacheMu sync.Mutex

// planCache maps a plan hash onto the plan that produced it.
var planCache = map[string]planCacheEntry{}

// ExplainIn is the input of the explain tool.
type ExplainIn struct {
	SQL       string `json:"sql" jsonschema:"the statement to plan; must be a single read-only SELECT, EXPLAIN or SHOW"`
	Analyze   bool   `json:"analyze,omitempty" jsonschema:"execute the statement to collect real row counts and timings (default false, which only estimates)"`
	Buffers   *bool  `json:"buffers,omitempty" jsonschema:"include buffer usage; applies only alongside analyze (default true)"`
	CompareTo string `json:"compare_to,omitempty" jsonschema:"a plan_hash returned by an earlier explain call, to diff this plan against"`
}

// ExplainOut is the output of the explain tool.
type ExplainOut struct {
	Meta
	Plan         diagnostics.PlanNode  `json:"plan" jsonschema:"the plan tree; each node nests its inputs under children"`
	PlanningMs   float64               `json:"planning_ms,omitempty" jsonschema:"time spent planning the statement"`
	ExecutionMs  float64               `json:"execution_ms,omitempty" jsonschema:"time spent executing the statement; present only with analyze"`
	HotNodes     []diagnostics.HotNode `json:"hot_nodes" jsonschema:"the nodes with the most self time, hottest first; empty without analyze"`
	Warnings     []string              `json:"warnings" jsonschema:"plan problems worth acting on, such as bad row estimates, large sequential scans or sorts spilling to disk"`
	PlanHash     string                `json:"plan_hash" jsonschema:"stable hash of the plan shape, ignoring costs and timings; pass it as compare_to on a later call"`
	Diff         *diagnostics.PlanDiff `json:"diff,omitempty" jsonschema:"how this plan differs from the compare_to plan; present only when that plan was still cached"`
	CompareFound bool                  `json:"compare_found" jsonschema:"whether the compare_to plan was still in the cache"`
}

// explainOutputSchema is built once: the plan tree is recursive, so the
// schema names it in $defs and refers back to it rather than inlining it,
// which the SDK's inference cannot do on its own.
var explainOutputSchema = mustExplainOutputSchema()

// Explain returns the plan for a single read-only statement, diffing it
// against an earlier plan when asked.
func Explain(d diagnostics.Diagnostics, p sqlguard.Parser) (*mcp.Tool, mcp.ToolHandlerFor[ExplainIn, ExplainOut]) {
	tool := &mcp.Tool{
		Name:         "explain",
		Description:  "Plans one read-only statement and reports the plan tree, the nodes burning the most self time, plan warnings and a stable plan_hash. Use it to answer why a specific query is slow; set analyze=true to actually run the statement and collect real row counts and timings. Pass compare_to with a plan_hash from an earlier call to diff the two plan shapes after a change.",
		Annotations:  readOnly("Explain query plan"),
		OutputSchema: explainOutputSchema,
	}

	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in ExplainIn) (*mcp.CallToolResult, ExplainOut, error) {
		if err := sqlguard.Validate(in.SQL, p); err != nil {
			return nil, ExplainOut{}, sanitizeRejection(err)
		}

		buffers := true
		if in.Buffers != nil {
			buffers = *in.Buffers
		}

		result, err := d.Explain(ctx, diagnostics.ExplainParams{SQL: in.SQL, Analyze: in.Analyze, Buffers: buffers})
		if err != nil {
			return nil, ExplainOut{}, err
		}
		if result == nil {
			return nil, ExplainOut{}, errors.New("explain: diagnostics returned no plan")
		}

		out := ExplainOut{
			Meta:        newMeta(ctx, d),
			Plan:        result.Plan,
			PlanningMs:  result.PlanningMs,
			ExecutionMs: result.ExecutionMs,
			HotNodes:    result.HotNodes,
			Warnings:    result.Warnings,
			PlanHash:    result.PlanHash,
		}
		if out.HotNodes == nil {
			out.HotNodes = []diagnostics.HotNode{}
		}
		if out.Warnings == nil {
			out.Warnings = []string{}
		}
		fillChildren(&out.Plan)

		if in.CompareTo != "" {
			if previous, ok := lookupPlan(in.CompareTo); ok {
				diff := diagnostics.DiffPlans(previous, result)
				out.Diff = &diff
				out.CompareFound = true
			}
		}

		rememberPlan(result)

		return nil, out, nil
	}

	return tool, handler
}

// sanitizeRejection strips the detail of a parse failure. Every other
// rejection reason reports only a statement kind or a function name, but the
// parser's message can quote the statement it choked on, and tool errors must
// never echo SQL back.
func sanitizeRejection(err error) error {
	var rejection *sqlguard.Rejection
	if errors.As(err, &rejection) && rejection.Reason == sqlguard.ReasonParse {
		return fmt.Errorf("sqlguard: %s: statement could not be parsed", sqlguard.ReasonParse)
	}

	return err
}

// fillChildren replaces the nil child slices a plan tree may carry with empty
// ones, so no slice in a tool output is null.
func fillChildren(node *diagnostics.PlanNode) {
	if node.Children == nil {
		node.Children = []diagnostics.PlanNode{}
	}
	for i := range node.Children {
		fillChildren(&node.Children[i])
	}
}

// lookupPlan returns the plan stored under hash, dropping and missing it once
// it has aged past the cache TTL.
func lookupPlan(hash string) (*diagnostics.ExplainResult, bool) {
	planCacheMu.Lock()
	defer planCacheMu.Unlock()

	entry, ok := planCache[hash]
	if !ok {
		return nil, false
	}
	if time.Since(entry.at) > planCacheTTL {
		delete(planCache, hash)

		return nil, false
	}

	return entry.result, true
}

// rememberPlan stores a plan under its own hash, evicting the oldest entry
// when the cache is full so a long-lived server cannot grow it without bound.
func rememberPlan(result *diagnostics.ExplainResult) {
	if result.PlanHash == "" {
		return
	}

	planCacheMu.Lock()
	defer planCacheMu.Unlock()

	if _, known := planCache[result.PlanHash]; !known && len(planCache) >= planCacheMaxEntries {
		evictOldestPlan()
	}

	planCache[result.PlanHash] = planCacheEntry{result: result, at: time.Now()}
}

// evictOldestPlan drops the least recently stored entry. The caller holds planCacheMu.
func evictOldestPlan() {
	var (
		oldestHash string
		oldestAt   time.Time
	)
	for hash, entry := range planCache {
		if oldestHash == "" || entry.at.Before(oldestAt) {
			oldestHash, oldestAt = hash, entry.at
		}
	}
	delete(planCache, oldestHash)
}

// mustExplainOutputSchema builds the explain output schema, naming the
// recursive plan node in $defs. A failure here is a programming error in the
// output types, not a runtime condition.
func mustExplainOutputSchema() *jsonschema.Schema {
	node, err := jsonschema.For[diagnostics.PlanNode](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			reflect.TypeFor[[]diagnostics.PlanNode](): {
				Types: []string{"null", "array"},
				Items: &jsonschema.Schema{Ref: "#/$defs/plan_node"},
			},
		},
	})
	if err != nil {
		panic("tool: build plan node schema: " + err.Error())
	}

	out, err := jsonschema.For[ExplainOut](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			reflect.TypeFor[diagnostics.PlanNode](): {Ref: "#/$defs/plan_node"},
		},
	})
	if err != nil {
		panic("tool: build explain output schema: " + err.Error())
	}
	out.Defs = map[string]*jsonschema.Schema{"plan_node": node}

	return out
}
