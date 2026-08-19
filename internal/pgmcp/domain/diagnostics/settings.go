package diagnostics

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// SettingsCategory selects which group of setting heuristics to run.
type SettingsCategory string

// The categories accepted by CheckSettings.
const (
	CategoryAll         SettingsCategory = "all"
	CategoryMemory      SettingsCategory = "memory"
	CategoryAutovacuum  SettingsCategory = "autovacuum"
	CategoryWAL         SettingsCategory = "wal"
	CategoryConnections SettingsCategory = "connections"
)

// The verdicts a setting heuristic can return.
const (
	VerdictOK     Verdict = "ok"
	VerdictReview Verdict = "review"
	VerdictWarn   Verdict = "warn"
)

// Byte and duration thresholds shared by the setting heuristics.
const (
	kilobyte = int64(1024)
	megabyte = 1024 * kilobyte
	gigabyte = 1024 * megabyte
)

// settingRule assesses one pg_settings row against the rest of the configuration.
// It returns false when the value cannot be judged, which drops the setting from
// the report rather than guessing.
type settingRule struct {
	name     string
	category SettingsCategory
	assess   func(s Setting, all map[string]Setting) (Verdict, string, bool)
}

// settingRules is the full heuristic table, one rule per consulted setting.
var settingRules = []settingRule{
	{"shared_buffers", CategoryMemory, checkSharedBuffers},
	{"work_mem", CategoryMemory, checkWorkMem},
	{"maintenance_work_mem", CategoryMemory, checkMaintenanceWorkMem},
	{"effective_cache_size", CategoryMemory, checkEffectiveCacheSize},
	{"random_page_cost", CategoryMemory, checkRandomPageCost},
	{"effective_io_concurrency", CategoryMemory, checkEffectiveIOConcurrency},
	{"jit", CategoryMemory, checkJIT},
	{"track_io_timing", CategoryMemory, checkTrackIOTiming},
	{"log_min_duration_statement", CategoryMemory, checkLogMinDurationStatement},
	{"shared_preload_libraries", CategoryMemory, checkSharedPreloadLibraries},
	{"max_connections", CategoryConnections, checkMaxConnections},
	{"idle_in_transaction_session_timeout", CategoryConnections, checkIdleInTransactionTimeout},
	{"statement_timeout", CategoryConnections, checkStatementTimeout},
	{"ssl", CategoryConnections, checkSSL},
	{"password_encryption", CategoryConnections, checkPasswordEncryption},
	{"autovacuum", CategoryAutovacuum, checkAutovacuum},
	{"autovacuum_vacuum_scale_factor", CategoryAutovacuum, checkAutovacuumVacuumScaleFactor},
	{"autovacuum_analyze_scale_factor", CategoryAutovacuum, checkAutovacuumAnalyzeScaleFactor},
	{"autovacuum_max_workers", CategoryAutovacuum, checkAutovacuumMaxWorkers},
	{"autovacuum_naptime", CategoryAutovacuum, checkAutovacuumNaptime},
	{"wal_level", CategoryWAL, checkWALLevel},
	{"checkpoint_completion_target", CategoryWAL, checkCheckpointCompletionTarget},
	{"max_wal_size", CategoryWAL, checkMaxWALSize},
	{"synchronous_commit", CategoryWAL, checkSynchronousCommit},
	{"fsync", CategoryWAL, checkFsync},
	{"full_page_writes", CategoryWAL, checkFullPageWrites},
}

// CheckSettings applies the heuristics of the requested category to the settings
// it recognises, sorted by name. Settings with no rule — and rules whose value or
// dependencies cannot be parsed — are left out. A rule that passes yields an "ok"
// verdict with an empty note. The result is never nil.
func CheckSettings(settings []Setting, category SettingsCategory) []SettingCheck {
	all := make(map[string]Setting, len(settings))
	for _, s := range settings {
		all[s.Name] = s
	}

	checks := []SettingCheck{}
	for _, rule := range settingRules {
		if category != CategoryAll && rule.category != category {
			continue
		}
		s, ok := all[rule.name]
		if !ok {
			continue
		}
		verdict, note, judged := rule.assess(s, all)
		if !judged {
			continue
		}
		checks = append(checks, SettingCheck{
			Name:    s.Name,
			Value:   s.Value,
			Unit:    s.Unit,
			Source:  s.Source,
			Verdict: verdict,
			Note:    note,
		})
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })

	return checks
}

// verdictWhen returns review/warn with note when bad holds, and ok otherwise.
func verdictWhen(bad bool, verdict Verdict, note string) (Verdict, string, bool) {
	if bad {
		return verdict, note, true
	}

	return VerdictOK, "", true
}

func checkSharedBuffers(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	bytes, ok := parseSettingBytes(s.Value, s.Unit)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(bytes < 128*megabyte, VerdictReview,
		"shared_buffers below 128MB is small for a dedicated server; about 25% of RAM is the usual starting point")
}

