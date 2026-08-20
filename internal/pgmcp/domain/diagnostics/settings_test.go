package diagnostics_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func setting(name, value, unit string) diagnostics.Setting {
	return diagnostics.Setting{Name: name, Value: value, Unit: unit, Source: "configuration file"}
}

// checkFor runs CheckSettings over every category and returns the check for name.
func checkFor(t *testing.T, name string, settings ...diagnostics.Setting) diagnostics.SettingCheck {
	t.Helper()

	for _, c := range diagnostics.CheckSettings(settings, diagnostics.CategoryAll) {
		if c.Name == name {
			return c
		}
	}

	t.Fatalf("no check produced for setting %q", name)

	return diagnostics.SettingCheck{}
}

func TestCheckSettingsMemory(t *testing.T) {
	t.Run("flags shared_buffers below 128MB and accepts 128MB", func(t *testing.T) {
		small := checkFor(t, "shared_buffers", setting("shared_buffers", "1024", "8kB"))
		assert.Equal(t, diagnostics.VerdictReview, small.Verdict)
		assert.NotEmpty(t, small.Note)

		fine := checkFor(t, "shared_buffers", setting("shared_buffers", "16384", "8kB"))
		assert.Equal(t, diagnostics.VerdictOK, fine.Verdict)
		assert.Empty(t, fine.Note)
	})

	t.Run("flags work_mem that multiplied by max_connections may exceed RAM", func(t *testing.T) {
		risky := checkFor(t, "work_mem",
			setting("work_mem", "65536", "kB"),
			setting("max_connections", "500", ""),
			setting("shared_buffers", "16384", "8kB"),
		)
		assert.Equal(t, diagnostics.VerdictReview, risky.Verdict)
		assert.Equal(t, "work_mem * max_connections may exceed RAM", risky.Note)

		fine := checkFor(t, "work_mem",
			setting("work_mem", "4096", "kB"),
			setting("max_connections", "100", ""),
			setting("shared_buffers", "1048576", "8kB"),
		)
		assert.Equal(t, diagnostics.VerdictOK, fine.Verdict)
		assert.Empty(t, fine.Note)
	})

	t.Run("skips the work_mem rule when max_connections is not reported", func(t *testing.T) {
		fine := checkFor(t, "work_mem", setting("work_mem", "65536", "kB"))

		assert.Equal(t, diagnostics.VerdictOK, fine.Verdict)
	})

	t.Run("flags maintenance_work_mem below 64MB", func(t *testing.T) {
		small := checkFor(t, "maintenance_work_mem", setting("maintenance_work_mem", "32768", "kB"))
		assert.Equal(t, diagnostics.VerdictReview, small.Verdict)

		fine := checkFor(t, "maintenance_work_mem", setting("maintenance_work_mem", "65536", "kB"))
		assert.Equal(t, diagnostics.VerdictOK, fine.Verdict)
	})

	t.Run("flags effective_cache_size that does not exceed shared_buffers", func(t *testing.T) {
		small := checkFor(t, "effective_cache_size",
			setting("effective_cache_size", "16384", "8kB"),
			setting("shared_buffers", "16384", "8kB"),
		)
		assert.Equal(t, diagnostics.VerdictReview, small.Verdict)

		fine := checkFor(t, "effective_cache_size",
			setting("effective_cache_size", "524288", "8kB"),
			setting("shared_buffers", "16384", "8kB"),
		)
		assert.Equal(t, diagnostics.VerdictOK, fine.Verdict)
	})

	t.Run("flags planner costs left at their spinning disk defaults", func(t *testing.T) {
		rpc := checkFor(t, "random_page_cost", setting("random_page_cost", "4", ""))
		assert.Equal(t, diagnostics.VerdictReview, rpc.Verdict)
		assert.Equal(t, "4.0 is the HDD default; SSD is closer to 1.1", rpc.Note)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "random_page_cost", setting("random_page_cost", "1.1", "")).Verdict)

		eio := checkFor(t, "effective_io_concurrency", setting("effective_io_concurrency", "1", ""))
		assert.Equal(t, diagnostics.VerdictReview, eio.Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "effective_io_concurrency", setting("effective_io_concurrency", "200", "")).Verdict)
	})

	t.Run("notes jit only when work_mem is small", func(t *testing.T) {
		noted := checkFor(t, "jit", setting("jit", "on", ""), setting("work_mem", "1024", "kB"))
		assert.Equal(t, diagnostics.VerdictOK, noted.Verdict)
		assert.NotEmpty(t, noted.Note)

		quiet := checkFor(t, "jit", setting("jit", "on", ""), setting("work_mem", "65536", "kB"))
		assert.Equal(t, diagnostics.VerdictOK, quiet.Verdict)
		assert.Empty(t, quiet.Note)

		off := checkFor(t, "jit", setting("jit", "off", ""), setting("work_mem", "1024", "kB"))
		assert.Empty(t, off.Note)
	})

	t.Run("flags observability settings that hide slow queries", func(t *testing.T) {
		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "log_min_duration_statement", setting("log_min_duration_statement", "-1", "ms")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK,
			checkFor(t, "log_min_duration_statement", setting("log_min_duration_statement", "1000", "ms")).Verdict)

		assert.Equal(t, diagnostics.VerdictReview, checkFor(t, "track_io_timing", setting("track_io_timing", "off", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "track_io_timing", setting("track_io_timing", "on", "")).Verdict)

		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "shared_preload_libraries", setting("shared_preload_libraries", "pg_cron", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK,
			checkFor(t, "shared_preload_libraries", setting("shared_preload_libraries", "pg_cron,pg_stat_statements", "")).Verdict)
	})
}

