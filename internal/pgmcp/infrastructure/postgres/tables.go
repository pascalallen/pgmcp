package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// tableHealthQuery reports the vacuum, analyze and scan counters the flags
// below are derived from, largest table first.
const tableHealthQuery = `
SELECT schemaname,
       relname,
       pg_total_relation_size(relid),
       n_live_tup,
       n_dead_tup,
       CASE WHEN n_live_tup + n_dead_tup = 0 THEN 0
            ELSE n_dead_tup::float / (n_live_tup + n_dead_tup)
       END,
       last_vacuum,
       last_autovacuum,
       last_analyze,
       last_autoanalyze,
       seq_scan,
       idx_scan
FROM pg_stat_user_tables
WHERE pg_total_relation_size(relid) >= $1
  AND ($2 = '' OR schemaname = $2)
ORDER BY 3 DESC
LIMIT 200`

// Thresholds for the health flags. They are deliberately blunt: the point is
// to surface a table worth looking at, not to grade it.
const (
	deadRatioThreshold    = 0.1
	deadTuplesThreshold   = 1000
	liveTuplesThreshold   = 1000
	seqScanRatio          = 10
	seqScanThreshold      = 100
	seqScanSizeThreshold  = 10 << 20
	bloatRatioThreshold   = 0.3
	maxTableHealthResults = 200
)

// TableHealth reports the largest tables in the requested schema together with
// their dead tuple ratio, vacuum and analyze recency, scan mix and estimated
// bloat, each summarised as a set of flags.
func (s *Store) TableHealth(ctx context.Context, p diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
	minSizeBytes := p.MinSizeMB * 1024 * 1024
	if minSizeBytes < 0 {
		minSizeBytes = 0
	}

	findings := make([]diagnostics.TableFinding, 0, maxTableHealthResults)

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, tableHealthQuery, minSizeBytes, p.Schema)
		if err != nil {
			return fmt.Errorf("postgres: table health: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var (
				finding     diagnostics.TableFinding
				size        sql.NullInt64
				live        sql.NullInt64
				dead        sql.NullInt64
				deadRatio   sql.NullFloat64
				seqScans    sql.NullInt64
				idxScans    sql.NullInt64
				vacuum      sql.NullTime
				autovacuum  sql.NullTime
				analyze     sql.NullTime
				autoanalyze sql.NullTime
			)
			if err := rows.Scan(
				&finding.Schema, &finding.Table, &size, &live, &dead, &deadRatio,
				&vacuum, &autovacuum, &analyze, &autoanalyze, &seqScans, &idxScans,
			); err != nil {
				return fmt.Errorf("postgres: scan table health: %w", err)
			}

			finding.SizeBytes = size.Int64
			finding.LiveTuples = live.Int64
			finding.DeadTuples = dead.Int64
			finding.DeadRatio = deadRatio.Float64
			finding.SeqScans = seqScans.Int64
			finding.IdxScans = idxScans.Int64
			finding.LastVacuum = nullableTime(vacuum)
			finding.LastAutovacuum = nullableTime(autovacuum)
			finding.LastAnalyze = nullableTime(analyze)
			finding.LastAutoanalyze = nullableTime(autoanalyze)

			findings = append(findings, finding)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgres: table health: %w", err)
		}

		bloat, err := scanBloatFindings(ctx, tx, tableBloatQuery, p.Schema)
		if err != nil {
			return fmt.Errorf("postgres: table bloat: %w", err)
		}

		byRelation := make(map[string]diagnostics.IndexFinding, len(bloat))
		for _, row := range bloat {
			byRelation[row.Schema+"."+row.Table] = row
		}
		for i := range findings {
			if row, ok := byRelation[findings[i].Schema+"."+findings[i].Table]; ok {
				findings[i].BloatRatio = row.BloatRatio
				findings[i].BloatBytes = row.BloatBytes
			}
		}

		return nil
	}); err != nil {
		return nil, err
	}

	for i := range findings {
		findings[i].Flags = tableFlags(findings[i])
	}

	return findings, nil
}

// tableFlags summarises a finding as the conditions it trips, in a fixed order
// so that the output is stable.
func tableFlags(finding diagnostics.TableFinding) []string {
	flags := make([]string, 0, 5)

	if finding.DeadRatio >= deadRatioThreshold && finding.DeadTuples >= deadTuplesThreshold {
		flags = append(flags, "high_dead_ratio")
	}
	if finding.LastVacuum == nil && finding.LastAutovacuum == nil && finding.LiveTuples >= liveTuplesThreshold {
		flags = append(flags, "never_vacuumed")
	}
	if finding.LastAnalyze == nil && finding.LastAutoanalyze == nil && finding.LiveTuples >= liveTuplesThreshold {
		flags = append(flags, "never_analyzed")
	}
	if finding.SeqScans > finding.IdxScans*seqScanRatio &&
		finding.SeqScans >= seqScanThreshold &&
		finding.SizeBytes >= seqScanSizeThreshold {
		flags = append(flags, "seq_scan_heavy")
	}
	if finding.BloatRatio >= bloatRatioThreshold {
		flags = append(flags, "bloated")
	}

	return flags
}

// nullableTime converts a nullable timestamp column into the pointer the
// domain type uses, so that "never" is distinguishable from the zero time.
func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}

	at := value.Time

	return &at
}
