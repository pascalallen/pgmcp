// Package config loads pgmcp's runtime configuration from CLI flags and
// environment variables, applies defaults, and validates the result.
package config

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

// Transport selects how the MCP server exposes itself: over stdio (the
// default, for use as a subprocess) or over HTTP.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
)

// AuthMode selects how HTTP transport requests are authenticated. It has no
// effect on the stdio transport.
type AuthMode string

const (
	AuthModeNone   AuthMode = "none"
	AuthModeStatic AuthMode = "static"
	AuthModeJWT    AuthMode = "jwt"
)

// Config is the fully resolved, validated runtime configuration for pgmcp.
type Config struct {
	DatabaseURL string
	Transport   Transport
	Listen      string
	ResourceURL string

	AuthMode    AuthMode
	APIKeys     []string
	JWKSURL     string
	JWTIssuer   string
	JWTAudience string
	AuthServers []string

	DisableQuery bool
	QuerySchemas []string

	MaxConns        int
	CallTimeout     time.Duration
	RateLimitPerMin int
	MaxOutputBytes  int

	LogLevel  slog.Level
	LogFormat string

	InsecureNoAuth bool
}

const (
	defaultTransport       = TransportStdio
	defaultListen          = "127.0.0.1:8080"
	defaultAuthMode        = AuthModeNone
	defaultMaxConns        = 4
	defaultCallTimeout     = 60 * time.Second
	defaultRateLimitPerMin = 60
	defaultMaxOutputBytes  = 1 << 20
	defaultLogLevel        = "info"
	defaultLogFormat       = "text"
)

