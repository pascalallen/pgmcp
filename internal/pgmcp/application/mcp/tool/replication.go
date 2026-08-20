package tool

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// ReplicationIn is the input of the replication tool, which takes no arguments.
type ReplicationIn struct{}

// ReplicationOut is the output of the replication tool.
type ReplicationOut struct {
	Meta
	IsPrimary            bool                  `json:"is_primary" jsonschema:"true when this server is a primary, false when it is a standby replaying WAL"`
	Standbys             []diagnostics.Standby `json:"standbys" jsonschema:"connected standbys with their send, write, flush and replay lag in bytes and milliseconds"`
	Slots                []diagnostics.Slot    `json:"slots" jsonschema:"replication slots and the WAL each one retains; an inactive slot pins WAL on disk forever"`
	WALRateBytesPerSec   float64               `json:"wal_rate_bytes_s" jsonschema:"current WAL generation rate in bytes per second"`
	ReplayLagMsOnStandby float64               `json:"replay_lag_ms,omitempty" jsonschema:"how far behind the primary this server is replaying; reported only on a standby"`
}

// Replication reports the replication topology, lag and slots.
func Replication(d diagnostics.Diagnostics) (*mcp.Tool, mcp.ToolHandlerFor[ReplicationIn, ReplicationOut]) {
	tool := &mcp.Tool{
		Name:        "replication",
		Description: "Reports whether this server is a primary or a standby, how far behind every connected standby is in bytes and milliseconds, the replication slots and the WAL each retains, and the current WAL rate. Use it for replication lag questions, failover readiness, or to find an inactive slot filling the disk with retained WAL.",
		Annotations: readOnly("Replication"),
	}

	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ ReplicationIn) (*mcp.CallToolResult, ReplicationOut, error) {
		result, err := d.Replication(ctx)
		if err != nil {
			return nil, ReplicationOut{}, err
		}
		if result == nil {
			result = &diagnostics.ReplicationResult{}
		}

		out := ReplicationOut{
			Meta:                 newMeta(ctx, d),
			IsPrimary:            result.IsPrimary,
			Standbys:             result.Standbys,
			Slots:                result.Slots,
			WALRateBytesPerSec:   result.WALRateBytesPerSec,
			ReplayLagMsOnStandby: result.ReplayLagMsOnStandby,
		}
		if out.Standbys == nil {
			out.Standbys = []diagnostics.Standby{}
		}
		if out.Slots == nil {
			out.Slots = []diagnostics.Slot{}
		}

		return nil, out, nil
	}

	return tool, handler
}
