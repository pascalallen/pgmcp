package sqlguard_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
)

// fakeParser is a test double for sqlguard.Parser: it either returns a
// canned error, or the Statement registered for the exact sql it receives.
type fakeParser struct {
	statements map[string]*sqlguard.Statement
	err        error
}

func (p *fakeParser) Parse(sql string) (*sqlguard.Statement, error) {
	if p.err != nil {
		return nil, p.err
	}
	stmt, ok := p.statements[sql]
	if !ok {
		return nil, errors.New("fakeParser: no statement registered for: " + sql)
	}
	return stmt, nil
}

func newFakeParser(sql string, stmt *sqlguard.Statement) *fakeParser {
	return &fakeParser{statements: map[string]*sqlguard.Statement{sql: stmt}}
}

func TestValidate(t *testing.T) {
	const sql = "the exact SQL text is irrelevant; the fake parser keys off it"

	tests := []struct {
		name          string
		parser        sqlguard.Parser
		wantErr       bool
		wantReason    sqlguard.Reason
		wantDetailHas string
	}{
		{
			name: "allows a plain select",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true},
			}),
		},
		{
			name: "allows a select built from a CTE",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true, "CommonTableExpr": true},
			}),
		},
		{
			name: "allows a select over a VALUES list",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true},
			}),
		},
		{
			name: "allows an explain of a select",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"ExplainStmt"},
				NodeTypes: map[string]bool{"ExplainStmt": true, "SelectStmt": true},
			}),
		},
		{
			name: "allows a show",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"VariableShowStmt"},
				NodeTypes: map[string]bool{"VariableShowStmt": true},
			}),
		},
		{
			name: "rejects two statements separated by a semicolon",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds: []string{"SelectStmt", "SelectStmt"},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonMultiple,
			wantDetailHas: "2",
		},
		{
			name: "rejects an update",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"UpdateStmt"},
				NodeTypes: map[string]bool{"UpdateStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "UpdateStmt",
		},
		{
			name: "rejects an insert",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"InsertStmt"},
				NodeTypes: map[string]bool{"InsertStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "InsertStmt",
		},
		{
			name: "rejects a delete",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"DeleteStmt"},
				NodeTypes: map[string]bool{"DeleteStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "DeleteStmt",
		},
		{
			name: "rejects a merge",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"MergeStmt"},
				NodeTypes: map[string]bool{"MergeStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "MergeStmt",
		},
		{
			name: "rejects a copy",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"CopyStmt"},
				NodeTypes: map[string]bool{"CopyStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "CopyStmt",
		},
		{
			name: "rejects a set",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"VariableSetStmt"},
				NodeTypes: map[string]bool{"VariableSetStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "VariableSetStmt",
		},
		{
			name: "rejects a call",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"CallStmt"},
				NodeTypes: map[string]bool{"CallStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "CallStmt",
		},
		{
			name: "rejects a do block",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"DoStmt"},
				NodeTypes: map[string]bool{"DoStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "DoStmt",
		},
		{
			name: "rejects a begin",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"TransactionStmt"},
				NodeTypes: map[string]bool{"TransactionStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "TransactionStmt",
		},
		{
			name: "rejects a create table as",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"CreateTableAsStmt"},
				NodeTypes: map[string]bool{"CreateTableAsStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonKind,
			wantDetailHas: "CreateTableAsStmt",
		},
		{
			name: "rejects a CTE containing a delete",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true, "CommonTableExpr": true, "DeleteStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonNestedStmt,
			wantDetailHas: "DeleteStmt",
		},
		{
			name: "rejects explain analyze of an update",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"ExplainStmt"},
				NodeTypes: map[string]bool{"ExplainStmt": true, "UpdateStmt": true},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonNestedStmt,
			wantDetailHas: "UpdateStmt",
		},
		{
			name: "rejects select for update",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true, "LockingClause": true},
			}),
			wantErr:    true,
			wantReason: sqlguard.ReasonLocking,
		},
		{
			name: "rejects select into",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true, "IntoClause": true},
			}),
			wantErr:    true,
			wantReason: sqlguard.ReasonInto,
		},
		{
			name: "rejects a call to pg_terminate_backend",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true},
				Functions: []string{"pg_terminate_backend"},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonFunction,
			wantDetailHas: "pg_terminate_backend",
		},
		{
			name: "rejects dblink_exec by prefix match",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true},
				Functions: []string{"dblink_exec"},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonFunction,
			wantDetailHas: "dblink_exec",
		},
		{
			name: "rejects an uppercase PG_SLEEP call case-insensitively",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true},
				Functions: []string{"PG_SLEEP"},
			}),
			wantErr:       true,
			wantReason:    sqlguard.ReasonFunction,
			wantDetailHas: "pg_sleep",
		},
		{
			name: "allows now, count, and jsonb_agg",
			parser: newFakeParser(sql, &sqlguard.Statement{
				Kinds:     []string{"SelectStmt"},
				NodeTypes: map[string]bool{"SelectStmt": true},
				Functions: []string{"now", "count", "jsonb_agg"},
			}),
		},
		{
			name:          "wraps a parser error as a parse rejection",
			parser:        &fakeParser{err: errors.New(`syntax error at or near "SELCT"`)},
			wantErr:       true,
			wantReason:    sqlguard.ReasonParse,
			wantDetailHas: "syntax error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := sqlguard.Validate(sql, tc.parser)

			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)

			var rejection *sqlguard.Rejection
			require.True(t, errors.As(err, &rejection), "error must be a *sqlguard.Rejection")
			assert.Equal(t, tc.wantReason, rejection.Reason)
			if tc.wantDetailHas != "" {
				assert.Contains(t, rejection.Detail, tc.wantDetailHas)
			}
		})
	}
}

func TestRejectionErrorFormatsAsSqlguardReasonColonDetail(t *testing.T) {
	rejection := &sqlguard.Rejection{Reason: sqlguard.ReasonFunction, Detail: "pg_sleep"}

	assert.Equal(t, "sqlguard: function_not_allowed: pg_sleep", rejection.Error())
}