// Load resolves configuration from args (CLI flags, e.g. os.Args[1:]) and
// getenv (e.g. os.Getenv). Flags take precedence over environment variables,
// which take precedence over defaults. Every PGMCP_<KEY> environment
// variable has a matching --<kebab-key> flag. Validation errors are
// aggregated and returned as a single error naming every offending key.
func Load(args []string, getenv func(string) string) (*Config, error) {
	fs := flag.NewFlagSet("pgmcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	databaseURL := fs.String("database-url", "", "Postgres connection string (env PGMCP_DATABASE_URL)")
	transport := fs.String("transport", "", `MCP transport: "stdio" or "http" (env PGMCP_TRANSPORT)`)
	listen := fs.String("listen", "", "HTTP listen address, e.g. 127.0.0.1:8080 (env PGMCP_LISTEN)")
	resourceURL := fs.String("resource-url", "", "Public URL this server is reachable at, for OAuth resource metadata (env PGMCP_RESOURCE_URL)")
	authMode := fs.String("auth-mode", "", `HTTP auth mode: "none", "static", or "jwt" (env PGMCP_AUTH_MODE)`)
	apiKeys := fs.String("api-keys", "", "Comma-separated static API keys (env PGMCP_API_KEYS)")
	jwksURL := fs.String("jwks-url", "", "JWKS URL for jwt auth (env PGMCP_JWKS_URL)")
	jwtIssuer := fs.String("jwt-issuer", "", "Expected JWT issuer for jwt auth (env PGMCP_JWT_ISSUER)")
	jwtAudience := fs.String("jwt-audience", "", "Expected JWT audience for jwt auth (env PGMCP_JWT_AUDIENCE)")
	authServers := fs.String("auth-servers", "", "Comma-separated OAuth authorization server URLs (env PGMCP_AUTH_SERVERS)")
	disableQuery := fs.Bool("disable-query", false, "Disable the bounded ad-hoc query tool (env PGMCP_DISABLE_QUERY)")
	querySchemas := fs.String("query-schemas", "", "Comma-separated schemas the query tool may read (env PGMCP_QUERY_SCHEMAS)")
	maxConns := fs.String("max-conns", "", "Maximum Postgres connections (env PGMCP_MAX_CONNS)")
	callTimeout := fs.String("call-timeout", "", "Per-tool-call timeout, e.g. 60s (env PGMCP_CALL_TIMEOUT)")
	rateLimit := fs.String("rate-limit", "", "Requests allowed per caller per minute (env PGMCP_RATE_LIMIT)")
	maxOutputBytes := fs.String("max-output-bytes", "", "Maximum bytes returned by a tool call (env PGMCP_MAX_OUTPUT_BYTES)")
	logLevel := fs.String("log-level", "", `Log level: "debug", "info", "warn", or "error" (env PGMCP_LOG_LEVEL)`)
	logFormat := fs.String("log-format", "", `Log format: "json" or "text" (env PGMCP_LOG_FORMAT)`)
	insecureNoAuth := fs.Bool("insecure-no-auth", false, "Allow AUTH_MODE=none on a non-loopback HTTP listen (env PGMCP_INSECURE_NO_AUTH)")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	resolve := func(flagName, flagVal, envKey string) string {
		if set[flagName] {
			return flagVal
		}
		if v := getenv("PGMCP_" + envKey); v != "" {
			return v
		}
		return ""
	}

	var errs []string
	addErr := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	resolveBool := func(flagName string, flagVal bool, envKey string, def bool) bool {
		if set[flagName] {
			return flagVal
		}
		raw := getenv("PGMCP_" + envKey)
		if raw == "" {
			return def
		}
		b, err := strconv.ParseBool(raw)
		if err != nil {
			addErr("%s must be a boolean (got %q)", envKey, raw)
			return def
		}
		return b
	}

	resolveInt := func(flagName, flagVal, envKey string, def int) int {
		raw := resolve(flagName, flagVal, envKey)
		if raw == "" {
			return def
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			addErr("%s must be a positive integer (got %q)", envKey, raw)
			return def
		}
		return n
	}

	cfg := &Config{}

	cfg.DatabaseURL = resolve("database-url", *databaseURL, "DATABASE_URL")
	if cfg.DatabaseURL == "" {
		addErr("DATABASE_URL is required")
	}

	transportRaw := resolve("transport", *transport, "TRANSPORT")
	switch Transport(transportRaw) {
	case "":
		cfg.Transport = defaultTransport
	case TransportStdio, TransportHTTP:
		cfg.Transport = Transport(transportRaw)
	default:
		addErr("TRANSPORT must be %q or %q (got %q)", TransportStdio, TransportHTTP, transportRaw)
		cfg.Transport = defaultTransport
	}

	cfg.Listen = resolve("listen", *listen, "LISTEN")
	if cfg.Listen == "" {
		cfg.Listen = defaultListen
	}

	cfg.ResourceURL = resolve("resource-url", *resourceURL, "RESOURCE_URL")

	authModeRaw := resolve("auth-mode", *authMode, "AUTH_MODE")
	switch AuthMode(authModeRaw) {
	case "":
		cfg.AuthMode = defaultAuthMode
	case AuthModeNone, AuthModeStatic, AuthModeJWT:
		cfg.AuthMode = AuthMode(authModeRaw)
	default:
		addErr("AUTH_MODE must be %q, %q, or %q (got %q)", AuthModeNone, AuthModeStatic, AuthModeJWT, authModeRaw)
		cfg.AuthMode = defaultAuthMode
	}

	cfg.APIKeys = splitCSV(resolve("api-keys", *apiKeys, "API_KEYS"))
	cfg.JWKSURL = resolve("jwks-url", *jwksURL, "JWKS_URL")
	cfg.JWTIssuer = resolve("jwt-issuer", *jwtIssuer, "JWT_ISSUER")
	cfg.JWTAudience = resolve("jwt-audience", *jwtAudience, "JWT_AUDIENCE")
	cfg.AuthServers = splitCSV(resolve("auth-servers", *authServers, "AUTH_SERVERS"))

	cfg.DisableQuery = resolveBool("disable-query", *disableQuery, "DISABLE_QUERY", false)
	cfg.QuerySchemas = splitCSV(resolve("query-schemas", *querySchemas, "QUERY_SCHEMAS"))

	cfg.MaxConns = resolveInt("max-conns", *maxConns, "MAX_CONNS", defaultMaxConns)
	cfg.RateLimitPerMin = resolveInt("rate-limit", *rateLimit, "RATE_LIMIT", defaultRateLimitPerMin)
	cfg.MaxOutputBytes = resolveInt("max-output-bytes", *maxOutputBytes, "MAX_OUTPUT_BYTES", defaultMaxOutputBytes)

	callTimeoutRaw := resolve("call-timeout", *callTimeout, "CALL_TIMEOUT")
	if callTimeoutRaw == "" {
		cfg.CallTimeout = defaultCallTimeout
	} else if d, err := time.ParseDuration(callTimeoutRaw); err != nil || d <= 0 {
		addErr("CALL_TIMEOUT must be a positive duration (got %q)", callTimeoutRaw)
		cfg.CallTimeout = defaultCallTimeout
	} else {
		cfg.CallTimeout = d
	}

	logLevelRaw := resolve("log-level", *logLevel, "LOG_LEVEL")
	if logLevelRaw == "" {
		logLevelRaw = defaultLogLevel
	}
	if lvl, err := parseLogLevel(logLevelRaw); err != nil {
		addErr("LOG_LEVEL must be one of \"debug\", \"info\", \"warn\", \"error\" (got %q)", logLevelRaw)
		cfg.LogLevel = slog.LevelInfo
	} else {
		cfg.LogLevel = lvl
	}

	cfg.LogFormat = resolve("log-format", *logFormat, "LOG_FORMAT")
	if cfg.LogFormat == "" {
		cfg.LogFormat = defaultLogFormat
	} else if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		addErr("LOG_FORMAT must be %q or %q (got %q)", "json", "text", cfg.LogFormat)
	}

	cfg.InsecureNoAuth = resolveBool("insecure-no-auth", *insecureNoAuth, "INSECURE_NO_AUTH", false)

	if cfg.AuthMode == AuthModeStatic && len(cfg.APIKeys) == 0 {
		addErr("API_KEYS must contain at least one key when AUTH_MODE=static")
	}

	if cfg.AuthMode == AuthModeJWT {
		if cfg.JWKSURL == "" {
			addErr("JWKS_URL is required when AUTH_MODE=jwt")
		}
		if cfg.JWTIssuer == "" {
			addErr("JWT_ISSUER is required when AUTH_MODE=jwt")
		}
		if cfg.JWTAudience == "" {
			addErr("JWT_AUDIENCE is required when AUTH_MODE=jwt")
		}
	}

	if cfg.Transport == TransportHTTP && cfg.AuthMode == AuthModeNone && !cfg.InsecureNoAuth && !isLoopback(cfg.Listen) {
		addErr("LISTEN must be a loopback address when TRANSPORT=http and AUTH_MODE=none; set AUTH_MODE or pass --insecure-no-auth to override")
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %s", strings.Join(errs, "; "))
	}

	return cfg, nil
}

// isLoopback reports whether hostport's host is "localhost" or a loopback
// IP address. A hostport without a port (or an unparseable one) is treated
// as a bare host.
func isLoopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// splitCSV splits a comma-separated string into trimmed, non-empty parts.
// An empty input yields a nil slice.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q", s)
	}
}
