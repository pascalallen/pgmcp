// Command pgmcp is a read-only Postgres ops/DBA MCP server. It serves the
// same MCP surface over two transports: stdio, for use as a subprocess of an
// MCP client, and Streamable HTTP behind bearer auth, for a shared
// deployment. Which one is a matter of configuration; everything above the
// transport is identical.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/config"
	"github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/container"
	httpserver "github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/http"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// Exit codes. They are the process's only structured output, so they stay
// stable: an operator's mistake is distinguishable from a runtime failure
// without parsing a log line.
const (
	exitOK      = 0
	exitRuntime = 1
	exitConfig  = 2
)

// shutdownTimeout bounds the graceful drain of the HTTP server after a
// signal. A streamable session may be mid-stream; ten seconds is long enough
// for a tool call to finish and short enough that a supervisor's own kill
// deadline is never the thing that stops us.
const shutdownTimeout = 10 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is main's testable body: it returns the exit code rather than taking
// it, and takes its arguments, environment and streams as parameters.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	rest, versionRequested := stripVersionFlag(args)
	if versionRequested {
		fmt.Fprintln(stdout, "pgmcp "+version)

		return exitOK
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c, cleanup, err := container.InitializeContainer(ctx, rest, getenv, version)
	if err != nil {
		fmt.Fprintln(stderr, err)

		var configErr *container.ConfigError
		if errors.As(err, &configErr) {
			return exitConfig
		}

		return exitRuntime
	}
	defer cleanup()

	logStartup(c)

	if c.Config.Transport == config.TransportHTTP {
		return serveHTTP(ctx, c)
	}

	return serveStdio(ctx, c)
}

// logStartup records what this process is, once, at Info. It never names the
// database: the DSN carries a password, and even its host is more than a log
// aggregator needs to know.
func logStartup(c *container.Container) {
	attrs := []any{"version", version, "transport", string(c.Config.Transport)}
	if c.Config.Transport == config.TransportHTTP {
		attrs = append(attrs, "listen", c.Config.Listen, "auth_mode", string(c.Config.AuthMode))
	}
	attrs = append(attrs, "query_enabled", !c.Config.DisableQuery)

	c.Log.Info("pgmcp started", attrs...)
}

// serveStdio runs the MCP server over stdin/stdout. It returns when the
// client closes the connection or a signal cancels ctx; a cancelled context
// is how this process is asked to stop, so it is a clean exit, not a failure.
func serveStdio(ctx context.Context, c *container.Container) int {
	if err := c.MCP.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		c.Log.Error("stdio transport failed", "error", err)

		return exitRuntime
	}

	c.Log.Info("pgmcp stopped")

	return exitOK
}

// serveHTTP runs the Streamable HTTP transport until the listener fails or a
// signal cancels ctx, then drains in-flight requests within shutdownTimeout.
// The drain deliberately runs on a fresh context: ctx is already cancelled by
// the time we get here, and shutting down with it would abort exactly the
// requests the drain exists to finish.
func serveHTTP(ctx context.Context, c *container.Container) int {
	srv := httpserver.NewServer(c.Config.Listen, c.HTTP)

	served := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		served <- err
	}()

	select {
	case err := <-served:
		if err != nil {
			c.Log.Error("http transport failed", "error", err)

			return exitRuntime
		}

		return exitOK
	case <-ctx.Done():
	}

	c.Log.Info("shutting down", "timeout", shutdownTimeout.String())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		c.Log.Error("http shutdown failed", "error", err)

		return exitRuntime
	}

	if err := <-served; err != nil {
		c.Log.Error("http transport failed", "error", err)

		return exitRuntime
	}

	c.Log.Info("pgmcp stopped")

	return exitOK
}

// stripVersionFlag reports whether a version flag appears anywhere in args
// and returns args without it. It is handled before configuration is loaded
// so that `pgmcp --transport http --version` answers rather than complaining
// about a missing database URL.
func stripVersionFlag(args []string) ([]string, bool) {
	rest := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		if a == "--version" || a == "-version" {
			found = true

			continue
		}
		rest = append(rest, a)
	}

	return rest, found
}
