package config_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/config"
)

// envFunc builds a getenv func backed by a fixed map, mirroring os.Getenv's
// "" for unset behavior.
func envFunc(vars map[string]string) func(string) string {
	return func(key string) string {
		return vars[key]
	}
}

func TestLoad(t *testing.T) {
	t.Run("loads required database url from env", func(t *testing.T) {
		getenv := envFunc(map[string]string{
			"PGMCP_DATABASE_URL": "postgres://user:pass@localhost:5432/db?sslmode=disable",
		})

		cfg, err := config.Load(nil, getenv)

		require.NoError(t, err)
		assert.Equal(t, "postgres://user:pass@localhost:5432/db?sslmode=disable", cfg.DatabaseURL)
	})

	t.Run("flag overrides env", func(t *testing.T) {
		getenv := envFunc(map[string]string{
			"PGMCP_DATABASE_URL": "postgres://user:pass@localhost:5432/db?sslmode=disable",
			"PGMCP_LISTEN":       "127.0.0.1:1234",
		})

		cfg, err := config.Load([]string{"--listen", "0.0.0.0:9000"}, getenv)

		require.NoError(t, err)
		assert.Equal(t, "0.0.0.0:9000", cfg.Listen)
	})

	t.Run("aggregates all missing and invalid values into one error", func(t *testing.T) {
		getenv := envFunc(map[string]string{
			"PGMCP_TRANSPORT": "carrier-pigeon",
			"PGMCP_MAX_CONNS": "not-a-number",
		})

		cfg, err := config.Load(nil, getenv)

		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "DATABASE_URL")
		assert.Contains(t, err.Error(), "TRANSPORT")
		assert.Contains(t, err.Error(), "MAX_CONNS")
	})

	t.Run("defaults are applied", func(t *testing.T) {
		getenv := envFunc(map[string]string{
			"PGMCP_DATABASE_URL": "postgres://user:pass@localhost:5432/db?sslmode=disable",
		})

		cfg, err := config.Load(nil, getenv)

		require.NoError(t, err)
		assert.Equal(t, config.Transport("stdio"), cfg.Transport)
		assert.Equal(t, "127.0.0.1:8080", cfg.Listen)
		assert.Equal(t, 4, cfg.MaxConns)
		assert.Equal(t, 60*time.Second, cfg.CallTimeout)
		assert.Equal(t, 60, cfg.RateLimitPerMin)
		assert.Equal(t, 1<<20, cfg.MaxOutputBytes)
		assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
		assert.Equal(t, "text", cfg.LogFormat)
		assert.Equal(t, config.AuthMode("none"), cfg.AuthMode)
	})

	t.Run("http transport on non-loopback listen with auth none fails unless insecure flag", func(t *testing.T) {
		getenv := envFunc(map[string]string{
			"PGMCP_DATABASE_URL": "postgres://user:pass@localhost:5432/db?sslmode=disable",
		})

		cfg, err := config.Load([]string{"--transport", "http", "--listen", "0.0.0.0:8080"}, getenv)
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "LISTEN")

		cfg, err = config.Load(
			[]string{"--transport", "http", "--listen", "0.0.0.0:8080", "--insecure-no-auth"},
			getenv,
		)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.True(t, cfg.InsecureNoAuth)
	})

	t.Run("static auth requires at least one api key", func(t *testing.T) {
		getenv := envFunc(map[string]string{
			"PGMCP_DATABASE_URL": "postgres://user:pass@localhost:5432/db?sslmode=disable",
			"PGMCP_AUTH_MODE":    "static",
		})

		cfg, err := config.Load(nil, getenv)
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "API_KEYS")

		getenv = envFunc(map[string]string{
			"PGMCP_DATABASE_URL": "postgres://user:pass@localhost:5432/db?sslmode=disable",
			"PGMCP_AUTH_MODE":    "static",
			"PGMCP_API_KEYS":     "dev-key",
		})

		cfg, err = config.Load(nil, getenv)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, []string{"dev-key"}, cfg.APIKeys)
	})

	t.Run("jwt auth requires jwks url issuer and audience", func(t *testing.T) {
		getenv := envFunc(map[string]string{
			"PGMCP_DATABASE_URL": "postgres://user:pass@localhost:5432/db?sslmode=disable",
			"PGMCP_AUTH_MODE":    "jwt",
		})

		cfg, err := config.Load(nil, getenv)
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "JWKS_URL")
		assert.Contains(t, err.Error(), "JWT_ISSUER")
		assert.Contains(t, err.Error(), "JWT_AUDIENCE")

		getenv = envFunc(map[string]string{
			"PGMCP_DATABASE_URL": "postgres://user:pass@localhost:5432/db?sslmode=disable",
			"PGMCP_AUTH_MODE":    "jwt",
			"PGMCP_JWKS_URL":     "https://issuer.example/.well-known/jwks.json",
			"PGMCP_JWT_ISSUER":   "https://issuer.example",
			"PGMCP_JWT_AUDIENCE": "pgmcp",
		})

		cfg, err = config.Load(nil, getenv)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, "https://issuer.example/.well-known/jwks.json", cfg.JWKSURL)
		assert.Equal(t, "https://issuer.example", cfg.JWTIssuer)
		assert.Equal(t, "pgmcp", cfg.JWTAudience)
	})

	t.Run("csv env values are split and trimmed", func(t *testing.T) {
		getenv := envFunc(map[string]string{
			"PGMCP_DATABASE_URL":  "postgres://user:pass@localhost:5432/db?sslmode=disable",
			"PGMCP_AUTH_MODE":     "static",
			"PGMCP_API_KEYS":      " key1, key2 ,key3",
			"PGMCP_AUTH_SERVERS":  "https://a.example, https://b.example",
			"PGMCP_QUERY_SCHEMAS": "public, app ",
		})

		cfg, err := config.Load(nil, getenv)

		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, []string{"key1", "key2", "key3"}, cfg.APIKeys)
		assert.Equal(t, []string{"https://a.example", "https://b.example"}, cfg.AuthServers)
		assert.Equal(t, []string{"public", "app"}, cfg.QuerySchemas)
	})
}
