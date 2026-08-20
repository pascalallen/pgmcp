package tool

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func TestTableHealth(t *testing.T) {
	ctx := context.Background()

	t.Run("table health defaults the size floor and reports every schema", func(t *testing.T) {
		var received diagnostics.TableHealthParams

		_, handler := TableHealth(fakeDiag{tableHealth: func(_ context.Context, p diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
			received = p

			return nil, nil
		}})

		_, _, err := handler(ctx, nil, TableHealthIn{})
		require.NoError(t, err)

		assert.Empty(t, received.Schema)
		assert.Equal(t, int64(defaultTableHealthMinSizeMB), received.MinSizeMB)
	})

	t.Run("table health passes an explicit schema and size floor through", func(t *testing.T) {
		var received diagnostics.TableHealthParams

		_, handler := TableHealth(fakeDiag{tableHealth: func(_ context.Context, p diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
			received = p

			return nil, nil
		}})

		_, _, err := handler(ctx, nil, TableHealthIn{Schema: "sales", MinSizeMB: 250})
		require.NoError(t, err)

		assert.Equal(t, "sales", received.Schema)
		assert.Equal(t, int64(250), received.MinSizeMB)
	})

	t.Run("table health treats a negative size floor as no floor at all", func(t *testing.T) {
		var received diagnostics.TableHealthParams

		_, handler := TableHealth(fakeDiag{tableHealth: func(_ context.Context, p diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
			received = p

			return nil, nil
		}})

		_, _, err := handler(ctx, nil, TableHealthIn{MinSizeMB: -5})
		require.NoError(t, err)
		assert.Equal(t, int64(0), received.MinSizeMB)
	})

	t.Run("table health caps an enormous size floor so it cannot overflow the byte conversion", func(t *testing.T) {
		var received diagnostics.TableHealthParams

		_, handler := TableHealth(fakeDiag{tableHealth: func(_ context.Context, p diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
			received = p

			return nil, nil
		}})

		_, _, err := handler(ctx, nil, TableHealthIn{MinSizeMB: math.MaxInt64})
		require.NoError(t, err)

		assert.Equal(t, int64(maxTableHealthMinSizeMB), received.MinSizeMB)
		assert.Positive(t, received.MinSizeMB*1024*1024, "the capped floor must still convert to a positive byte count")
	})

	t.Run("table health returns the findings with the meta block", func(t *testing.T) {
		vacuumed := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)

		_, handler := TableHealth(fakeDiag{tableHealth: func(context.Context, diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
			return []diagnostics.TableFinding{{
				Schema: "public", Table: "orders", SizeBytes: 1 << 30,
				LiveTuples: 1_000_000, DeadTuples: 400_000, DeadRatio: 0.4,
				LastAutovacuum: &vacuumed, SeqScans: 900, IdxScans: 10,
				BloatRatio: 0.35, BloatBytes: 1 << 28,
				Flags: []string{"high_dead_ratio", "seq_scan_heavy"},
			}}, nil
		}})

		_, out, err := handler(ctx, nil, TableHealthIn{})
		require.NoError(t, err)

		require.Len(t, out.Tables, 1)
		assert.Equal(t, "orders", out.Tables[0].Table)
		assert.Equal(t, 0.4, out.Tables[0].DeadRatio)
		assert.Equal(t, []string{"high_dead_ratio", "seq_scan_heavy"}, out.Tables[0].Flags)
		require.NotNil(t, out.Tables[0].LastAutovacuum)
		assert.Equal(t, "16.4", out.ServerVersion)
		assert.False(t, out.GeneratedAt.IsZero())
	})

	t.Run("table health reports an empty list and empty flags rather than null", func(t *testing.T) {
		_, handler := TableHealth(fakeDiag{tableHealth: func(context.Context, diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
			return []diagnostics.TableFinding{{Schema: "public", Table: "quiet"}}, nil
		}})

		_, out, err := handler(ctx, nil, TableHealthIn{})
		require.NoError(t, err)

		require.Len(t, out.Tables, 1)
		assert.NotNil(t, out.Tables[0].Flags)
		assert.Empty(t, out.Tables[0].Flags)
	})

	t.Run("table health treats a nil result as an empty report", func(t *testing.T) {
		_, handler := TableHealth(fakeDiag{tableHealth: func(context.Context, diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
			return nil, nil
		}})

		_, out, err := handler(ctx, nil, TableHealthIn{})
		require.NoError(t, err)

		assert.NotNil(t, out.Tables)
		assert.Empty(t, out.Tables)
	})

	t.Run("table health propagates a port failure", func(t *testing.T) {
		failure := errors.New("pg_stat_user_tables unavailable")

		_, handler := TableHealth(fakeDiag{tableHealth: func(context.Context, diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
			return nil, failure
		}})

		_, _, err := handler(ctx, nil, TableHealthIn{})
		require.ErrorIs(t, err, failure)
	})

	t.Run("table health answers over an mcp session and lists itself as read only", func(t *testing.T) {
		definition, handler := TableHealth(fakeDiag{tableHealth: func(context.Context, diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
			return []diagnostics.TableFinding{{
				Schema: "public", Table: "orders", SizeBytes: 1 << 20, DeadRatio: 0.2, Flags: []string{"never_analyzed"},
			}}, nil
		}})

		session := serveTool(t, definition, handler)

		out := callStructured[TableHealthOut](t, session, "table_health", TableHealthIn{Schema: "public", MinSizeMB: 10})
		require.Len(t, out.Tables, 1)
		assert.Equal(t, "orders", out.Tables[0].Table)
		assert.Equal(t, []string{"never_analyzed"}, out.Tables[0].Flags)
		assert.Equal(t, "pgmcp", out.Database)

		requireReadOnlyListing(t, session, "table_health", "Table health")
	})
}
