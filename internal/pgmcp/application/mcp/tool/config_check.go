package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// defaultConfigCheckCategory is the category applied when the caller asks for
// none: every heuristic the domain knows.
const defaultConfigCheckCategory = diagnostics.CategoryAll

// configCheckCategories are the categories the tool accepts, in the order the
// description advertises them.
var configCheckCategories = []diagnostics.SettingsCategory{
	diagnostics.CategoryMemory,
	diagnostics.CategoryAutovacuum,
	diagnostics.CategoryWAL,
	diagnostics.CategoryConnections,
	diagnostics.CategoryAll,
}

// ConfigCheckIn is the input of the config_check tool.
type ConfigCheckIn struct {
	Category string `json:"category,omitempty" jsonschema:"which heuristics to run: memory, autovacuum, wal, connections or all (default all)"`
}

// ConfigCheckSummary counts the verdicts of one config_check report.
type ConfigCheckSummary struct {
	OK     int `json:"ok" jsonschema:"settings whose value looks reasonable"`
	Review int `json:"review" jsonschema:"settings worth a look, usually a default left in place"`
	Warn   int `json:"warn" jsonschema:"settings that risk data loss or unavailability"`
}

// ConfigCheckOut is the output of the config_check tool.
type ConfigCheckOut struct {
	Meta
	Checks  []diagnostics.SettingCheck `json:"checks" jsonschema:"one entry per assessed setting, sorted by name, each with a verdict of ok, review or warn and a note explaining it"`
	Summary ConfigCheckSummary         `json:"summary" jsonschema:"how many checks landed on each verdict"`
}

// ConfigCheck assesses the server configuration against tuning heuristics.
func ConfigCheck(d diagnostics.Diagnostics) (*mcp.Tool, mcp.ToolHandlerFor[ConfigCheckIn, ConfigCheckOut]) {
	tool := &mcp.Tool{
		Name:        "config_check",
		Description: "Assesses the server's pg_settings against tuning and safety heuristics for memory, autovacuum, WAL or connections, returning a verdict of ok, review or warn per setting with a note explaining it. Use it for tuning questions or a quick sanity check of an unfamiliar server. The verdicts are heuristics rather than measurements, so confirm against the workload before changing anything.",
		Annotations: readOnly("Configuration check"),
	}

	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in ConfigCheckIn) (*mcp.CallToolResult, ConfigCheckOut, error) {
		category, err := configCheckCategory(in.Category)
		if err != nil {
			return nil, ConfigCheckOut{}, err
		}

		settings, err := d.Settings(ctx)
		if err != nil {
			return nil, ConfigCheckOut{}, err
		}

		checks := diagnostics.CheckSettings(settings, category)
		if checks == nil {
			checks = []diagnostics.SettingCheck{}
		}

		out := ConfigCheckOut{Meta: newMeta(ctx, d), Checks: checks, Summary: summarize(checks)}

		return nil, out, nil
	}

	return tool, handler
}

// configCheckCategory resolves the requested category, defaulting when it is
// absent and rejecting anything outside the set so the model is told what it
// may ask for instead of silently getting a different report.
func configCheckCategory(requested string) (diagnostics.SettingsCategory, error) {
	if requested == "" {
		return defaultConfigCheckCategory, nil
	}

	for _, category := range configCheckCategories {
		if diagnostics.SettingsCategory(requested) == category {
			return category, nil
		}
	}

	allowed := make([]string, 0, len(configCheckCategories))
	for _, category := range configCheckCategories {
		allowed = append(allowed, string(category))
	}

	return "", fmt.Errorf("config_check: unsupported category %q; expected one of %s", requested, strings.Join(allowed, ", "))
}

// summarize counts how many checks landed on each verdict, so a model can see
// the shape of the report before reading it.
func summarize(checks []diagnostics.SettingCheck) ConfigCheckSummary {
	summary := ConfigCheckSummary{}
	for _, check := range checks {
		switch check.Verdict {
		case diagnostics.VerdictWarn:
			summary.Warn++
		case diagnostics.VerdictReview:
			summary.Review++
		case diagnostics.VerdictOK:
			summary.OK++
		}
	}

	return summary
}