func TestCheckSettingsConnections(t *testing.T) {
	t.Run("flags max_connections above 500 and suggests a pooler", func(t *testing.T) {
		many := checkFor(t, "max_connections", setting("max_connections", "600", ""))
		assert.Equal(t, diagnostics.VerdictReview, many.Verdict)
		assert.Contains(t, many.Note, "pooler")

		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "max_connections", setting("max_connections", "100", "")).Verdict)
	})

	t.Run("flags disabled timeouts", func(t *testing.T) {
		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "idle_in_transaction_session_timeout", setting("idle_in_transaction_session_timeout", "0", "ms")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK,
			checkFor(t, "idle_in_transaction_session_timeout", setting("idle_in_transaction_session_timeout", "60000", "ms")).Verdict)

		assert.Equal(t, diagnostics.VerdictReview, checkFor(t, "statement_timeout", setting("statement_timeout", "0", "ms")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "statement_timeout", setting("statement_timeout", "30000", "ms")).Verdict)
	})

	t.Run("warns about weak password encryption and flags disabled ssl", func(t *testing.T) {
		weak := checkFor(t, "password_encryption", setting("password_encryption", "md5", ""))
		assert.Equal(t, diagnostics.VerdictWarn, weak.Verdict)
		assert.Equal(t, diagnostics.VerdictOK,
			checkFor(t, "password_encryption", setting("password_encryption", "scram-sha-256", "")).Verdict)

		assert.Equal(t, diagnostics.VerdictReview, checkFor(t, "ssl", setting("ssl", "off", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "ssl", setting("ssl", "on", "")).Verdict)
	})
}

func TestCheckSettingsAutovacuum(t *testing.T) {
	t.Run("warns when autovacuum is off", func(t *testing.T) {
		off := checkFor(t, "autovacuum", setting("autovacuum", "off", ""))
		assert.Equal(t, diagnostics.VerdictWarn, off.Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "autovacuum", setting("autovacuum", "on", "")).Verdict)
	})

	t.Run("flags lazy scale factors", func(t *testing.T) {
		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "autovacuum_vacuum_scale_factor", setting("autovacuum_vacuum_scale_factor", "0.2", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK,
			checkFor(t, "autovacuum_vacuum_scale_factor", setting("autovacuum_vacuum_scale_factor", "0.05", "")).Verdict)

		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "autovacuum_analyze_scale_factor", setting("autovacuum_analyze_scale_factor", "0.1", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK,
			checkFor(t, "autovacuum_analyze_scale_factor", setting("autovacuum_analyze_scale_factor", "0.02", "")).Verdict)
	})

	t.Run("flags too few workers and a long naptime", func(t *testing.T) {
		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "autovacuum_max_workers", setting("autovacuum_max_workers", "2", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK,
			checkFor(t, "autovacuum_max_workers", setting("autovacuum_max_workers", "3", "")).Verdict)

		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "autovacuum_naptime", setting("autovacuum_naptime", "600", "s")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK,
			checkFor(t, "autovacuum_naptime", setting("autovacuum_naptime", "60", "s")).Verdict)
	})
}

func TestCheckSettingsWAL(t *testing.T) {
	t.Run("flags wal_level minimal", func(t *testing.T) {
		minimal := checkFor(t, "wal_level", setting("wal_level", "minimal", ""))
		assert.Equal(t, diagnostics.VerdictReview, minimal.Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "wal_level", setting("wal_level", "replica", "")).Verdict)
	})

	t.Run("flags checkpoint settings that concentrate io", func(t *testing.T) {
		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "checkpoint_completion_target", setting("checkpoint_completion_target", "0.5", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK,
			checkFor(t, "checkpoint_completion_target", setting("checkpoint_completion_target", "0.9", "")).Verdict)

		assert.Equal(t, diagnostics.VerdictReview, checkFor(t, "max_wal_size", setting("max_wal_size", "512", "MB")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "max_wal_size", setting("max_wal_size", "1024", "MB")).Verdict)
	})

	t.Run("flags durability settings that trade safety for speed", func(t *testing.T) {
		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "synchronous_commit", setting("synchronous_commit", "off", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "synchronous_commit", setting("synchronous_commit", "on", "")).Verdict)

		assert.Equal(t, diagnostics.VerdictWarn, checkFor(t, "fsync", setting("fsync", "off", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "fsync", setting("fsync", "on", "")).Verdict)

		assert.Equal(t, diagnostics.VerdictWarn, checkFor(t, "full_page_writes", setting("full_page_writes", "off", "")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "full_page_writes", setting("full_page_writes", "on", "")).Verdict)
	})
}

