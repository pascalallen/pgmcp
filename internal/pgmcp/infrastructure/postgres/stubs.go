package postgres

import (
	"context"
	"errors"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// errNotImplemented is returned by the diagnostics methods that later slices
// of the adapter fill in (index and table health, and the ad hoc query
// runner).
var errNotImplemented = errors.New("postgres: not implemented")

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
