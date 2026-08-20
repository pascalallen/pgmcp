package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testVersion is stamped into the test binary through the same -ldflags path
// a release build uses, so the smoke test proves the wiring, not the default.
const testVersion = "t13-test"

// buildBinary compiles the command under test into a temporary directory and
// returns its path. It builds with CGO disabled, as every pgmcp build does.
func buildBinary(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "pgmcp")
	build := exec.Command("go", "build", "-ldflags", "-X main.version="+testVersion, "-o", bin, ".")
	build.Env = append(build.Environ(), "CGO_ENABLED=0")

	out, err := build.CombinedOutput()
	require.NoError(t, err, "building pgmcp: %s", out)

	return bin
}

// exitCode returns the exit status of a finished command, and fails the test
// if err is anything other than a clean run or a non-zero exit.
func exitCode(t *testing.T, err error) int {
	t.Helper()

	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected an exit error, got %v", err)

	return exitErr.ExitCode()
}

func TestCommandStartup(t *testing.T) {
	bin := buildBinary(t)

	t.Run("it prints the build version and exits zero for --version", func(t *testing.T) {
		cmd := exec.Command(bin, "--version")
		cmd.Env = []string{}

		out, err := cmd.Output()

		require.NoError(t, err)
		assert.Equal(t, "pgmcp "+testVersion, strings.TrimSpace(string(out)))
	})

	t.Run("it answers --version even when other flags are present", func(t *testing.T) {
		cmd := exec.Command(bin, "--transport", "http", "--version", "--log-level", "debug")
		cmd.Env = []string{}

		out, err := cmd.Output()

		require.NoError(t, err)
		assert.Equal(t, "pgmcp "+testVersion, strings.TrimSpace(string(out)))
	})

	t.Run("it exits two with a configuration error when the database url is missing", func(t *testing.T) {
		cmd := exec.Command(bin)
		cmd.Env = []string{}

		var stderr strings.Builder
		cmd.Stderr = &stderr

		err := cmd.Run()

		assert.Equal(t, 2, exitCode(t, err))
		assert.Contains(t, stderr.String(), "DATABASE_URL is required")
		assert.NotContains(t, stderr.String(), "panic:")
	})

	t.Run("it exits two with a configuration error when a flag value is invalid", func(t *testing.T) {
		cmd := exec.Command(bin, "--transport", "carrier-pigeon", "--database-url", "postgres://x/y")
		cmd.Env = []string{}

		var stderr strings.Builder
		cmd.Stderr = &stderr

		err := cmd.Run()

		assert.Equal(t, 2, exitCode(t, err))
		assert.Contains(t, stderr.String(), "TRANSPORT must be")
		assert.NotContains(t, stderr.String(), "panic:")
	})
}

func TestStripVersionFlag(t *testing.T) {
	t.Run("it reports no version flag and leaves the arguments untouched", func(t *testing.T) {
		rest, found := stripVersionFlag([]string{"--transport", "http"})

		assert.False(t, found)
		assert.Equal(t, []string{"--transport", "http"}, rest)
	})

	t.Run("it finds a version flag in any position and removes it", func(t *testing.T) {
		rest, found := stripVersionFlag([]string{"--transport", "http", "--version", "--log-level", "debug"})

		assert.True(t, found)
		assert.Equal(t, []string{"--transport", "http", "--log-level", "debug"}, rest)
	})

	t.Run("it accepts the single-dash spelling", func(t *testing.T) {
		rest, found := stripVersionFlag([]string{"-version"})

		assert.True(t, found)
		assert.Empty(t, rest)
	})
}
