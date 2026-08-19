package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// walStatsMinVersion is the first server version with the pg_stat_wal view.
const walStatsMinVersion = 140000

// replicationRoleQuery reports whether this server is a standby, and the
// version that decides which statistics views exist.
const replicationRoleQuery = `SELECT pg_is_in_recovery(), current_setting('server_version_num')::int`

// standbysQuery reports one row per connected standby, with lag measured both
// in bytes of WAL and in wall-clock milliseconds.
const standbysQuery = `
SELECT coalesce(client_addr::text, 'local'),
       application_name,
       state,
       sync_state,
       pg_wal_lsn_diff(pg_current_wal_lsn(), sent_lsn)::bigint,
       pg_wal_lsn_diff(pg_current_wal_lsn(), write_lsn)::bigint,
       pg_wal_lsn_diff(pg_current_wal_lsn(), flush_lsn)::bigint,
       pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn)::bigint,
       EXTRACT(EPOCH FROM write_lag) * 1000,
       EXTRACT(EPOCH FROM flush_lag) * 1000,
       EXTRACT(EPOCH FROM replay_lag) * 1000
FROM pg_stat_replication`

// slotsQuery reports the replication slots and how much WAL each one pins.
const slotsQuery = `
SELECT slot_name,
       slot_type,
       active,
       coalesce(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn), 0)::bigint
FROM pg_replication_slots`

// walRateQuery estimates the bytes of WAL written per second since the WAL
// statistics were last reset.
const walRateQuery = `SELECT wal_bytes / GREATEST(EXTRACT(EPOCH FROM now() - stats_reset), 1) FROM pg_stat_wal`

// standbyReplayLagQuery measures how far behind this standby is replaying.
const standbyReplayLagQuery = `SELECT EXTRACT(EPOCH FROM now() - pg_last_xact_replay_timestamp()) * 1000`

// Replication reports the server's replication role: on a primary its
// standbys, slots and WAL rate; on a standby how far behind it is replaying.
func (s *Store) Replication(ctx context.Context) (*diagnostics.ReplicationResult, error) {
	result := &diagnostics.ReplicationResult{
		Standbys: make([]diagnostics.Standby, 0),
		Slots:    make([]diagnostics.Slot, 0),
	}

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		var inRecovery bool
		var versionNum int
		if err := tx.QueryRowContext(ctx, replicationRoleQuery).Scan(&inRecovery, &versionNum); err != nil {
			return fmt.Errorf("postgres: replication role: %w", err)
		}
		result.IsPrimary = !inRecovery

		if !result.IsPrimary {
			var replayLagMs sql.NullFloat64
			if err := tx.QueryRowContext(ctx, standbyReplayLagQuery).Scan(&replayLagMs); err != nil {
				return fmt.Errorf("postgres: standby replay lag: %w", err)
			}
			result.ReplayLagMsOnStandby = replayLagMs.Float64
			return nil
		}

		if err := scanStandbys(ctx, tx, result); err != nil {
			return err
		}
		if err := scanSlots(ctx, tx, result); err != nil {
			return err
		}

		// pg_stat_wal only exists from PostgreSQL 14 on; older servers
		// simply report no WAL rate.
		if versionNum >= walStatsMinVersion {
			var walRate sql.NullFloat64
			if err := tx.QueryRowContext(ctx, walRateQuery).Scan(&walRate); err != nil {
				return fmt.Errorf("postgres: wal rate: %w", err)
			}
			result.WALRateBytesPerSec = walRate.Float64
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return result, nil
}

// scanStandbys appends one Standby per row of pg_stat_replication.
func scanStandbys(ctx context.Context, tx *sql.Tx, result *diagnostics.ReplicationResult) error {
	rows, err := tx.QueryContext(ctx, standbysQuery)
	if err != nil {
		return fmt.Errorf("postgres: standbys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			standby         diagnostics.Standby
			client          sql.NullString
			applicationName sql.NullString
			state           sql.NullString
			syncState       sql.NullString
			sentLag         sql.NullInt64
			writeLag        sql.NullInt64
			flushLag        sql.NullInt64
			replayLag       sql.NullInt64
			writeLagMs      sql.NullFloat64
			flushLagMs      sql.NullFloat64
			replayLagMs     sql.NullFloat64
		)
		if err := rows.Scan(
			&client, &applicationName, &state, &syncState,
			&sentLag, &writeLag, &flushLag, &replayLag,
			&writeLagMs, &flushLagMs, &replayLagMs,
		); err != nil {
			return fmt.Errorf("postgres: scan standby: %w", err)
		}

		standby.Client = client.String
		standby.ApplicationName = applicationName.String
		standby.State = state.String
		standby.SyncState = syncState.String
		standby.SentLagBytes = sentLag.Int64
		standby.WriteLagBytes = writeLag.Int64
		standby.FlushLagBytes = flushLag.Int64
		standby.ReplayLagBytes = replayLag.Int64
		standby.WriteLagMs = writeLagMs.Float64
		standby.FlushLagMs = flushLagMs.Float64
		standby.ReplayLagMs = replayLagMs.Float64

		result.Standbys = append(result.Standbys, standby)
	}

	return rows.Err()
}

// scanSlots appends one Slot per row of pg_replication_slots.
func scanSlots(ctx context.Context, tx *sql.Tx, result *diagnostics.ReplicationResult) error {
	rows, err := tx.QueryContext(ctx, slotsQuery)
	if err != nil {
		return fmt.Errorf("postgres: replication slots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			slot     diagnostics.Slot
			name     sql.NullString
			slotType sql.NullString
			active   sql.NullBool
			retained sql.NullInt64
		)
		if err := rows.Scan(&name, &slotType, &active, &retained); err != nil {
			return fmt.Errorf("postgres: scan replication slot: %w", err)
		}

		slot.Name = name.String
		slot.Type = slotType.String
		slot.Active = active.Bool
		slot.RetainedWALBytes = retained.Int64

		result.Slots = append(result.Slots, slot)
	}

	return rows.Err()
}
