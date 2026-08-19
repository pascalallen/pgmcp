package postgres

import (
	"context"
	"errors"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// errNotImplemented is returned by the diagnostics methods that later slices
// of the adapter fill in (pg_stat_statements queries, EXPLAIN, index and
// table health, and the ad hoc query runner).
var errNotImplemented = errors.New("postgres: not implemented")

// TopQueries is implemented by statements.go.
func (s *Store) TopQueries(context.Context, diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
	return nil, errNotImplemented
}

// Explain is implemented by explain.go.
func (s *Store) Explain(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
	return nil, errNotImplemented
}

// IndexHealth is implemented by indexes.go.
func (s *Store) IndexHealth(context.Context, diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
	return nil, errNotImplemented
}

// TableHealth is implemented by tables.go.
func (s *Store) TableHealth(context.Context, diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
	return nil, errNotImplemented
}

// Query is implemented by query.go.
func (s *Store) Query(context.Context, diagnostics.QueryParams) (*diagnostics.QueryResult, error) {
	return nil, errNotImplemented
}