func checkWorkMem(s Setting, all map[string]Setting) (Verdict, string, bool) {
	workMem, ok := parseSettingBytes(s.Value, s.Unit)
	if !ok {
		return VerdictOK, "", false
	}
	maxConns, connsOK := settingInt(all, "max_connections")
	sharedBuffers, buffersOK := settingBytes(all, "shared_buffers")
	if !connsOK || !buffersOK {
		return VerdictOK, "", true
	}

	return verdictWhen(workMem*maxConns > sharedBuffers*4/2, VerdictReview,
		"work_mem * max_connections may exceed RAM")
}

func checkMaintenanceWorkMem(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	bytes, ok := parseSettingBytes(s.Value, s.Unit)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(bytes < 64*megabyte, VerdictReview,
		"maintenance_work_mem below 64MB slows VACUUM and index builds")
}

func checkEffectiveCacheSize(s Setting, all map[string]Setting) (Verdict, string, bool) {
	cache, ok := parseSettingBytes(s.Value, s.Unit)
	if !ok {
		return VerdictOK, "", false
	}
	sharedBuffers, buffersOK := settingBytes(all, "shared_buffers")
	if !buffersOK {
		return VerdictOK, "", true
	}

	return verdictWhen(cache <= sharedBuffers, VerdictReview,
		"effective_cache_size should exceed shared_buffers; it models the operating system cache too")
}

func checkRandomPageCost(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	cost, err := strconv.ParseFloat(s.Value, 64)
	if err != nil {
		return VerdictOK, "", false
	}

	return verdictWhen(cost == 4, VerdictReview, "4.0 is the HDD default; SSD is closer to 1.1")
}

func checkEffectiveIOConcurrency(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	concurrency, err := strconv.ParseInt(s.Value, 10, 64)
	if err != nil {
		return VerdictOK, "", false
	}

	return verdictWhen(concurrency == 1, VerdictReview,
		"effective_io_concurrency of 1 leaves SSD and RAID read-ahead unused")
}

func checkJIT(s Setting, all map[string]Setting) (Verdict, string, bool) {
	on, ok := parseSettingBool(s.Value)
	if !ok {
		return VerdictOK, "", false
	}
	workMem, workMemOK := settingBytes(all, "work_mem")
	if on && workMemOK && workMem < 4*megabyte {
		return VerdictOK, "jit is on while work_mem is small; short queries can spend more time compiling than executing", true
	}

	return VerdictOK, "", true
}

func checkTrackIOTiming(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	on, ok := parseSettingBool(s.Value)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(!on, VerdictReview, "track_io_timing is off, so per-query I/O timings are unavailable")
}

func checkLogMinDurationStatement(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	ms, err := strconv.ParseInt(s.Value, 10, 64)
	if err != nil {
		return VerdictOK, "", false
	}

	return verdictWhen(ms == -1, VerdictReview, "log_min_duration_statement of -1 logs no slow queries")
}

func checkSharedPreloadLibraries(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	return verdictWhen(!strings.Contains(s.Value, "pg_stat_statements"), VerdictReview,
		"shared_preload_libraries does not load pg_stat_statements, so query statistics are unavailable")
}

func checkMaxConnections(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	conns, err := strconv.ParseInt(s.Value, 10, 64)
	if err != nil {
		return VerdictOK, "", false
	}

	return verdictWhen(conns > 500, VerdictReview,
		"max_connections above 500 usually calls for a connection pooler instead")
}

func checkIdleInTransactionTimeout(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	timeout, ok := parseSettingDuration(s.Value, s.Unit)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(timeout == 0, VerdictReview,
		"idle_in_transaction_session_timeout of 0 lets abandoned transactions hold locks indefinitely")
}

func checkStatementTimeout(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	timeout, ok := parseSettingDuration(s.Value, s.Unit)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(timeout == 0, VerdictReview, "statement_timeout of 0 lets a runaway query run forever")
}

func checkSSL(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	on, ok := parseSettingBool(s.Value)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(!on, VerdictReview, "ssl is off, so client connections are unencrypted")
}

func checkPasswordEncryption(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	return verdictWhen(s.Value != "scram-sha-256", VerdictWarn,
		"password_encryption is not scram-sha-256")
}

func checkAutovacuum(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	on, ok := parseSettingBool(s.Value)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(!on, VerdictWarn, "autovacuum is off; dead tuples and transaction age will accumulate")
}

func checkAutovacuumVacuumScaleFactor(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	factor, err := strconv.ParseFloat(s.Value, 64)
	if err != nil {
		return VerdictOK, "", false
	}

	return verdictWhen(factor >= 0.2, VerdictReview,
		"autovacuum_vacuum_scale_factor of 0.2 lets large tables accumulate dead tuples before a vacuum")
}

