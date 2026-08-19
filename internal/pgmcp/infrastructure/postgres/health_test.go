package postgres

import (
	"context"
	"testing"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreIndexHealth(t *testing.T) {
	t.Run("index health lists the unused and duplicate fixture indexes", func(t *testing.T) {
		store := testStore(t)
		seedFixtures(t, rawDB(t))

		result, err := store.IndexHealth(context.Background(), diagnostics.IndexHealthParams{Schema: "pgmcp_test"})

		require.NoError(t, err)
		require.NotNil(t, result.Unused)
		require.NotNil(t, result.Duplicate)
		require.NotNil(t, result.Invalid)
		require.NotNil(t, result.Bloated)
		assert.Empty(t, result.Bloated, "bloat is only estimated when the caller asks for it")

		unused := indexNames(result.Unused)
		assert.Contains(t, unused, "bloaty_pad_idx")
		assert.Contains(t, unused, "bloaty_pad_idx2")

		for _, finding := range result.Unused {
			assert.Equal(t, "pgmcp_test", finding.Schema)
			assert.Equal(t, "bloaty", finding.Table)
			assert.Zero(t, finding.Scans)
			assert.Greater(t, finding.SizeBytes, int64(0))
			assert.Contains(t, finding.Definition, "CREATE INDEX")
			assert.Equal(t,
				`DROP INDEX CONCURRENTLY "pgmcp_test"."`+finding.Index+`";`,
				finding.DropCandidateSQL,
			)
		}

		require.Len(t, result.Duplicate, 1)
		duplicate := result.Duplicate[0]
		assert.Equal(t, "pgmcp_test", duplicate.Schema)
		assert.Equal(t, "bloaty", duplicate.Table)
		assert.Equal(t, "bloaty_pad_idx2", duplicate.Index)
		assert.Equal(t, "bloaty_pad_idx", duplicate.DuplicateOf)
		assert.Greater(t, duplicate.SizeBytes, int64(0))
		assert.Contains(t, duplicate.Definition, "bloaty_pad_idx2")
	})

	t.Run("index health estimates bloat only when the caller asks for it", func(t *testing.T) {
		store := testStore(t)
		seedFixtures(t, rawDB(t))

		result, err := store.IndexHealth(context.Background(), diagnostics.IndexHealthParams{
			Schema:       "pgmcp_test",
			IncludeBloat: true,
		})

		require.NoError(t, err)
		require.NotEmpty(t, result.Bloated, "the fixture index keeps the pages of 10000 deleted rows")

		bloated := result.Bloated[0]
		assert.Equal(t, "pgmcp_test", bloated.Schema)
		assert.Equal(t, "bloaty", bloated.Table)
		assert.Contains(t, bloated.Index, "bloaty_pad_idx")
		assert.GreaterOrEqual(t, bloated.SizeBytes, int64(1<<20))
		assert.GreaterOrEqual(t, bloated.BloatRatio, 0.3)
		assert.Greater(t, bloated.BloatBytes, int64(0))
	})

	t.Run("index health reports an index a failed concurrent build left invalid", func(t *testing.T) {
		store := testStore(t)
		seedFixtures(t, rawDB(t))

		result, err := store.IndexHealth(context.Background(), diagnostics.IndexHealthParams{Schema: "pgmcp_test"})

		require.NoError(t, err)
		require.Len(t, result.Invalid, 1)

		invalid := result.Invalid[0]
		assert.Equal(t, "pgmcp_test", invalid.Schema)
		assert.Equal(t, "inv_t", invalid.Table)
		assert.Equal(t, "inv_t_a_uidx", invalid.Index)
		assert.Zero(t, invalid.Scans)
		assert.Contains(t, invalid.Definition, "CREATE UNIQUE INDEX inv_t_a_uidx")
	})

	t.Run("index health scopes its findings to the requested schema", func(t *testing.T) {
		store := testStore(t)
		seedFixtures(t, rawDB(t))

		result, err := store.IndexHealth(context.Background(), diagnostics.IndexHealthParams{Schema: "pg_toast"})

		require.NoError(t, err)
		assert.Empty(t, result.Unused)
		assert.Empty(t, result.Duplicate)
	})
}

func TestStoreTableHealth(t *testing.T) {
	t.Run("table health flags the bloaty table with high_dead_ratio", func(t *testing.T) {
		store := testStore(t)
		seedFixtures(t, rawDB(t))

		findings, err := store.TableHealth(context.Background(), diagnostics.TableHealthParams{Schema: "pgmcp_test"})

		require.NoError(t, err)
		require.NotEmpty(t, findings)

		var bloaty *diagnostics.TableFinding
		for i := range findings {
			require.NotNil(t, findings[i].Flags)
			if findings[i].Table == "bloaty" {
				bloaty = &findings[i]
			}
		}
		require.NotNil(t, bloaty, "the seeded bloaty table is missing from the findings")

		assert.Equal(t, "pgmcp_test", bloaty.Schema)
		assert.Greater(t, bloaty.SizeBytes, int64(0))
		assert.GreaterOrEqual(t, bloaty.LiveTuples, int64(1000))
		assert.GreaterOrEqual(t, bloaty.DeadTuples, int64(1000))
		assert.GreaterOrEqual(t, bloaty.DeadRatio, 0.1)
		assert.Contains(t, bloaty.Flags, "high_dead_ratio")
		assert.Contains(t, bloaty.Flags, "bloated")
		assert.Greater(t, bloaty.BloatRatio, 0.0)
		assert.Greater(t, bloaty.BloatBytes, int64(0))
	})

	t.Run("table health drops tables below the minimum size", func(t *testing.T) {
		store := testStore(t)
		seedFixtures(t, rawDB(t))

		findings, err := store.TableHealth(context.Background(), diagnostics.TableHealthParams{
			Schema:    "pgmcp_test",
			MinSizeMB: 1024,
		})

		require.NoError(t, err)
		assert.NotNil(t, findings)
		assert.Empty(t, findings)
	})
}

// indexNames collects the index names of findings, for order-insensitive
// membership assertions.
func indexNames(findings []diagnostics.IndexFinding) []string {
	names := make([]string, 0, len(findings))
	for _, finding := range findings {
		names = append(names, finding.Index)
	}

	return names
}
