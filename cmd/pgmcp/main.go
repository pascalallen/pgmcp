// Command pgmcp is a read-only Postgres ops/DBA MCP server.
package main

import (
	"fmt"
	"os"

	"github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/config"
	"github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/logger"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-version") {
		fmt.Println("pgmcp " + version)
		return
	}

	cfg, err := config.Load(args, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	log := logger.New(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	log.Info("pgmcp configured", "transport", cfg.Transport)
}
