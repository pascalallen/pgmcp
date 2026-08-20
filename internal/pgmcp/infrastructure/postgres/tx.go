package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// lockTimeout bounds how long any statement waits for a lock. Diagnostics
// must never queue behind another session's lock.
const lockTimeout = "2s"

// PostgreSQL SQLSTATEs the adapter translates into domain errors.
const (
	codeReadOnlySQLTransaction = "25006"
	codeQueryCanceled          = "57014"
)

// readOnly runs fn inside a READ ONLY, READ COMMITTED transaction with
// statement_timeout set to timeout (the store default when timeout is not
// positive) and lock_timeout set to 2s. The transaction is always rolled
// back: it is the only way this package runs SQL.
func (s *Store) readOnly(ctx context.Context, timeout time.Duration, fn func(ctx context.Context, tx *sql.Tx) error) error {
	if timeout <= 0 {
		timeout = s.defaultTimeout
	}
	if timeout < time.Millisecond {
		timeout = time.Millisecond
	}

	started := time.Now()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("postgres: begin read-only transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// SET LOCAL does not accept bind parameters; the value is a duration the
	// adapter itself formats as an integer, never caller-supplied text.
	statementTimeoutMs := int64(timeout / time.Millisecond)
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", statementTimeoutMs)); err != nil {
		return translateError(err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%s'", lockTimeout)); err != nil {
		return translateError(err)
	}

	err = translateError(fn(ctx, tx))

	s.log.Debug("read-only transaction finished",
		"duration_ms", time.Since(started).Milliseconds(),
		"ok", err == nil,
	)

	return err
}

// translateError maps PostgreSQL error codes onto domain errors: a write
// attempt becomes a *diagnostics.ReadOnlyViolation and a cancelled statement
// becomes a deadline error.
func translateError(err error) error {
	if err == nil {
		return nil
	}

	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch string(pqErr.Code) {
		case codeReadOnlySQLTransaction:
			return &diagnostics.ReadOnlyViolation{Msg: pqErr.Message}
		case codeQueryCanceled:
			return fmt.Errorf("statement timeout exceeded: %w", context.DeadlineExceeded)
		}
	}

	return err
}
