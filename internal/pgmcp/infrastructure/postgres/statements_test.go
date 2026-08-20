package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// taggedStatement is the query the top-queries tests look for. Two properties
// make it findable in a pg_stat_statements the whole package shares:
//
// Its tag is a table name, not a comment or an output alias. The query id is
// computed from a jumble of the parse tree that skips both, so "SELECT 1
// /* tag */" and "SELECT 1 AS tag" are folded into the entry an earlier
// "SELECT 1" already created and the tag is never stored. A relation
// reference is jumbled, so this statement always gets an entry of its own.
//
// It sleeps, so its total execution time dwarfs every other statement the
// package runs (tens of microseconds each) and it sits at the top of an
// ordering by total time no matter how busy the view is.
const taggedStatement = "SELECT count(*) FROM pgmcp_test.pgmcp_top_test, pg_sleep(0.1)"

// taggedStatementTag is the substring the tests match the recorded query text
// on, and the name of the table taggedStatement reads.
const taggedStatementTag = "pgmcp_top_test"

// taggedStatementSleepMs is how long one execution of taggedStatement sleeps.
const taggedStatementSleepMs = 100

// taggedStatementRuns is how often the tests execute taggedStatement.
const taggedStatementRuns = 3

// requirePgStatStatements skips the test when the extension is not installed
// in the test database.
func requirePgStatStatements(t *testing.T, ctx context.Context) {
	t.Helper()

	if !hasPgStatStatements(t, ctx) {
		t.Skip("pg_stat_statements is not installed; skipping")
	}
}

// seedTopQueriesFixture creates the table taggedStatement reads and gives it
// a row, so that the cross join with pg_sleep always executes. It is
// idempotent so repeated runs against the same database are safe.
func seedTopQueriesFixture(t *testing.T, ctx context.Context) {
	t.Helper()

	db := rawDB(t)
	_, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS pgmcp_test")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS pgmcp_test.pgmcp_top_test(n int NOT NULL)")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO pgmcp_test.pgmcp_top_test(n)
SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM pgmcp_test.pgmcp_top_test)`)
	require.NoError(t, err)
}

// runTaggedStatement seeds the fixture and executes taggedStatement over a
// raw connection, so that pg_stat_statements records it.
func runTaggedStatement(t *testing.T, ctx context.Context, times int) {
	t.Helper()

	seedTopQueriesFixture(t, ctx)

	db := rawDB(t)
	for range times {
		var rows int64
		require.NoError(t, db.QueryRowContext(ctx, taggedStatement).Scan(&rows))
		require.Equal(t, int64(1), rows)
	}
}

// findStatement returns the recorded statement whose query text contains the
// tag, failing the test when it is absent.
func findStatement(t *testing.T, statements []diagnostics.StatementStat) diagnostics.StatementStat {
	t.Helper()

	for _, statement := range statements {
		if strings.Contains(statement.Query, taggedStatementTag) {
			return statement
		}
	}
	t.Fatalf("no recorded statement contains %q", taggedStatementTag)

	return diagnostics.StatementStat{}
}

// seedExplainFixture creates the table the explain tests plan against and
// fills it once. It is idempotent so repeated runs against the same database
// are safe.
func seedExplainFixture(t *testing.T, ctx context.Context) {
	t.Helper()

	db := rawDB(t)
	_, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS pgmcp_test")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS pgmcp_test.explain_t(n int NOT NULL)")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO pgmcp_test.explain_t(n)
SELECT g FROM generate_series(1, 20000) g
WHERE NOT EXISTS (SELECT 1 FROM pgmcp_test.explain_t)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "ANALYZE pgmcp_test.explain_t")
	require.NoError(t, err)
}

// explainFixtureChecksum returns the row count and sum of the fixture table,
// which a write would change.
func explainFixtureChecksum(t *testing.T, ctx context.Context) (int64, int64) {
	t.Helper()

	var count, sum int64
	require.NoError(t, rawDB(t).QueryRowContext(ctx,
		"SELECT count(*), coalesce(sum(n), 0) FROM pgmcp_test.explain_t").Scan(&count, &sum))

	return count, sum
}

func TestStoreTopQueries(t *testing.T) {
	t.Run("top queries reports the extension as available and lists a known statement", func(t *testing.T) {
		ctx := context.Background()
		store := testStore(t)
		requirePgStatStatements(t, ctx)
		runTaggedStatement(t, ctx, taggedStatementRuns)

		// Ordered by total time the tagged statement leads by construction:
		// it sleeps, and nothing else the package runs takes a millisecond.
		result, err := store.TopQueries(ctx, diagnostics.TopQueriesParams{
			OrderBy:  diagnostics.OrderByTotalTime,
			Limit:    maxTopQueriesLimit,
			MinCalls: taggedStatementRuns,
		})

		require.NoError(t, err)
		assert.True(t, result.Available)
		assert.Empty(t, result.Hint)
		require.NotNil(t, result.Statements)
		require.NotEmpty(t, result.Statements)

		info, err := store.ServerInfo(ctx)
		require.NoError(t, err)
		if info.VersionNum >= 140000 {
			assert.NotNil(t, result.StatsSince)
		}

		statement := findStatement(t, result.Statements)
		assert.NotZero(t, statement.QueryID)
		assert.GreaterOrEqual(t, statement.Calls, int64(taggedStatementRuns))
		// The recorded timings are the sleeps, minus a little slack for a
		// clock that rounds down.
		assert.GreaterOrEqual(t, statement.MeanMs, float64(taggedStatementSleepMs)*0.9)
		assert.GreaterOrEqual(t, statement.TotalMs, float64(taggedStatementSleepMs*taggedStatementRuns)*0.9)
		assert.GreaterOrEqual(t, statement.StddevMs, float64(0))
		assert.GreaterOrEqual(t, statement.HitRatio, float64(0))
		assert.LessOrEqual(t, statement.HitRatio, float64(1))
		assert.GreaterOrEqual(t, statement.TempBlks, int64(0))
		assert.LessOrEqual(t, len([]rune(statement.Query)), 2000)
	})

	t.Run("top queries honours min_calls and limit", func(t *testing.T) {
		ctx := context.Background()
		store := testStore(t)
		requirePgStatStatements(t, ctx)

		limited, err := store.TopQueries(ctx, diagnostics.TopQueriesParams{MinCalls: 2, Limit: 2})

		require.NoError(t, err)
		assert.LessOrEqual(t, len(limited.Statements), 2)
		for _, statement := range limited.Statements {
			assert.GreaterOrEqual(t, statement.Calls, int64(2))
		}

		defaulted, err := store.TopQueries(ctx, diagnostics.TopQueriesParams{})

		require.NoError(t, err)
		assert.LessOrEqual(t, len(defaulted.Statements), defaultTopQueriesLimit)

		clamped, err := store.TopQueries(ctx, diagnostics.TopQueriesParams{Limit: 5000})

		require.NoError(t, err)
		assert.LessOrEqual(t, len(clamped.Statements), maxTopQueriesLimit)

		unreachable, err := store.TopQueries(ctx, diagnostics.TopQueriesParams{MinCalls: 1 << 40})

		require.NoError(t, err)
		assert.NotNil(t, unreachable.Statements)
		assert.Empty(t, unreachable.Statements)
	})

	t.Run("top queries orders by calls when asked", func(t *testing.T) {
		ctx := context.Background()
		store := testStore(t)
		requirePgStatStatements(t, ctx)

		result, err := store.TopQueries(ctx, diagnostics.TopQueriesParams{OrderBy: diagnostics.OrderByCalls, Limit: 10})

		require.NoError(t, err)
		require.NotEmpty(t, result.Statements)
		for i := 1; i < len(result.Statements); i++ {
			assert.LessOrEqual(t, result.Statements[i].Calls, result.Statements[i-1].Calls)
		}
	})

	t.Run("top queries filters to the named database", func(t *testing.T) {
		ctx := context.Background()
		store := testStore(t)
		requirePgStatStatements(t, ctx)

		result, err := store.TopQueries(ctx, diagnostics.TopQueriesParams{
			OrderBy:  diagnostics.OrderByCalls,
			Limit:    maxTopQueriesLimit,
			MinCalls: 1,
			Database: "pgmcp_no_such_database",
		})

		require.NoError(t, err)
		assert.True(t, result.Available)
		assert.NotNil(t, result.Statements)
		assert.Empty(t, result.Statements)
	})

	t.Run("top queries rejects an unknown ordering", func(t *testing.T) {
		store := testStore(t)

		_, err := store.TopQueries(context.Background(), diagnostics.TopQueriesParams{OrderBy: "nonsense"})

		require.Error(t, err)
	})
}

func TestStoreExplain(t *testing.T) {
	t.Run("explain without analyze returns a plan with zero actual rows", func(t *testing.T) {
		ctx := context.Background()
		store := testStore(t)
		seedExplainFixture(t, ctx)

		result, err := store.Explain(ctx, diagnostics.ExplainParams{SQL: "SELECT * FROM pgmcp_test.explain_t"})

		require.NoError(t, err)
		assert.Equal(t, "Seq Scan", result.Plan.NodeType)
		assert.Equal(t, "explain_t", result.Plan.Relation)
		assert.Greater(t, result.Plan.EstRows, float64(0))
		assert.Greater(t, result.Plan.EstCost, float64(0))
		assert.Zero(t, result.Plan.ActualRows)
		assert.Zero(t, result.Plan.TotalMs)
		assert.Zero(t, result.ExecutionMs)
		assert.Len(t, result.PlanHash, 16)
		assert.NotNil(t, result.HotNodes)
		assert.NotNil(t, result.Warnings)
		assert.NotEmpty(t, result.Raw)
	})

	t.Run("explain analyze on a seq scan returns hot nodes and self time", func(t *testing.T) {
		ctx := context.Background()
		store := testStore(t)
		seedExplainFixture(t, ctx)

		result, err := store.Explain(ctx, diagnostics.ExplainParams{
			SQL:     "SELECT count(*) FROM pgmcp_test.explain_t WHERE n % 2 = 0",
			Analyze: true,
			Buffers: true,
		})

		require.NoError(t, err)
		assert.Greater(t, result.ExecutionMs, float64(0))
		assert.Greater(t, result.PlanningMs, float64(0))
		require.NotEmpty(t, result.HotNodes)
		assert.Greater(t, result.HotNodes[0].SelfMs, float64(0))
		assert.Greater(t, result.HotNodes[0].PctOfTotal, float64(0))

		scan := findSeqScan(t, &result.Plan)
		assert.Equal(t, "explain_t", scan.Relation)
		assert.Equal(t, float64(10000), scan.ActualRows)
		assert.Greater(t, scan.SharedHit+scan.SharedRead, int64(0))
	})

	t.Run("explain rejects an update even when asked", func(t *testing.T) {
		ctx := context.Background()
		store := testStore(t)
		seedExplainFixture(t, ctx)
		countBefore, sumBefore := explainFixtureChecksum(t, ctx)

		_, err := store.Explain(ctx, diagnostics.ExplainParams{
			SQL:     "UPDATE pgmcp_test.explain_t SET n = n + 1",
			Analyze: true,
		})

		require.Error(t, err)
		var rejection *sqlguard.Rejection
		require.ErrorAs(t, err, &rejection)
		assert.Equal(t, sqlguard.ReasonKind, rejection.Reason)

		countAfter, sumAfter := explainFixtureChecksum(t, ctx)
		assert.Equal(t, countBefore, countAfter)
		assert.Equal(t, sumBefore, sumAfter)
	})
}

// findSeqScan returns the first sequential scan node in the plan tree.
func findSeqScan(t *testing.T, node *diagnostics.PlanNode) diagnostics.PlanNode {
	t.Helper()

	if found, ok := seqScan(node); ok {
		return found
	}
	t.Fatal("plan contains no sequential scan")

	return diagnostics.PlanNode{}
}

// seqScan walks the plan tree depth first looking for a sequential scan.
func seqScan(node *diagnostics.PlanNode) (diagnostics.PlanNode, bool) {
	if node.NodeType == "Seq Scan" {
		return *node, true
	}
	for i := range node.Children {
		if found, ok := seqScan(&node.Children[i]); ok {
			return found, true
		}
	}

	return diagnostics.PlanNode{}, false
}
