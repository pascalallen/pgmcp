package diagnostics_test

// The testdata/plan_*.json fixtures are verbatim output of
// EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) captured on postgres:16 against:
//
//	CREATE TABLE t(id int primary key, grp int, pad text);
//	INSERT INTO t SELECT g, g%10, repeat('a',50) || g FROM generate_series(1,200000) g;
//	CREATE TABLE b(id int, grp int, k int);
//	INSERT INTO b SELECT g, g%20, g%20 FROM generate_series(1,200000) g;
//	ANALYZE t; ANALYZE b;
//
// plan_seqscan.json:
//
//	EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT * FROM t WHERE pad LIKE '%a%';
//
// plan_nestloop_badestimate.json (b.grp and b.k are perfectly correlated, so the
// planner multiplies their selectivities and underestimates by ~20x):
//
//	SET max_parallel_workers_per_gather = 0;
//	SET enable_hashjoin = off;
//	SET enable_mergejoin = off;
//	EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
//	SELECT x.id, y.id FROM b x JOIN b y ON x.grp = y.grp
//	WHERE x.grp = 5 AND x.k = 5 AND y.id < 200;
//
// plan_disksort.json:
//
//	SET max_parallel_workers_per_gather = 0;
//	SET work_mem = '64kB';
//	EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT * FROM t ORDER BY pad;

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	return raw
}

func parseFixture(t *testing.T, name string) *diagnostics.ExplainResult {
	t.Helper()

	result, err := diagnostics.ParseExplainJSON(readFixture(t, name))
	require.NoError(t, err)

	return result
}

