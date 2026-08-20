package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
)

// explainBaseOptions are the options every EXPLAIN carries: JSON so the
// domain can parse it, costs so the estimates are present, and no VERBOSE
// output because it adds column lists the plan analysis never reads.
var explainBaseOptions = []string{"FORMAT JSON", "COSTS", "VERBOSE false"}

// Explain returns the plan for a statement, optionally executing it under
// ANALYZE. The statement is re-validated with sqlguard here even though the
// tool layer already did: EXPLAIN ANALYZE runs the statement, so this is the
// last place a write could slip past before the read-only transaction is the
// only thing standing in its way. A rejection is returned unchanged so
// callers can recover it with errors.As.
func (s *Store) Explain(ctx context.Context, p diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
	if err := sqlguard.Validate(p.SQL, Parser{}); err != nil {
		return nil, err
	}

	statement := "EXPLAIN (" + strings.Join(explainOptions(p), ", ") + ") " + p.SQL

	var result *diagnostics.ExplainResult

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		// EXPLAIN (FORMAT JSON) returns a single row holding a single json
		// value: the array the domain parser consumes.
		var raw []byte
		if err := tx.QueryRowContext(ctx, statement).Scan(&raw); err != nil {
			return fmt.Errorf("postgres: explain: %w", err)
		}

		parsed, err := diagnostics.ParseExplainJSON(raw)
		if err != nil {
			if errors.Is(err, diagnostics.ErrNoPlan) {
				return fmt.Errorf("postgres: explain returned no plan: %w", err)
			}

			return fmt.Errorf("postgres: parse explain output: %w", err)
		}
		result = parsed

		return nil
	}); err != nil {
		return nil, err
	}

	return result, nil
}

// explainOptions builds the EXPLAIN option list. BUFFERS is only added
// alongside ANALYZE: PostgreSQL rejects it on its own before 16, and without
// execution there are no buffer counts to report.
func explainOptions(p diagnostics.ExplainParams) []string {
	options := make([]string, 0, len(explainBaseOptions)+2)
	options = append(options, explainBaseOptions...)
	if p.Analyze {
		options = append(options, "ANALYZE")
		if p.Buffers {
			options = append(options, "BUFFERS")
		}
	}

	return options
}
