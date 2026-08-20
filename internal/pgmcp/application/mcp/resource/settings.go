package resource

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

const settingsURI = "pgmcp://settings"

// settingsTTLMs is how long a client may cache the settings resource before
// re-fetching. pg_settings only changes on a config reload/restart, so a 5
// minute TTL is generous without going stale in any operationally relevant
// window.
const settingsTTLMs = 300000

// registerSettings adds the pgmcp://settings resource, which lists the raw
// pg_settings rows produced by d.Settings.
func registerSettings(s *mcp.Server, d diagnostics.Diagnostics) {
	s.AddResource(&mcp.Resource{
		URI:      settingsURI,
		Name:     "settings",
		Title:    "Server settings",
		MIMEType: "application/json",
	}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		settings, err := d.Settings(ctx)
		if err != nil {
			return nil, err
		}

		body, err := json.Marshal(settings)
		if err != nil {
			return nil, err
		}

		return &mcp.ReadResourceResult{
			Cacheable: mcp.Cacheable{TTLMs: settingsTTLMs},
			Contents: []*mcp.ResourceContents{
				{URI: settingsURI, MIMEType: "application/json", Text: string(body)},
			},
		}, nil
	})
}

// Register adds every pgmcp resource to s, backed by d.
func Register(s *mcp.Server, d diagnostics.Diagnostics) {
	registerOverview(s, d)
	registerSettings(s, d)
}