func TestParseExplainJSON(t *testing.T) {
	t.Run("parses a simple seq scan fixture into a tree with self time", func(t *testing.T) {
		result := parseFixture(t, "plan_seqscan.json")

		assert.Equal(t, "Seq Scan", result.Plan.NodeType)
		assert.Equal(t, "t", result.Plan.Relation)
		assert.Equal(t, "t", result.Plan.Alias)
		assert.InDelta(t, 199980, result.Plan.EstRows, 0.001)
		assert.InDelta(t, 200000, result.Plan.ActualRows, 0.001)
		assert.Equal(t, 1, result.Plan.Loops)
		assert.InDelta(t, 4871.00, result.Plan.EstCost, 0.001)
		assert.InDelta(t, 16.859, result.Plan.TotalMs, 0.001)
		assert.InDelta(t, 16.859, result.Plan.SelfMs, 0.001)
		assert.Equal(t, int64(2371), result.Plan.SharedHit)
		assert.Empty(t, result.Plan.Children)
		assert.InDelta(t, 0.109, result.PlanningMs, 0.001)
		assert.InDelta(t, 21.920, result.ExecutionMs, 0.001)
		assert.Equal(t, readFixture(t, "plan_seqscan.json"), []byte(result.Raw))
		assert.NotEmpty(t, result.PlanHash)
	})

	t.Run("reports the root node as hot node zero with its share of execution time", func(t *testing.T) {
		result := parseFixture(t, "plan_seqscan.json")

		require.Len(t, result.HotNodes, 1)
		assert.Equal(t, "0", result.HotNodes[0].Path)
		assert.Equal(t, "Seq Scan", result.HotNodes[0].NodeType)
		assert.Equal(t, "t", result.HotNodes[0].Relation)
		assert.InDelta(t, 16.859, result.HotNodes[0].SelfMs, 0.001)
		assert.InDelta(t, 76.91, result.HotNodes[0].PctOfTotal, 0.01)
	})

	t.Run("flags a sequential scan over a large relation", func(t *testing.T) {
		result := parseFixture(t, "plan_seqscan.json")

		assert.Equal(t, []string{"Sequential scan over large relation t"}, result.Warnings)
	})

	t.Run("flags a 10x row estimate miss", func(t *testing.T) {
		result := parseFixture(t, "plan_nestloop_badestimate.json")

		assert.Contains(t, result.Warnings, "Row estimate off by 22x at 0 (Nested Loop)")
		assert.Contains(t, result.Warnings, "Row estimate off by 20x at 0.0 (Seq Scan on b)")
	})

	t.Run("flags a nested loop with a large inner side", func(t *testing.T) {
		result := parseFixture(t, "plan_nestloop_badestimate.json")

		assert.Contains(t, result.Warnings, "Nested loop with large inner side at 0")
	})

	// EXPLAIN already parenthesises the filter expression, so the warning's own
	// parentheses around it double up; the wording still follows the spec.
	t.Run("flags a filter that discards most rows", func(t *testing.T) {
		result := parseFixture(t, "plan_nestloop_badestimate.json")

		assert.Contains(t, result.Warnings, "Filter discards most rows at 0.0 (((grp = 5) AND (k = 5)))")
		assert.Contains(t, result.Warnings, "Filter discards most rows at 0.1.0 (((id < 200) AND (grp = 5)))")
	})

	t.Run("walks child paths depth first", func(t *testing.T) {
		result := parseFixture(t, "plan_nestloop_badestimate.json")

		require.Len(t, result.Plan.Children, 2)
		assert.Equal(t, "Seq Scan", result.Plan.Children[0].NodeType)
		assert.Equal(t, "Materialize", result.Plan.Children[1].NodeType)
		require.Len(t, result.Plan.Children[1].Children, 1)
		assert.Equal(t, "y", result.Plan.Children[1].Children[0].Alias)
		assert.Equal(t, 10000, result.Plan.Children[1].Loops)
		assert.Equal(t, "Inner", result.Plan.JoinType)
	})

	t.Run("flags disk sort", func(t *testing.T) {
		result := parseFixture(t, "plan_disksort.json")

		assert.Equal(t, "external merge", result.Plan.SortMethod)
		assert.Equal(t, int64(7743), result.Plan.TempWritten)
		assert.Contains(t, result.Warnings, "Sort/hash spilled to disk at 0")
	})

	t.Run("subtracts child time from parent self time", func(t *testing.T) {
		result := parseFixture(t, "plan_disksort.json")

		assert.InDelta(t, 489.944, result.Plan.SelfMs, 0.001)
		require.Len(t, result.HotNodes, 2)
		assert.Equal(t, "0", result.HotNodes[0].Path)
		assert.InDelta(t, 489.944, result.HotNodes[0].SelfMs, 0.001)
		assert.Equal(t, "0.0", result.HotNodes[1].Path)
		assert.InDelta(t, 7.977, result.HotNodes[1].SelfMs, 0.001)
	})

	t.Run("rejects malformed json", func(t *testing.T) {
		_, err := diagnostics.ParseExplainJSON([]byte("{not json"))

		require.Error(t, err)
	})

	t.Run("rejects an empty plan array", func(t *testing.T) {
		_, err := diagnostics.ParseExplainJSON([]byte("[]"))

		require.Error(t, err)
	})
}

func TestAnalyzePlan(t *testing.T) {
	t.Run("returns empty slices for a plan with no timings and no problems", func(t *testing.T) {
		hot, warnings := diagnostics.AnalyzePlan(&diagnostics.PlanNode{NodeType: "Result"}, 0)

		assert.Empty(t, hot)
		assert.Empty(t, warnings)
		assert.NotNil(t, hot)
		assert.NotNil(t, warnings)
	})

	t.Run("keeps only the three hottest nodes", func(t *testing.T) {
		root := &diagnostics.PlanNode{NodeType: "Append", TotalMs: 100, Loops: 1, Children: []diagnostics.PlanNode{
			{NodeType: "Seq Scan", Relation: "a", TotalMs: 40, Loops: 1},
			{NodeType: "Seq Scan", Relation: "b", TotalMs: 30, Loops: 1},
			{NodeType: "Seq Scan", Relation: "c", TotalMs: 20, Loops: 1},
			{NodeType: "Seq Scan", Relation: "d", TotalMs: 5, Loops: 1},
		}}

		hot, _ := diagnostics.AnalyzePlan(root, 100)

		require.Len(t, hot, 3)
		assert.Equal(t, []string{"0.0", "0.1", "0.2"}, []string{hot[0].Path, hot[1].Path, hot[2].Path})
		assert.InDelta(t, 40, hot[0].SelfMs, 0.001)
		assert.InDelta(t, 5, root.SelfMs, 0.001)
	})

	t.Run("clamps self time at zero when children report more time than the parent", func(t *testing.T) {
		root := &diagnostics.PlanNode{NodeType: "Limit", TotalMs: 1, Loops: 1, Children: []diagnostics.PlanNode{
			{NodeType: "Seq Scan", Relation: "a", TotalMs: 9, Loops: 1},
		}}

		_, _ = diagnostics.AnalyzePlan(root, 10)

		assert.Zero(t, root.SelfMs)
	})

	t.Run("reports zero percent of total when execution time is unknown", func(t *testing.T) {
		root := &diagnostics.PlanNode{NodeType: "Seq Scan", Relation: "a", TotalMs: 7, Loops: 1}

		hot, _ := diagnostics.AnalyzePlan(root, 0)

		require.Len(t, hot, 1)
		assert.Zero(t, hot[0].PctOfTotal)
	})
}

