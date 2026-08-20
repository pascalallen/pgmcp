package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// tunableSettings is a pg_settings snapshot spanning every category, with one
// safety setting deliberately wrong so a warn verdict has something to land on.
func tunableSettings() []diagnostics.Setting {
	return []diagnostics.Setting{
		{Name: "shared_buffers", Value: "16384", Unit: "8kB", Source: "configuration file"},
		{Name: "work_mem", Value: "4096", Unit: "kB", Source: "default"},
		{Name: "max_connections", Value: "100", Source: "configuration file"},
		{Name: "autovacuum", Value: "on", Source: "default"},
		{Name: "wal_level", Value: "replica", Source: "default"},
		{Name: "fsync", Value: "off", Source: "configuration file"},
	}
}

func TestConfigCheck(t *testing.T) {
	ctx := context.Background()

	t.Run("config check runs every category when none is named", func(t *testing.T) {
		_, handler := ConfigCheck(fakeDiag{settings: func(context.Context) ([]diagnostics.Setting, error) {
			return tunableSettings(), nil
		}})

		_, out, err := handler(ctx, nil, ConfigCheckIn{})
		require.NoError(t, err)

		names := make([]string, 0, len(out.Checks))
		for _, check := range out.Checks {
			names = append(names, check.Name)
		}
		assert.Contains(t, names, "shared_buffers", "the memory category is included by default")
		assert.Contains(t, names, "autovacuum", "the autovacuum category is included by default")
		assert.Contains(t, names, "fsync", "the wal category is included by default")
		assert.Contains(t, names, "max_connections", "the connections category is included by default")
	})

	t.Run("config check restricts the report to the requested category", func(t *testing.T) {
		_, handler := ConfigCheck(fakeDiag{settings: func(context.Context) ([]diagnostics.Setting, error) {
			return tunableSettings(), nil
		}})

		_, out, err := handler(ctx, nil, ConfigCheckIn{Category: "autovacuum"})
		require.NoError(t, err)

		require.NotEmpty(t, out.Checks)
		for _, check := range out.Checks {
			assert.Equal(t, "autovacuum", check.Name, "only autovacuum heuristics may appear")
		}
	})

	t.Run("config check counts the verdicts it reports", func(t *testing.T) {
		_, handler := ConfigCheck(fakeDiag{settings: func(context.Context) ([]diagnostics.Setting, error) {
			return tunableSettings(), nil
		}})

		_, out, err := handler(ctx, nil, ConfigCheckIn{})
		require.NoError(t, err)

		counted := ConfigCheckSummary{}
		for _, check := range out.Checks {
			switch check.Verdict {
			case diagnostics.VerdictOK:
				counted.OK++
			case diagnostics.VerdictReview:
				counted.Review++
			case diagnostics.VerdictWarn:
				counted.Warn++
			}
		}

		assert.Equal(t, counted, out.Summary)
		assert.Equal(t, len(out.Checks), out.Summary.OK+out.Summary.Review+out.Summary.Warn)
		assert.Positive(t, out.Summary.Warn, "fsync=off must be reported as a warning")
	})

	t.Run("config check rejects an unknown category with a tool error before calling the port", func(t *testing.T) {
		called := false

		_, handler := ConfigCheck(fakeDiag{settings: func(context.Context) ([]diagnostics.Setting, error) {
			called = true

			return tunableSettings(), nil
		}})

		_, _, err := handler(ctx, nil, ConfigCheckIn{Category: "replication"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unsupported category "replication"`)
		assert.Contains(t, err.Error(), "autovacuum")
		assert.False(t, called, "the port must not be reached for an invalid category")
	})

	t.Run("config check reports an empty list rather than null when nothing is assessable", func(t *testing.T) {
		_, handler := ConfigCheck(fakeDiag{settings: func(context.Context) ([]diagnostics.Setting, error) {
			return nil, nil
		}})

		_, out, err := handler(ctx, nil, ConfigCheckIn{})
		require.NoError(t, err)

		assert.NotNil(t, out.Checks)
		assert.Empty(t, out.Checks)
		assert.Equal(t, ConfigCheckSummary{}, out.Summary)
	})

	t.Run("config check propagates a port failure", func(t *testing.T) {
		failure := errors.New("pg_settings unavailable")

		_, handler := ConfigCheck(fakeDiag{settings: func(context.Context) ([]diagnostics.Setting, error) {
			return nil, failure
		}})

		_, _, err := handler(ctx, nil, ConfigCheckIn{})
		require.ErrorIs(t, err, failure)
	})

	t.Run("config check answers over an mcp session and lists itself as read only", func(t *testing.T) {
		definition, handler := ConfigCheck(fakeDiag{settings: func(context.Context) ([]diagnostics.Setting, error) {
			return tunableSettings(), nil
		}})

		session := serveTool(t, definition, handler)

		out := callStructured[ConfigCheckOut](t, session, "config_check", ConfigCheckIn{Category: "wal"})
		require.NotEmpty(t, out.Checks)
		assert.Equal(t, "pgmcp", out.Database)
		assert.Equal(t, len(out.Checks), out.Summary.OK+out.Summary.Review+out.Summary.Warn)

		requireReadOnlyListing(t, session, "config_check", "Configuration check")
	})

	t.Run("config check reports an unknown category as a tool error over an mcp session", func(t *testing.T) {
		definition, handler := ConfigCheck(fakeDiag{})

		session := serveTool(t, definition, handler)

		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "config_check", Arguments: ConfigCheckIn{Category: "replication"}})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, contentText(result), "unsupported category")
	})
}
