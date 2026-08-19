package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// Row budget for the ad hoc query runner. A caller that asks for nothing gets
// defaultQueryMaxRows; anything outside the bounds is clamped rather than
// rejected, because the point of the cap is to bound the response, not to
// argue with the caller.
const (
	defaultQueryMaxRows = 500
	minQueryMaxRows     = 1
	maxQueryMaxRows     = 5000
)

// Query runs a caller-supplied statement inside the read-only transaction and
// returns at most MaxRows rows. A statement that tries to write fails on the
// server and surfaces as *diagnostics.ReadOnlyViolation; the SQL and the rows
// are returned to the caller and never logged.
func (s *Store) Query(ctx context.Context, p diagnostics.QueryParams) (*diagnostics.QueryResult, error) {
	maxRows := p.MaxRows
	if maxRows == 0 {
		maxRows = defaultQueryMaxRows
	}
	if maxRows < minQueryMaxRows {
		maxRows = minQueryMaxRows
	}
	if maxRows > maxQueryMaxRows {
		maxRows = maxQueryMaxRows
	}

	result := &diagnostics.QueryResult{
		Columns: make([]diagnostics.Column, 0),
		Rows:    make([][]any, 0),
	}

	if err := s.readOnly(ctx, p.Timeout, func(ctx context.Context, tx *sql.Tx) error {
		started := time.Now()

		rows, err := tx.QueryContext(ctx, p.SQL, p.Params...)
		if err != nil {
			return fmt.Errorf("postgres: query: %w", err)
		}
		defer func() { _ = rows.Close() }()

		columnTypes, err := rows.ColumnTypes()
		if err != nil {
			return fmt.Errorf("postgres: query columns: %w", err)
		}
		for _, columnType := range columnTypes {
			result.Columns = append(result.Columns, diagnostics.Column{
				Name: columnType.Name(),
				Type: columnType.DatabaseTypeName(),
			})
		}

		values := make([]any, len(columnTypes))
		targets := make([]any, len(columnTypes))
		for i := range values {
			targets[i] = &values[i]
		}

		for rows.Next() {
			// The loop reads one row past the budget on purpose: reaching this
			// branch means the server had another row to give, which is what
			// Truncated reports.
			if len(result.Rows) == maxRows {
				result.Truncated = true
				break
			}

			if err := rows.Scan(targets...); err != nil {
				return fmt.Errorf("postgres: scan query row: %w", err)
			}

			row := make([]any, len(values))
			for i, value := range values {
				row[i] = normaliseValue(value)
			}
			result.Rows = append(result.Rows, row)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgres: query: %w", err)
		}

		result.RowCount = len(result.Rows)
		result.DurationMs = float64(time.Since(started).Microseconds()) / 1000

		return nil
	}); err != nil {
		return nil, err
	}

	return result, nil
}

// normaliseValue converts a driver value into something that survives a JSON
// round trip. lib/pq hands back []byte for every text-ish type, which would
// otherwise be base64-encoded; NULL stays nil so that a missing value is
// distinguishable from an empty one.
func normaliseValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return string(typed)
	case string, bool, int64, float64, time.Time:
		return typed
	default:
		// The lib/pq driver produces only the cases above; anything else is
		// rendered rather than dropped.
		return fmt.Sprintf("%v", typed)
	}
}
