package postgres

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithConnParams(t *testing.T) {
	t.Run("a url dsn gains the application name and the read-only default", func(t *testing.T) {
		got, err := withConnParams("postgres://u:p@localhost:5432/db")

		require.NoError(t, err)
		parsed, err := url.Parse(got)
		require.NoError(t, err)
		assert.Equal(t, "pgmcp", parsed.Query().Get("application_name"))
		assert.Equal(t, "on", parsed.Query().Get("default_transaction_read_only"))
		assert.Equal(t, "/db", parsed.Path)
	})

	t.Run("a url dsn keeps its existing query parameters", func(t *testing.T) {
		got, err := withConnParams("postgresql://u@localhost/db?sslmode=require&connect_timeout=5")

		require.NoError(t, err)
		parsed, err := url.Parse(got)
		require.NoError(t, err)
		assert.Equal(t, "require", parsed.Query().Get("sslmode"))
		assert.Equal(t, "5", parsed.Query().Get("connect_timeout"))
		assert.Equal(t, "pgmcp", parsed.Query().Get("application_name"))
		assert.Equal(t, "on", parsed.Query().Get("default_transaction_read_only"))
	})

	t.Run("a key value dsn gains the application name and the read-only default", func(t *testing.T) {
		got, err := withConnParams("host=localhost user=pgmcp dbname=app sslmode=disable")

		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(got, "host=localhost user=pgmcp dbname=app sslmode=disable"), got)
		assert.Contains(t, got, "application_name=pgmcp")
		assert.Contains(t, got, "default_transaction_read_only=on")
	})

	t.Run("a key value dsn overrides a conflicting application name", func(t *testing.T) {
		got, err := withConnParams("host=localhost application_name=other")

		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(got, "application_name=pgmcp default_transaction_read_only=on"), got)
	})

	t.Run("an empty dsn yields only the enforced parameters", func(t *testing.T) {
		got, err := withConnParams("")

		require.NoError(t, err)
		assert.Equal(t, "application_name=pgmcp default_transaction_read_only=on", got)
	})

	t.Run("an unparsable url dsn returns an error", func(t *testing.T) {
		_, err := withConnParams("postgres://u:p@local host:5432/db?x=%zz")

		require.Error(t, err)
	})
}