func TestCheckSettings(t *testing.T) {
	all := []diagnostics.Setting{
		setting("shared_buffers", "16384", "8kB"),
		setting("work_mem", "4096", "kB"),
		setting("autovacuum", "on", ""),
		setting("wal_level", "replica", ""),
		setting("max_connections", "100", ""),
		setting("ssl", "on", ""),
	}

	t.Run("category filter returns only that category", func(t *testing.T) {
		checks := diagnostics.CheckSettings(all, diagnostics.CategoryWAL)

		require.Len(t, checks, 1)
		assert.Equal(t, "wal_level", checks[0].Name)
	})

	t.Run("category all returns every consulted setting", func(t *testing.T) {
		checks := diagnostics.CheckSettings(all, diagnostics.CategoryAll)

		names := make([]string, 0, len(checks))
		for _, c := range checks {
			names = append(names, c.Name)
		}
		assert.ElementsMatch(t, []string{"shared_buffers", "work_mem", "autovacuum", "wal_level", "max_connections", "ssl"}, names)
	})

	t.Run("unknown setting is ignored", func(t *testing.T) {
		checks := diagnostics.CheckSettings([]diagnostics.Setting{setting("lc_messages", "en_US.UTF-8", "")}, diagnostics.CategoryAll)

		require.NotNil(t, checks)
		assert.Empty(t, checks)
	})

	t.Run("unknown category returns nothing", func(t *testing.T) {
		checks := diagnostics.CheckSettings(all, diagnostics.SettingsCategory("planner"))

		require.NotNil(t, checks)
		assert.Empty(t, checks)
	})

	t.Run("output is sorted by name", func(t *testing.T) {
		checks := diagnostics.CheckSettings(all, diagnostics.CategoryAll)

		names := make([]string, 0, len(checks))
		for _, c := range checks {
			names = append(names, c.Name)
		}
		assert.True(t, sort.StringsAreSorted(names), "expected sorted names, got %v", names)
	})

	t.Run("carries value unit and source through to the check", func(t *testing.T) {
		checks := diagnostics.CheckSettings([]diagnostics.Setting{setting("shared_buffers", "16384", "8kB")}, diagnostics.CategoryMemory)

		require.Len(t, checks, 1)
		assert.Equal(t, "16384", checks[0].Value)
		assert.Equal(t, "8kB", checks[0].Unit)
		assert.Equal(t, "configuration file", checks[0].Source)
	})

	t.Run("an unparsable value is skipped", func(t *testing.T) {
		checks := diagnostics.CheckSettings([]diagnostics.Setting{setting("shared_buffers", "lots", "8kB")}, diagnostics.CategoryAll)

		assert.Empty(t, checks)
	})
}

func TestCheckSettingsUnits(t *testing.T) {
	t.Run("reads byte units including the 8kB block size", func(t *testing.T) {
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "max_wal_size", setting("max_wal_size", "2", "GB")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "max_wal_size", setting("max_wal_size", "1048576", "kB")).Verdict)
		assert.Equal(t, diagnostics.VerdictReview, checkFor(t, "max_wal_size", setting("max_wal_size", "524288", "kB")).Verdict)
		assert.Equal(t, diagnostics.VerdictReview,
			checkFor(t, "shared_buffers", setting("shared_buffers", "134217727", "B")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "shared_buffers", setting("shared_buffers", "16384", "8kB")).Verdict)
	})

	t.Run("reads duration units", func(t *testing.T) {
		assert.Equal(t, diagnostics.VerdictReview, checkFor(t, "autovacuum_naptime", setting("autovacuum_naptime", "10", "min")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "autovacuum_naptime", setting("autovacuum_naptime", "1", "min")).Verdict)
		assert.Equal(t, diagnostics.VerdictOK, checkFor(t, "statement_timeout", setting("statement_timeout", "5000", "")).Verdict)
	})

	t.Run("skips a setting whose unit is not understood", func(t *testing.T) {
		checks := diagnostics.CheckSettings([]diagnostics.Setting{setting("shared_buffers", "16384", "pages")}, diagnostics.CategoryAll)

		assert.Empty(t, checks)
	})

	t.Run("skips a boolean setting with an unexpected value", func(t *testing.T) {
		checks := diagnostics.CheckSettings([]diagnostics.Setting{setting("fsync", "maybe", "")}, diagnostics.CategoryAll)

		assert.Empty(t, checks)
	})
}
