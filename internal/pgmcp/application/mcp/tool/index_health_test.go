package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func TestIndexHealth(t *testing.T) {
	ctx := context.Background()

	t.Run("index health includes bloat and every schema by default", func(t *testing.T) {
		var received diagnostics.IndexHealthParams

		_, handler := IndexHealth(fakeDiag{indexHealth: func(_ context.Context, p diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
			received = p

			return &diagnostics.IndexHealthResult{}, nil
		}})

		_, _, err := handler(ctx, nil, IndexHealthIn{})
		require.NoError(t, err)

		assert.Empty(t, received.Schema, "an absent schema means every schema")
		assert.True(t, received.IncludeBloat, "bloat estimation defaults to on")
	})

	t.Run("index health passes an explicit schema through and honours bloat turned off", func(t *testing.T) {
		var received diagnostics.IndexHealthParams

		_, handler := IndexHealth(fakeDiag{indexHealth: func(_ context.Context, p diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
			received = p

			return &diagnostics.IndexHealthResult{}, nil
		}})

		_, _, err := handler(ctx, nil, IndexHealthIn{Schema: "sales", IncludeBloat: ptr(false)})
		require.NoError(t, err)

		assert.Equal(t, "sales", received.Schema)
		assert.False(t, received.IncludeBloat)
	})

	t.Run("index health returns each group of findings with the meta block", func(t *testing.T) {
		_, handler := IndexHealth(fakeDiag{indexHealth: func(context.Context, diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
			return &diagnostics.IndexHealthResult{
				Unused: []diagnostics.IndexFinding{{
					Schema: "public", Table: "orders", Index: "orders_stale_idx",
					SizeBytes: 4096, Scans: 0, DropCandidateSQL: `DROP INDEX CONCURRENTLY "public"."orders_stale_idx";`,
				}},
				Duplicate: []diagnostics.IndexFinding{{Schema: "public", Table: "orders", Index: "orders_dup_idx", DuplicateOf: "orders_pkey"}},
				Invalid:   []diagnostics.IndexFinding{{Schema: "public", Table: "orders", Index: "orders_broken_idx"}},
				Bloated:   []diagnostics.IndexFinding{{Schema: "public", Table: "orders", Index: "orders_fat_idx", BloatRatio: 0.7, BloatBytes: 1 << 20}},
			}, nil
		}})

		_, out, err := handler(ctx, nil, IndexHealthIn{})
		require.NoError(t, err)

		require.Len(t, out.Unused, 1)
		assert.Equal(t, "orders_stale_idx", out.Unused[0].Index)
		require.Len(t, out.Duplicate, 1)
		assert.Equal(t, "orders_pkey", out.Duplicate[0].DuplicateOf)
		require.Len(t, out.Invalid, 1)
		require.Len(t, out.Bloated, 1)
		assert.Equal(t, 0.7, out.Bloated[0].BloatRatio)
		assert.Equal(t, "16.4", out.ServerVersion)
		assert.False(t, out.GeneratedAt.IsZero())
	})

	t.Run("index health reports empty groups rather than null", func(t *testing.T) {
		_, handler := IndexHealth(fakeDiag{indexHealth: func(context.Context, diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
			return &diagnostics.IndexHealthResult{}, nil
		}})

		_, out, err := handler(ctx, nil, IndexHealthIn{})
		require.NoError(t, err)

		for name, group := range map[string][]diagnostics.IndexFinding{
			"unused": out.Unused, "duplicate": out.Duplicate, "invalid": out.Invalid, "bloated": out.Bloated,
		} {
			assert.NotNilf(t, group, "%s must be an empty list, never null", name)
			assert.Empty(t, group)
		}
	})

	t.Run("index health treats a nil result as an empty report", func(t *testing.T) {
		_, handler := IndexHealth(fakeDiag{indexHealth: func(context.Context, diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
			return nil, nil
		}})

		_, out, err := handler(ctx, nil, IndexHealthIn{})
		require.NoError(t, err)

		assert.NotNil(t, out.Unused)
		assert.NotNil(t, out.Duplicate)
		assert.NotNil(t, out.Invalid)
		assert.NotNil(t, out.Bloated)
	})

	t.Run("index health propagates a port failure", func(t *testing.T) {
		failure := errors.New("pg_stat_user_indexes unavailable")

		_, handler := IndexHealth(fakeDiag{indexHealth: func(context.Context, diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
			return nil, failure
		}})

		_, _, err := handler(ctx, nil, IndexHealthIn{})
		require.ErrorIs(t, err, failure)
	})

	t.Run("index health answers over an mcp session and lists itself as read only", func(t *testing.T) {
		definition, handler := IndexHealth(fakeDiag{indexHealth: func(context.Context, diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
			return &diagnostics.IndexHealthResult{
				Unused: []diagnostics.IndexFinding{{Schema: "public", Table: "orders", Index: "orders_stale_idx", SizeBytes: 8192}},
			}, nil
		}})

		session := serveTool(t, definition, handler)

		out := callStructured[IndexHealthOut](t, session, "index_health", IndexHealthIn{Schema: "public"})
		require.Len(t, out.Unused, 1)
		assert.Equal(t, "orders_stale_idx", out.Unused[0].Index)
		assert.Equal(t, int64(8192), out.Unused[0].SizeBytes)
		assert.Equal(t, "pgmcp", out.Database)
		assert.NotNil(t, out.Bloated)

		requireReadOnlyListing(t, session, "index_health", "Index health")
	})
}