func checkAutovacuumAnalyzeScaleFactor(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	factor, err := strconv.ParseFloat(s.Value, 64)
	if err != nil {
		return VerdictOK, "", false
	}

	return verdictWhen(factor >= 0.1, VerdictReview,
		"autovacuum_analyze_scale_factor of 0.1 delays statistics refresh on large tables")
}

func checkAutovacuumMaxWorkers(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	workers, err := strconv.ParseInt(s.Value, 10, 64)
	if err != nil {
		return VerdictOK, "", false
	}

	return verdictWhen(workers < 3, VerdictReview,
		"fewer than 3 autovacuum workers can leave busy tables unvacuumed")
}

func checkAutovacuumNaptime(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	naptime, ok := parseSettingDuration(s.Value, s.Unit)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(naptime > 5*time.Minute, VerdictReview,
		"autovacuum_naptime above 5min delays vacuum scheduling")
}

func checkWALLevel(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	return verdictWhen(s.Value == "minimal", VerdictReview,
		"wal_level=minimal rules out replication and point-in-time recovery")
}

func checkCheckpointCompletionTarget(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	target, err := strconv.ParseFloat(s.Value, 64)
	if err != nil {
		return VerdictOK, "", false
	}

	return verdictWhen(target < 0.9, VerdictReview,
		"checkpoint_completion_target below 0.9 concentrates checkpoint I/O into a short window")
}

func checkMaxWALSize(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	size, ok := parseSettingBytes(s.Value, s.Unit)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(size < gigabyte, VerdictReview, "max_wal_size below 1GB forces frequent checkpoints")
}

func checkSynchronousCommit(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	return verdictWhen(s.Value == "off", VerdictReview,
		"synchronous_commit is off; recently committed transactions can be lost on a crash")
}

func checkFsync(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	on, ok := parseSettingBool(s.Value)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(!on, VerdictWarn, "fsync is off; a crash can corrupt the database beyond repair")
}

func checkFullPageWrites(s Setting, _ map[string]Setting) (Verdict, string, bool) {
	on, ok := parseSettingBool(s.Value)
	if !ok {
		return VerdictOK, "", false
	}

	return verdictWhen(!on, VerdictWarn, "full_page_writes is off; a crash can leave torn pages behind")
}

// settingBytes looks up a setting and parses it as a byte size.
func settingBytes(all map[string]Setting, name string) (int64, bool) {
	s, ok := all[name]
	if !ok {
		return 0, false
	}

	return parseSettingBytes(s.Value, s.Unit)
}

// settingInt looks up a setting and parses it as a plain integer.
func settingInt(all map[string]Setting, name string) (int64, bool) {
	s, ok := all[name]
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(s.Value, 10, 64)
	if err != nil {
		return 0, false
	}

	return n, true
}

// parseSettingBytes converts a pg_settings value and its unit ("B", "kB",
// "8kB", "MB", "GB", "TB", or empty for a raw byte count) into bytes.
func parseSettingBytes(value, unit string) (int64, bool) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	if unit == "" {
		return n, true
	}

	multiple, suffix := splitUnit(unit)
	scale, ok := map[string]int64{"B": 1, "kB": kilobyte, "MB": megabyte, "GB": gigabyte, "TB": 1024 * gigabyte}[suffix]
	if !ok {
		return 0, false
	}

	return n * multiple * scale, true
}

// parseSettingDuration converts a pg_settings value and its unit ("us", "ms",
// "s", "min", "h", "d", or empty for milliseconds) into a duration.
func parseSettingDuration(value, unit string) (time.Duration, bool) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	if unit == "" {
		return time.Duration(n) * time.Millisecond, true
	}

	multiple, suffix := splitUnit(unit)
	scale, ok := map[string]time.Duration{
		"us":  time.Microsecond,
		"ms":  time.Millisecond,
		"s":   time.Second,
		"min": time.Minute,
		"h":   time.Hour,
		"d":   24 * time.Hour,
	}[suffix]
	if !ok {
		return 0, false
	}

	return time.Duration(n*multiple) * scale, true
}

// splitUnit separates the optional leading multiplier of a unit such as "8kB".
func splitUnit(unit string) (int64, string) {
	end := 0
	for end < len(unit) && unit[end] >= '0' && unit[end] <= '9' {
		end++
	}
	if end == 0 {
		return 1, unit
	}
	multiple, err := strconv.ParseInt(unit[:end], 10, 64)
	if err != nil || multiple == 0 {
		return 1, unit[end:]
	}

	return multiple, unit[end:]
}

// parseSettingBool reads the on/off form pg_settings uses for boolean settings.
func parseSettingBool(value string) (bool, bool) {
	switch strings.ToLower(value) {
	case "on", "true", "yes", "1":
		return true, true
	case "off", "false", "no", "0":
		return false, true
	default:
		return false, false
	}
}
