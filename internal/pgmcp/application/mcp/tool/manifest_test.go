package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// manifestPath is the MCPB manifest the release pipeline packs into the
// Claude Desktop bundle, relative to this package.
const manifestPath = "../../../../../mcpb/manifest.json"

// serverJSONPath is the MCP Registry manifest, relative to this package.
const serverJSONPath = "../../../../../server.json"

// bundleManifest is the subset of the MCPB manifest these tests pin. The
// bundle is the one distribution channel that lists the tools statically, so
// the list has to be held to the catalogue by a test or it will drift.
type bundleManifest struct {
	ManifestVersion string `json:"manifest_version"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Description     string `json:"description"`
	Server          struct {
		Type       string `json:"type"`
		EntryPoint string `json:"entry_point"`
		MCPConfig  struct {
			Command           string            `json:"command"`
			Env               map[string]string `json:"env"`
			PlatformOverrides map[string]struct {
				Command string `json:"command"`
			} `json:"platform_overrides"`
		} `json:"mcp_config"`
	} `json:"server"`
	Tools []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"tools"`
	Prompts          []json.RawMessage `json:"prompts"`
	PromptsGenerated bool              `json:"prompts_generated"`
	UserConfig       map[string]struct {
		Type      string `json:"type"`
		Required  bool   `json:"required"`
		Sensitive bool   `json:"sensitive"`
	} `json:"user_config"`
	Compatibility struct {
		Platforms []string `json:"platforms"`
	} `json:"compatibility"`
}

func loadBundleManifest(t *testing.T) bundleManifest {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(manifestPath))
	require.NoError(t, err)

	var manifest bundleManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))

	return manifest
}

func TestBundleManifest(t *testing.T) {
	manifest := loadBundleManifest(t)

	t.Run("the bundle lists exactly the tools the catalogue registers, with the same descriptions", func(t *testing.T) {
		session := serveCatalog(t, Deps{Diag: fakeDiag{}, Parser: selectParser()}, Options{})

		listed, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
		require.NoError(t, err)

		registered := make(map[string]string, len(listed.Tools))
		for _, definition := range listed.Tools {
			registered[definition.Name] = definition.Description
		}

		bundled := make(map[string]string, len(manifest.Tools))
		for _, definition := range manifest.Tools {
			bundled[definition.Name] = definition.Description
		}

		assert.Equal(t, registered, bundled)
		assert.ElementsMatch(t, Names(Options{}), listedKeys(bundled))
	})

	t.Run("the bundle declares its prompt as server-generated rather than duplicating the template", func(t *testing.T) {
		// A static prompts[] entry needs the prompt text in the manifest; the
		// slow-query template lives in Go and is served over prompts/get.
		assert.True(t, manifest.PromptsGenerated)
		assert.Empty(t, manifest.Prompts)
	})

	t.Run("the bundle describes the server the same way the registry manifest does", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Clean(serverJSONPath))
		require.NoError(t, err)

		var registry struct {
			Description string `json:"description"`
		}
		require.NoError(t, json.Unmarshal(raw, &registry))

		assert.Equal(t, "0.3", manifest.ManifestVersion)
		assert.Equal(t, "pgmcp", manifest.Name)
		assert.Equal(t, registry.Description, manifest.Description)
	})

	t.Run("the bundle launches the packed binary over stdio with the database url from the keychain", func(t *testing.T) {
		server := manifest.Server
		assert.Equal(t, "binary", server.Type)
		assert.Equal(t, "server/pgmcp", server.EntryPoint)
		assert.Equal(t, "${__dirname}/server/pgmcp", server.MCPConfig.Command)
		assert.Equal(t, "${__dirname}/server/pgmcp.exe", server.MCPConfig.PlatformOverrides["win32"].Command)
		assert.Equal(t, "${user_config.database_url}", server.MCPConfig.Env["PGMCP_DATABASE_URL"])

		databaseURL, ok := manifest.UserConfig["database_url"]
		require.True(t, ok, "database_url must be a user_config field")
		assert.Equal(t, "string", databaseURL.Type)
		assert.True(t, databaseURL.Required, "the DSN is the one setting the server cannot start without")
		assert.True(t, databaseURL.Sensitive, "the DSN carries a password and belongs in the OS keychain")

		assert.ElementsMatch(t, []string{"darwin", "win32"}, manifest.Compatibility.Platforms)
	})

	t.Run("every environment variable the bundle sets is one the server reads", func(t *testing.T) {
		for key := range manifest.Server.MCPConfig.Env {
			assert.Regexp(t, `^PGMCP_[A-Z_]+$`, key)
		}
		for key, value := range manifest.Server.MCPConfig.Env {
			assert.Regexp(t, `^\$\{user_config\.[a-z_]+\}$`, value, "env %s must come from user_config, not be hard-coded", key)
		}
	})
}

// listedKeys returns the keys of m in no particular order.
func listedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	return keys
}
