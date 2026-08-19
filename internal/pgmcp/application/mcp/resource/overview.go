// Package resource registers the read-only MCP resources exposed by pgmcp:
// pgmcp://overview and pgmcp://settings.
package resource

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

const overviewURI = "pgmcp://overview"

// overviewTTLMs is how long a client may cache the overview resource before
// re-fetching. The dashboard summary changes slowly enough that a 30s TTL
// keeps repeated reads cheap without going stale in practice.
const overviewTTLMs = 30000

// registerOverview adds the pgmcp://overview resource, which reports the
// top-level server/database dashboard summary produced by d.Overview.
func registerOverview(s *mcp.Server, d diagnostics.Diagnostics) {
	s.AddResource(&mcp.Resource{
		URI:      overviewURI,
		Name:     "overview",
		Title:    "Server overview",
		MIMEType: "application/json",
	}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		overview, err := d.Overview(ctx)
		if err != nil {
			return nil, err
		}

		body, err := json.Marshal(overview)
		if err != nil {
			return nil, err
		}

		return &mcp.ReadResourceResult{
			Cacheable: mcp.Cacheable{TTLMs: overviewTTLMs},
			Contents: []*mcp.ResourceContents{
				{URI: overviewURI, MIMEType: "application/json", Text: string(body)},
			},
		}, nil
	})
}
