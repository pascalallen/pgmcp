package postgres

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

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

	t.Run("an unparsable url dsn returns an error that never quotes the dsn", func(t *testing.T) {
		_, err := withConnParams("postgres://admin:sup3rs3cret@local host:5432/db")

		require.ErrorIs(t, err, errInvalidDSN)
		assert.NotContains(t, err.Error(), "sup3rs3cret")
		assert.NotContains(t, err.Error(), "admin")
		assert.NotContains(t, err.Error(), "local host")
	})

	t.Run("an unparsable query string returns an error that never quotes the dsn", func(t *testing.T) {
		_, err := withConnParams("postgres://admin:sup3rs3cret@localhost:5432/db?opt=%zz")

		require.ErrorIs(t, err, errInvalidDSN)
		assert.NotContains(t, err.Error(), "sup3rs3cret")
	})
}

func TestOpenRejectsAnUnparsableDSNWithoutLeakingIt(t *testing.T) {
	t.Run("the error surfaced to the startup path carries no credentials", func(t *testing.T) {
		_, err := Open(context.Background(), "postgres://admin:sup3rs3cret@local host:5432/db", 2, time.Second, testLogger())

		require.ErrorIs(t, err, errInvalidDSN)
		assert.NotContains(t, err.Error(), "sup3rs3cret")
	})
}
