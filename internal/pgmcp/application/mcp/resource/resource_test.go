package resource_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/resource"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// fakeDiagnostics implements only Overview and Settings; any other method
// panics because the embedded interface is left nil.
type fakeDiagnostics struct {
	diagnostics.Diagnostics

	overview    *diagnostics.Overview
	overviewErr error

	settings    []diagnostics.Setting
	settingsErr error
}

func (f *fakeDiagnostics) Overview(context.Context) (*diagnostics.Overview, error) {
	return f.overview, f.overviewErr
}

func (f *fakeDiagnostics) Settings(context.Context) ([]diagnostics.Setting, error) {
	return f.settings, f.settingsErr
}

// connect wires a server with the given Diagnostics fake to an in-memory
// client session, cleaning up both when the test ends.
func connect(t *testing.T, d diagnostics.Diagnostics) *mcp.ClientSession {
	t.Helper()

	s := mcp.NewServer(&mcp.Implementation{Name: "pgmcp-test", Version: "0.0.0"}, nil)
	resource.Register(s, d)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ctx := context.Background()

	serverSession, err := s.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "pgmcp-client-test", Version: "0.0.0"}, nil)

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

func TestResourceRegister(t *testing.T) {
	ctx := context.Background()

	t.Run("overview resource returns json with ttl hint", func(t *testing.T) {
		fake := &fakeDiagnostics{overview: &diagnostics.Overview{
			Server:           diagnostics.ServerInfo{Version: "16.4", Database: "app"},
			CacheHitRatio:    0.98,
			Connections:      5,
			MaxConnections:   100,
			PgStatStatements: true,
		}}
		cs := connect(t, fake)

		result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "pgmcp://overview"})
		require.NoError(t, err)
		assert.Equal(t, 30000, result.TTLMs)
		require.Len(t, result.Contents, 1)

		content := result.Contents[0]
		assert.Equal(t, "pgmcp://overview", content.URI)
		assert.Equal(t, "application/json", content.MIMEType)

		var got diagnostics.Overview
		require.NoError(t, json.Unmarshal([]byte(content.Text), &got))
		assert.Equal(t, "16.4", got.Server.Version)
		assert.Equal(t, "app", got.Server.Database)
		assert.Equal(t, 0.98, got.CacheHitRatio)
		assert.True(t, got.PgStatStatements)
	})

	t.Run("settings resource lists settings", func(t *testing.T) {
		fake := &fakeDiagnostics{settings: []diagnostics.Setting{
			{Name: "shared_buffers", Value: "128MB", Source: "configuration file", Category: "Resource Usage / Memory"},
			{Name: "work_mem", Value: "4MB", Source: "default", Category: "Resource Usage / Memory"},
		}}
		cs := connect(t, fake)

		result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "pgmcp://settings"})
		require.NoError(t, err)
		assert.Equal(t, 300000, result.TTLMs)
		require.Len(t, result.Contents, 1)

		content := result.Contents[0]
		assert.Equal(t, "pgmcp://settings", content.URI)
		assert.Equal(t, "application/json", content.MIMEType)

		var got []diagnostics.Setting
		require.NoError(t, json.Unmarshal([]byte(content.Text), &got))
		require.Len(t, got, 2)
		assert.Equal(t, "shared_buffers", got[0].Name)
		assert.Equal(t, "work_mem", got[1].Name)
	})

	t.Run("unknown uri returns resource not found error", func(t *testing.T) {
		cs := connect(t, &fakeDiagnostics{})

		_, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "pgmcp://nope"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("port error from overview propagates to the client", func(t *testing.T) {
		fake := &fakeDiagnostics{overviewErr: assertError("boom")}
		cs := connect(t, fake)

		_, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "pgmcp://overview"})
		require.Error(t, err)
	})

	t.Run("port error from settings propagates to the client", func(t *testing.T) {
		fake := &fakeDiagnostics{settingsErr: assertError("boom")}
		cs := connect(t, fake)

		_, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "pgmcp://settings"})
		require.Error(t, err)
	})
}

type assertError string

func (e assertError) Error() string { return string(e) }
