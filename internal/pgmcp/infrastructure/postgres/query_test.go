package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreQuery(t *testing.T) {
	t.Run("query returns columns rows and duration", func(t *testing.T) {
		store := testStore(t)

		result, err := store.Query(context.Background(), diagnostics.QueryParams{
			SQL: "SELECT 42::int AS n, 'abc'::text AS s, true AS b, 1.5::float8 AS f, NULL::int AS z, now() AS t",
		})

		require.NoError(t, err)
		require.Len(t, result.Columns, 6)
		assert.Equal(t, []string{"n", "s", "b", "f", "z", "t"}, columnNames(result.Columns))
		assert.Equal(t, "INT4", result.Columns[0].Type)
		assert.Equal(t, "TEXT", result.Columns[1].Type)
		for _, column := range result.Columns {
			assert.NotEmpty(t, column.Type, "column %q has no database type", column.Name)
		}

		require.Len(t, result.Rows, 1)
		assert.Equal(t, 1, result.RowCount)
		assert.False(t, result.Truncated)

		row := result.Rows[0]
		require.Len(t, row, 6)
		assert.Equal(t, int64(42), row[0])
		assert.Equal(t, "abc", row[1], "text arrives as []byte and must be normalised to a string")
		assert.Equal(t, true, row[2])
		assert.InDelta(t, 1.5, row[3], 0.0001)
		assert.Nil(t, row[4], "SQL NULL stays nil")
		assert.IsType(t, time.Time{}, row[5])

		assert.Greater(t, result.DurationMs, float64(0))
	})

	t.Run("query truncates at max_rows and says so", func(t *testing.T) {
		store := testStore(t)

		result, err := store.Query(context.Background(), diagnostics.QueryParams{
			SQL:     "SELECT generate_series(1, 10) AS n",
			MaxRows: 3,
		})

		require.NoError(t, err)
		require.Len(t, result.Rows, 3)
		assert.Equal(t, 3, result.RowCount)
		assert.True(t, result.Truncated)
		assert.Equal(t, int64(1), result.Rows[0][0])
		assert.Equal(t, int64(3), result.Rows[2][0])
	})

	t.Run("query does not claim truncation when the result exactly fills max_rows", func(t *testing.T) {
		store := testStore(t)

		result, err := store.Query(context.Background(), diagnostics.QueryParams{
			SQL:     "SELECT generate_series(1, 3) AS n",
			MaxRows: 3,
		})

		require.NoError(t, err)
		require.Len(t, result.Rows, 3)
		assert.False(t, result.Truncated)
	})

	t.Run("query defaults to 500 rows and clamps an oversized max_rows", func(t *testing.T) {
		store := testStore(t)

		defaulted, err := store.Query(context.Background(), diagnostics.QueryParams{
			SQL: "SELECT generate_series(1, 600) AS n",
		})

		require.NoError(t, err)
		assert.Len(t, defaulted.Rows, 500)
		assert.True(t, defaulted.Truncated)

		clamped, err := store.Query(context.Background(), diagnostics.QueryParams{
			SQL:     "SELECT generate_series(1, 10) AS n",
			MaxRows: 1_000_000,
		})

		require.NoError(t, err)
		assert.Len(t, clamped.Rows, 10)
		assert.False(t, clamped.Truncated)
	})

	t.Run("query returns an empty non-nil result for a no-row query", func(t *testing.T) {
		store := testStore(t)

		result, err := store.Query(context.Background(), diagnostics.QueryParams{
			SQL: "SELECT 1 AS n WHERE false",
		})

		require.NoError(t, err)
		require.NotNil(t, result.Rows)
		require.NotNil(t, result.Columns)
		assert.Empty(t, result.Rows)
		assert.Equal(t, 0, result.RowCount)
		assert.False(t, result.Truncated)
	})

	t.Run("query binds positional params", func(t *testing.T) {
		store := testStore(t)

		result, err := store.Query(context.Background(), diagnostics.QueryParams{
			SQL:    "SELECT $1::int + 1 AS n",
			Params: []any{41},
		})

		require.NoError(t, err)
		require.Len(t, result.Rows, 1)
		assert.Equal(t, int64(42), result.Rows[0][0])
	})

	t.Run("query rejects a write with ReadOnlyViolation", func(t *testing.T) {
		store := testStore(t)
		db := rawDB(t)
		seedFixtures(t, db)
		ctx := context.Background()

		var before int64
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM pgmcp_test.ro_probe WHERE val = 'original'").Scan(&before))
		require.Positive(t, before)

		_, err := store.Query(ctx, diagnostics.QueryParams{SQL: "UPDATE pgmcp_test.ro_probe SET val = 'x'"})

		var violation *diagnostics.ReadOnlyViolation
		require.ErrorAs(t, err, &violation)

		var after int64
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM pgmcp_test.ro_probe WHERE val = 'original'").Scan(&after))
		assert.Equal(t, before, after, "the rejected write must not have changed a single row")
	})

	t.Run("query honours timeout", func(t *testing.T) {
		store := testStore(t)

		_, err := store.Query(context.Background(), diagnostics.QueryParams{
			SQL:     "SELECT pg_sleep(5)",
			Timeout: 150 * time.Millisecond,
		})

		require.Error(t, err)
		assert.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
	})
}

// columnNames collects the column names of a query result in order.
func columnNames(columns []diagnostics.Column) []string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.Name)
	}

	return names
}
