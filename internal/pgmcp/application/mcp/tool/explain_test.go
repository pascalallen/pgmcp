package tool

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
)

// resetPlanCache empties the package-level plan cache so one subtest cannot
// see the plans another one remembered.
func resetPlanCache(t *testing.T) {
	t.Helper()

	planCacheMu.Lock()
	defer planCacheMu.Unlock()

	planCache = map[string]planCacheEntry{}
}

// seqScanPlan builds an explain result shaped like a single sequential scan.
func seqScanPlan(hash string, executionMs float64) *diagnostics.ExplainResult {
	return &diagnostics.ExplainResult{
		Plan:        diagnostics.PlanNode{NodeType: "Seq Scan", Relation: "orders", EstCost: 100, Children: []diagnostics.PlanNode{}},
		ExecutionMs: executionMs,
		HotNodes:    []diagnostics.HotNode{},
		Warnings:    []string{},
		PlanHash:    hash,
	}
}

func TestExplain(t *testing.T) {
	ctx := context.Background()

	t.Run("explain rejects an update through sqlguard before touching the port", func(t *testing.T) {
		resetPlanCache(t)

		called := false
		parser := fakeParser{statement: &sqlguard.Statement{Kinds: []string{"UpdateStmt"}, NodeTypes: map[string]bool{}}}

		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			called = true

			return seqScanPlan("abc", 0), nil
		}}, parser)

		_, _, err := handler(ctx, nil, ExplainIn{SQL: "UPDATE orders SET total = 0"})
		require.Error(t, err)

		var rejection *sqlguard.Rejection
		require.ErrorAs(t, err, &rejection)
		assert.Equal(t, sqlguard.ReasonKind, rejection.Reason)
		assert.False(t, called, "the port must never see a rejected statement")
	})

	t.Run("explain sanitizes a parse rejection so no statement text leaks back", func(t *testing.T) {
		resetPlanCache(t)

		parser := fakeParser{err: errors.New(`syntax error at or near "SELCT" in SELCT * FROM salaries`)}

		_, handler := Explain(fakeDiag{}, parser)

		_, _, err := handler(ctx, nil, ExplainIn{SQL: "SELCT * FROM salaries"})
		require.Error(t, err)
		assert.Equal(t, "sqlguard: parse_error: statement could not be parsed", err.Error())
		assert.NotContains(t, err.Error(), "salaries")

		var rejection *sqlguard.Rejection
		require.ErrorAs(t, err, &rejection, "the sanitized error is still a rejection callers can inspect")
		assert.Equal(t, sqlguard.ReasonParse, rejection.Reason)
	})

	t.Run("explain sanitizes a parse rejection the port raises when it revalidates", func(t *testing.T) {
		resetPlanCache(t)

		// The adapter re-validates before running EXPLAIN, so a rejection can
		// arrive from the port carrying the parser's message.
		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			return nil, &sqlguard.Rejection{
				Reason: sqlguard.ReasonParse,
				Detail: `syntax error at or near "SELCT" in SELCT * FROM salaries`,
			}
		}}, selectParser())

		_, _, err := handler(ctx, nil, ExplainIn{SQL: "SELCT * FROM salaries"})
		require.Error(t, err)
		assert.Equal(t, "sqlguard: parse_error: statement could not be parsed", err.Error())
		assert.NotContains(t, err.Error(), "salaries")
		assert.NotContains(t, err.Error(), "SELCT")

		var rejection *sqlguard.Rejection
		require.ErrorAs(t, err, &rejection)
		assert.Equal(t, sqlguard.ReasonParse, rejection.Reason)
		assert.Equal(t, "statement could not be parsed", rejection.Detail)
	})

	t.Run("explain passes a port rejection that names no statement text through unchanged", func(t *testing.T) {
		resetPlanCache(t)

		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			return nil, &sqlguard.Rejection{Reason: sqlguard.ReasonFunction, Detail: "pg_sleep"}
		}}, selectParser())

		_, _, err := handler(ctx, nil, ExplainIn{SQL: "SELECT pg_sleep(10)"})
		require.Error(t, err)
		assert.Equal(t, "sqlguard: function_not_allowed: pg_sleep", err.Error())
	})

	t.Run("explain defaults buffers on and passes analyze through", func(t *testing.T) {
		resetPlanCache(t)

		var received diagnostics.ExplainParams

		_, handler := Explain(fakeDiag{explain: func(_ context.Context, p diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			received = p

			return seqScanPlan("abc", 12), nil
		}}, selectParser())

		_, _, err := handler(ctx, nil, ExplainIn{SQL: "SELECT 1", Analyze: true})
		require.NoError(t, err)

		assert.Equal(t, "SELECT 1", received.SQL)
		assert.True(t, received.Analyze)
		assert.True(t, received.Buffers, "buffers defaults to on")
	})

	t.Run("explain honours buffers turned off explicitly", func(t *testing.T) {
		resetPlanCache(t)

		var received diagnostics.ExplainParams

		_, handler := Explain(fakeDiag{explain: func(_ context.Context, p diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			received = p

			return seqScanPlan("abc", 0), nil
		}}, selectParser())

		_, _, err := handler(ctx, nil, ExplainIn{SQL: "SELECT 1", Analyze: true, Buffers: ptr(false)})
		require.NoError(t, err)
		assert.False(t, received.Buffers)
	})

	t.Run("explain returns the plan hot nodes warnings and hash with the meta block", func(t *testing.T) {
		resetPlanCache(t)

		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			return &diagnostics.ExplainResult{
				Plan:        diagnostics.PlanNode{NodeType: "Seq Scan", Relation: "orders", EstCost: 100},
				PlanningMs:  0.4,
				ExecutionMs: 25,
				HotNodes:    []diagnostics.HotNode{{Path: "0", NodeType: "Seq Scan", SelfMs: 25, PctOfTotal: 100}},
				Warnings:    []string{"Sequential scan over large relation orders"},
				PlanHash:    "deadbeefdeadbeef",
			}, nil
		}}, selectParser())

		_, out, err := handler(ctx, nil, ExplainIn{SQL: "SELECT * FROM orders"})
		require.NoError(t, err)

		assert.Equal(t, "Seq Scan", out.Plan.NodeType)
		assert.NotNil(t, out.Plan.Children, "a leaf node reports an empty child list, never null")
		assert.Equal(t, 0.4, out.PlanningMs)
		assert.Equal(t, 25.0, out.ExecutionMs)
		require.Len(t, out.HotNodes, 1)
		require.Len(t, out.Warnings, 1)
		assert.Equal(t, "deadbeefdeadbeef", out.PlanHash)
		assert.Nil(t, out.Diff)
		assert.False(t, out.CompareFound)
		assert.Equal(t, "16.4", out.ServerVersion)
	})

	t.Run("explain fills empty hot node and warning lists rather than returning null", func(t *testing.T) {
		resetPlanCache(t)

		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			return &diagnostics.ExplainResult{Plan: diagnostics.PlanNode{NodeType: "Result"}, PlanHash: "abc"}, nil
		}}, selectParser())

		_, out, err := handler(ctx, nil, ExplainIn{SQL: "SELECT 1"})
		require.NoError(t, err)

		assert.NotNil(t, out.HotNodes)
		assert.NotNil(t, out.Warnings)
		assert.Empty(t, out.HotNodes)
		assert.Empty(t, out.Warnings)
	})

	t.Run("explain diffs a new plan against a cached plan hash", func(t *testing.T) {
		resetPlanCache(t)

		first := seqScanPlan("hash-seq", 40)
		second := &diagnostics.ExplainResult{
			Plan:        diagnostics.PlanNode{NodeType: "Index Scan", Relation: "orders", IndexName: "orders_pkey", EstCost: 8, Children: []diagnostics.PlanNode{}},
			ExecutionMs: 2,
			HotNodes:    []diagnostics.HotNode{},
			Warnings:    []string{},
			PlanHash:    "hash-index",
		}

		plans := []*diagnostics.ExplainResult{first, second}
		call := 0

		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			plan := plans[call]
			call++

			return plan, nil
		}}, selectParser())

		_, _, err := handler(ctx, nil, ExplainIn{SQL: "SELECT * FROM orders WHERE id = 1"})
		require.NoError(t, err)

		_, out, err := handler(ctx, nil, ExplainIn{SQL: "SELECT * FROM orders WHERE id = 1", CompareTo: "hash-seq"})
		require.NoError(t, err)

		assert.True(t, out.CompareFound)
		require.NotNil(t, out.Diff)
		assert.False(t, out.Diff.Same)
		assert.Equal(t, []string{"Index Scan|orders|orders_pkey|"}, out.Diff.Added)
		assert.Equal(t, []string{"Seq Scan|orders||"}, out.Diff.Removed)
		assert.InDelta(t, -92.0, out.Diff.CostDelta, 0.001)
		assert.InDelta(t, -38.0, out.Diff.TimeDeltaMs, 0.001)
	})

	t.Run("explain reports compare found false for a hash it has never seen", func(t *testing.T) {
		resetPlanCache(t)

		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			return seqScanPlan("hash-seq", 5), nil
		}}, selectParser())

		_, out, err := handler(ctx, nil, ExplainIn{SQL: "SELECT 1", CompareTo: "never-stored"})
		require.NoError(t, err)

		assert.False(t, out.CompareFound)
		assert.Nil(t, out.Diff)
	})

	t.Run("explain forgets a cached plan once it has aged past the cache ttl", func(t *testing.T) {
		resetPlanCache(t)

		planCacheMu.Lock()
		planCache["stale"] = planCacheEntry{result: seqScanPlan("stale", 1), at: time.Now().Add(-2 * planCacheTTL)}
		planCacheMu.Unlock()

		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			return seqScanPlan("fresh", 1), nil
		}}, selectParser())

		_, out, err := handler(ctx, nil, ExplainIn{SQL: "SELECT 1", CompareTo: "stale"})
		require.NoError(t, err)
		assert.False(t, out.CompareFound)

		planCacheMu.Lock()
		_, stillCached := planCache["stale"]
		planCacheMu.Unlock()
		assert.False(t, stillCached, "an expired entry is dropped when it is missed")
	})

	t.Run("explain evicts the oldest plan once the cache is full", func(t *testing.T) {
		resetPlanCache(t)

		oldest := time.Now().Add(-time.Minute)

		planCacheMu.Lock()
		for i := range planCacheMaxEntries {
			hash := "hash-" + strconv.Itoa(i)
			planCache[hash] = planCacheEntry{result: seqScanPlan(hash, 1), at: oldest.Add(time.Duration(i) * time.Second)}
		}
		planCacheMu.Unlock()

		rememberPlan(seqScanPlan("newcomer", 1))

		planCacheMu.Lock()
		size := len(planCache)
		_, evicted := planCache["hash-0"]
		_, kept := planCache["newcomer"]
		planCacheMu.Unlock()

		assert.Equal(t, planCacheMaxEntries, size)
		assert.False(t, evicted, "the oldest entry makes room for the newcomer")
		assert.True(t, kept)
	})

	t.Run("explain does not cache a plan without a hash", func(t *testing.T) {
		resetPlanCache(t)

		rememberPlan(seqScanPlan("", 1))

		planCacheMu.Lock()
		size := len(planCache)
		planCacheMu.Unlock()

		assert.Zero(t, size)
	})

	t.Run("explain propagates a port failure", func(t *testing.T) {
		resetPlanCache(t)

		failure := errors.New("relation does not exist")

		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			return nil, failure
		}}, selectParser())

		_, _, err := handler(ctx, nil, ExplainIn{SQL: "SELECT 1"})
		require.ErrorIs(t, err, failure)
	})

	t.Run("explain reports a missing plan as an error", func(t *testing.T) {
		resetPlanCache(t)

		_, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			return nil, nil
		}}, selectParser())

		_, _, err := handler(ctx, nil, ExplainIn{SQL: "SELECT 1"})
		require.EqualError(t, err, "explain: diagnostics returned no plan")
	})

	t.Run("explain answers over an mcp session and lists itself as read only", func(t *testing.T) {
		resetPlanCache(t)

		definition, handler := Explain(fakeDiag{explain: func(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
			return &diagnostics.ExplainResult{
				Plan: diagnostics.PlanNode{
					NodeType: "Nested Loop",
					EstCost:  120,
					Children: []diagnostics.PlanNode{
						{NodeType: "Seq Scan", Relation: "orders", EstCost: 80, Children: []diagnostics.PlanNode{}},
						{NodeType: "Index Scan", Relation: "customers", IndexName: "customers_pkey", EstCost: 40},
					},
				},
				ExecutionMs: 31,
				HotNodes:    []diagnostics.HotNode{{Path: "0.0", NodeType: "Seq Scan", Relation: "orders", SelfMs: 30, PctOfTotal: 96.7}},
				Warnings:    []string{"Sequential scan over large relation orders"},
				PlanHash:    "deadbeefdeadbeef",
			}, nil
		}}, selectParser())

		session := serveTool(t, definition, handler)

		out := callStructured[ExplainOut](t, session, "explain", ExplainIn{SQL: "SELECT * FROM orders JOIN customers USING (id)", Analyze: true})
		assert.Equal(t, "Nested Loop", out.Plan.NodeType)
		require.Len(t, out.Plan.Children, 2)
		assert.Equal(t, "orders", out.Plan.Children[0].Relation)
		assert.NotNil(t, out.Plan.Children[1].Children, "the recursive plan schema round trips every level")
		require.Len(t, out.HotNodes, 1)
		require.Len(t, out.Warnings, 1)
		assert.Equal(t, "deadbeefdeadbeef", out.PlanHash)
		assert.False(t, out.CompareFound)

		requireReadOnlyListing(t, session, "explain", "Explain query plan")
	})

	t.Run("explain reports a rejected statement as a tool error over an mcp session", func(t *testing.T) {
		resetPlanCache(t)

		parser := fakeParser{statement: &sqlguard.Statement{Kinds: []string{"SelectStmt"}, NodeTypes: map[string]bool{"LockingClause": true}}}

		definition, handler := Explain(fakeDiag{}, parser)

		session := serveTool(t, definition, handler)

		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "explain", Arguments: ExplainIn{SQL: "SELECT 1 FOR UPDATE"}})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, contentText(result), "locking_clause_not_allowed")
	})
}
