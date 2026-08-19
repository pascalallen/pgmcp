package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func TestReplication(t *testing.T) {
	ctx := context.Background()

	t.Run("replication returns the topology lag and slots with the meta block", func(t *testing.T) {
		_, handler := Replication(fakeDiag{replication: func(context.Context) (*diagnostics.ReplicationResult, error) {
			return &diagnostics.ReplicationResult{
				IsPrimary: true,
				Standbys: []diagnostics.Standby{{
					Client: "10.0.0.2", ApplicationName: "replica1", State: "streaming", SyncState: "async",
					ReplayLagBytes: 4096, ReplayLagMs: 12.5,
				}},
				Slots:              []diagnostics.Slot{{Name: "replica1", Type: "physical", Active: true, RetainedWALBytes: 8192}},
				WALRateBytesPerSec: 1024,
			}, nil
		}})

		_, out, err := handler(ctx, nil, ReplicationIn{})
		require.NoError(t, err)

		assert.True(t, out.IsPrimary)
		require.Len(t, out.Standbys, 1)
		assert.Equal(t, "streaming", out.Standbys[0].State)
		assert.Equal(t, 12.5, out.Standbys[0].ReplayLagMs)
		require.Len(t, out.Slots, 1)
		assert.Equal(t, int64(8192), out.Slots[0].RetainedWALBytes)
		assert.Equal(t, 1024.0, out.WALRateBytesPerSec)
		assert.Equal(t, "16.4", out.ServerVersion)
		assert.False(t, out.GeneratedAt.IsZero())
	})

	t.Run("replication reports a standby with its own replay lag", func(t *testing.T) {
		_, handler := Replication(fakeDiag{replication: func(context.Context) (*diagnostics.ReplicationResult, error) {
			return &diagnostics.ReplicationResult{IsPrimary: false, ReplayLagMsOnStandby: 250}, nil
		}})

		_, out, err := handler(ctx, nil, ReplicationIn{})
		require.NoError(t, err)

		assert.False(t, out.IsPrimary)
		assert.Equal(t, 250.0, out.ReplayLagMsOnStandby)
		assert.NotNil(t, out.Standbys)
		assert.NotNil(t, out.Slots)
		assert.Empty(t, out.Standbys)
		assert.Empty(t, out.Slots)
	})

	t.Run("replication treats a nil result as a server with nothing to report", func(t *testing.T) {
		_, handler := Replication(fakeDiag{replication: func(context.Context) (*diagnostics.ReplicationResult, error) {
			return nil, nil
		}})

		_, out, err := handler(ctx, nil, ReplicationIn{})
		require.NoError(t, err)
		assert.NotNil(t, out.Standbys)
		assert.NotNil(t, out.Slots)
	})

	t.Run("replication propagates a port failure", func(t *testing.T) {
		failure := errors.New("pg_stat_replication unavailable")

		_, handler := Replication(fakeDiag{replication: func(context.Context) (*diagnostics.ReplicationResult, error) {
			return nil, failure
		}})

		_, _, err := handler(ctx, nil, ReplicationIn{})
		require.ErrorIs(t, err, failure)
	})

	t.Run("replication answers over an mcp session and lists itself as read only", func(t *testing.T) {
		definition, handler := Replication(fakeDiag{replication: func(context.Context) (*diagnostics.ReplicationResult, error) {
			return &diagnostics.ReplicationResult{
				IsPrimary:          true,
				Standbys:           []diagnostics.Standby{{ApplicationName: "replica1", State: "streaming", FlushLagMs: 3}},
				Slots:              []diagnostics.Slot{{Name: "orphan", Type: "logical", Active: false, RetainedWALBytes: 1 << 30}},
				WALRateBytesPerSec: 2048,
			}, nil
		}})

		session := serveTool(t, definition, handler)

		out := callStructured[ReplicationOut](t, session, "replication", ReplicationIn{})
		assert.True(t, out.IsPrimary)
		require.Len(t, out.Standbys, 1)
		require.Len(t, out.Slots, 1)
		assert.False(t, out.Slots[0].Active)
		assert.Equal(t, 2048.0, out.WALRateBytesPerSec)

		requireReadOnlyListing(t, session, "replication", "Replication")
	})
}