func TestHashPlan(t *testing.T) {
	t.Run("hash is stable across cost and timing changes", func(t *testing.T) {
		raw := string(readFixture(t, "plan_seqscan.json"))
		retimed := strings.NewReplacer(
			"4871.00", "99999.00",
			"16.859", "1234.567",
			"21.920", "9999.999",
			"199980", "17",
		).Replace(raw)
		require.NotEqual(t, raw, retimed)

		original, err := diagnostics.ParseExplainJSON([]byte(raw))
		require.NoError(t, err)
		changed, err := diagnostics.ParseExplainJSON([]byte(retimed))
		require.NoError(t, err)

		assert.Equal(t, original.PlanHash, changed.PlanHash)
		assert.Len(t, original.PlanHash, 16)
	})

	t.Run("hash changes when the plan shape changes", func(t *testing.T) {
		seqScan := parseFixture(t, "plan_seqscan.json")
		diskSort := parseFixture(t, "plan_disksort.json")

		assert.NotEqual(t, seqScan.PlanHash, diskSort.PlanHash)
	})

	t.Run("hash of a nil plan is empty", func(t *testing.T) {
		assert.Empty(t, diagnostics.HashPlan(nil))
	})
}

func TestDiffPlans(t *testing.T) {
	t.Run("diff reports added and removed nodes", func(t *testing.T) {
		seqScan := parseFixture(t, "plan_seqscan.json")
		diskSort := parseFixture(t, "plan_disksort.json")

		diff := diagnostics.DiffPlans(seqScan, diskSort)

		assert.False(t, diff.Same)
		assert.Equal(t, []string{"Sort|||"}, diff.Added)
		assert.Empty(t, diff.Removed)
		assert.InDelta(t, 52566.64-4871.00, diff.CostDelta, 0.001)
		assert.InDelta(t, 502.434-21.920, diff.TimeDeltaMs, 0.001)
	})

	t.Run("diff of the reversed pair swaps added and removed", func(t *testing.T) {
		seqScan := parseFixture(t, "plan_seqscan.json")
		diskSort := parseFixture(t, "plan_disksort.json")

		diff := diagnostics.DiffPlans(diskSort, seqScan)

		assert.Empty(t, diff.Added)
		assert.Equal(t, []string{"Sort|||"}, diff.Removed)
	})

	t.Run("reports identical plans as the same shape", func(t *testing.T) {
		diff := diagnostics.DiffPlans(parseFixture(t, "plan_disksort.json"), parseFixture(t, "plan_disksort.json"))

		assert.True(t, diff.Same)
		assert.Empty(t, diff.Added)
		assert.Empty(t, diff.Removed)
		assert.Zero(t, diff.CostDelta)
	})

	t.Run("reports nothing for a missing side", func(t *testing.T) {
		diff := diagnostics.DiffPlans(nil, parseFixture(t, "plan_disksort.json"))

		assert.False(t, diff.Same)
		assert.NotNil(t, diff.Added)
		assert.NotNil(t, diff.Removed)
	})
}
