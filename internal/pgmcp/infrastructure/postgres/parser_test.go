package postgres

import (
	"testing"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParserParse(t *testing.T) {
	cases := []struct {
		name      string
		sql       string
		kinds     []string
		nodeTypes []string
		functions []string
		schemas   []string
	}{
		{
			name:      "a plain select reports a single SelectStmt kind",
			sql:       "select 1",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "ResTarget", "A_Const"},
			functions: []string{},
			schemas:   []string{},
		},
		{
			name:      "a cte wrapping a delete exposes the nested DeleteStmt",
			sql:       "with d as (delete from t returning *) select * from d",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "DeleteStmt", "CommonTableExpr", "RangeVar"},
			functions: []string{},
			schemas:   []string{""},
		},
		{
			name:      "function names are reported lowercased",
			sql:       "select pg_terminate_backend(1), NOW()",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "FuncCall"},
			functions: []string{"pg_terminate_backend", "now"},
			schemas:   []string{},
		},
		{
			name:      "a schema qualified function keeps only its last name segment",
			sql:       "select PG_Catalog.PG_Terminate_Backend(1)",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "FuncCall"},
			functions: []string{"pg_terminate_backend"},
			schemas:   []string{},
		},
		{
			name:      "a for update clause is reported as a LockingClause node",
			sql:       "select * from t for update",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "LockingClause", "RangeVar"},
			functions: []string{},
			schemas:   []string{""},
		},
		{
			name:      "a select into is reported as an IntoClause node",
			sql:       "select 1 into x",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "IntoClause"},
			functions: []string{},
			schemas:   []string{""},
		},
		{
			name:      "two statements report two kinds",
			sql:       "select 1; select 2",
			kinds:     []string{"SelectStmt", "SelectStmt"},
			nodeTypes: []string{"SelectStmt"},
			functions: []string{},
			schemas:   []string{},
		},
		{
			name:      "a merge statement reports the MergeStmt kind",
			sql:       "MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE",
			kinds:     []string{"MergeStmt"},
			nodeTypes: []string{"MergeStmt", "MergeWhenClause", "RangeVar"},
			functions: []string{},
			schemas:   []string{""},
		},
		{
			name:      "an update reports the UpdateStmt kind",
			sql:       "update t set a = 1",
			kinds:     []string{"UpdateStmt"},
			nodeTypes: []string{"UpdateStmt", "RangeVar"},
			functions: []string{},
			schemas:   []string{""},
		},
		{
			name:      "a set statement reports the VariableSetStmt kind",
			sql:       "set work_mem = '64MB'",
			kinds:     []string{"VariableSetStmt"},
			nodeTypes: []string{"VariableSetStmt"},
			functions: []string{},
			schemas:   []string{},
		},
		{
			name:      "a show statement reports the VariableShowStmt kind",
			sql:       "show all",
			kinds:     []string{"VariableShowStmt"},
			nodeTypes: []string{"VariableShowStmt"},
			functions: []string{},
			schemas:   []string{},
		},
		{
			name:      "an explain reports the ExplainStmt kind and its inner select",
			sql:       "explain select * from public.t",
			kinds:     []string{"ExplainStmt"},
			nodeTypes: []string{"ExplainStmt", "SelectStmt", "RangeVar"},
			functions: []string{},
			schemas:   []string{"public"},
		},
		{
			name:      "a function inside a subquery is reported",
			sql:       "select * from t where a = ANY(select pg_sleep(1))",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "SubLink", "FuncCall"},
			functions: []string{"pg_sleep"},
			schemas:   []string{""},
		},
		{
			name:      "an empty statement reports no kinds",
			sql:       "",
			kinds:     []string{},
			nodeTypes: []string{},
			functions: []string{},
			schemas:   []string{},
		},
		{
			name:      "a schema qualified table reports its schema",
			sql:       "select * from public.orders",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "RangeVar"},
			functions: []string{},
			schemas:   []string{"public"},
		},
		{
			name:      "an unqualified table reports the empty schema",
			sql:       "select * from orders",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "RangeVar"},
			functions: []string{},
			schemas:   []string{""},
		},
		{
			name:      "a join across two schemas reports both",
			sql:       "select * from public.orders o join sales.items i on i.id = o.id",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "RangeVar", "JoinExpr"},
			functions: []string{},
			schemas:   []string{"public", "sales"},
		},
		{
			name:      "a table hidden in a subquery reports its schema too",
			sql:       "select * from public.orders where id in (select id from private.secrets)",
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "SubLink", "RangeVar"},
			functions: []string{},
			schemas:   []string{"private", "public"},
		},
		{
			name:      "a quoted schema keeps the case it was written in",
			sql:       `select * from "Public".t`,
			kinds:     []string{"SelectStmt"},
			nodeTypes: []string{"SelectStmt", "RangeVar"},
			functions: []string{},
			schemas:   []string{"Public"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parser{}.Parse(tc.sql)

			require.NoError(t, err)
			require.NotNil(t, stmt)
			assert.Equal(t, tc.kinds, stmt.Kinds)
			for _, nodeType := range tc.nodeTypes {
				assert.Truef(t, stmt.NodeTypes[nodeType], "expected node type %q in %v", nodeType, stmt.NodeTypes)
			}
			assert.ElementsMatch(t, tc.functions, stmt.Functions)
			assert.Equal(t, tc.schemas, stmt.Schemas)
		})
	}
}

func TestParserParseReportsAnErrorForInvalidSQL(t *testing.T) {
	t.Run("a syntax error is returned to the caller", func(t *testing.T) {
		stmt, err := Parser{}.Parse("select from from")

		require.Error(t, err)
		assert.Nil(t, stmt)
	})
}

func TestParserNeverReturnsNilSlices(t *testing.T) {
	t.Run("kinds, functions and schemas are empty rather than nil", func(t *testing.T) {
		stmt, err := Parser{}.Parse("select 1")

		require.NoError(t, err)
		assert.NotNil(t, stmt.Kinds)
		assert.NotNil(t, stmt.Functions)
		assert.NotNil(t, stmt.NodeTypes)
		assert.NotNil(t, stmt.Schemas)
	})
}

func TestParserSatisfiesTheSqlguardParserPort(t *testing.T) {
	t.Run("a rejected statement is recognised by sqlguard", func(t *testing.T) {
		err := sqlguard.Validate("select pg_sleep(1)", Parser{})

		var rejection *sqlguard.Rejection
		require.ErrorAs(t, err, &rejection)
		assert.Equal(t, sqlguard.ReasonFunction, rejection.Reason)
	})

	t.Run("a plain select is accepted by sqlguard", func(t *testing.T) {
		require.NoError(t, sqlguard.Validate("select 1", Parser{}))
	})
}
