// Package config parses and validates the operator configuration: analyzer
// settings, selection policy, execution profiles, and quota-pool topology.
// It is the single authority for what the operator can actually run.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/marcus/frost/internal/catalog"
	"github.com/marcus/frost/internal/router"
)

// SchemaVersion is the operator config schema this package reads.
const SchemaVersion = 1

// Defaults that apply when the file leaves a field unset.
const (
	DefaultProvider        = "typesafe"
	DefaultAPIKeyEnv       = "TYPESAFE_API_KEY"
	DefaultTimeoutSeconds  = 45
	LatencySuggestionsFile = "latency.suggestions.json"
)

// Config is the resolved operator configuration.
type Config struct {
	SchemaVersion int
	Path          string
	CatalogFile   string // absolute
	CapacityFile  string // absolute; optional default snapshot
	// CapacityFiles is every configured snapshot producer in precedence
	// order: capacity_file first, then capacity_files. All are absolute.
	CapacityFiles []string
	Analyzer      AnalyzerConfig
	Policy        router.Policy
	Profiles      []router.Profile
	Pools         []Pool
	Gateways      []Gateway
	// Generated describes the producer-written profiles file, when the config
	// names one. Its accepted profiles, pools, and gateways are already merged
	// into the slices above, each profile marked with Source "generated".
	Generated *Generated
	Hash      string // sha256 hex of the raw file bytes

	loadProblems     []Problem
	generatedPools   map[string]bool
	generatedGateway map[string]bool
}

// Gateway is a named OpenAI-compatible endpoint, such as a LiteLLM proxy,
// that serves the aliases gateway profiles name.
type Gateway struct {
	ID      string
	BaseURL string
}

// Generated reports what the producer-written profiles file contributed.
type Generated struct {
	Path        string
	Hash        string // sha256 hex of the raw bytes; empty when unread
	Producer    string
	GeneratedAt string
	Missing     bool
	Profiles    int // accepted by structural checks; Validate may still drop some
	Pools       int
	Gateways    int
}

// AnalyzerConfig names the judgment provider and its request settings.
type AnalyzerConfig struct {
	Provider       string
	Model          string
	QuestionsFile  string // absolute
	APIKeyEnv      string
	BaseURL        string
	TimeoutSeconds int
}

// Pool is the router's quota-pool declaration; the TOML shape converts to it.
type Pool = router.Pool

// Problem is one validation finding. Source is "generated" for findings about
// the producer-written profiles file and empty for the operator's own config.
type Problem struct {
	Severity string // error | warning | info
	Message  string
	Source   string `json:",omitempty"`
}

// LatencySuggestions is the optional producer output written beside a catalog.
// It remains advisory: the operator's profile configuration is authoritative.
type LatencySuggestions struct {
	SchemaVersion int                          `json:"schema_version"`
	Suggestions   map[string]LatencySuggestion `json:"suggestions"`
}

// LatencySuggestion is the producer's model-level prior, optionally refined
// for the effort variants reported by its source.
type LatencySuggestion struct {
	Class    string            `json:"class"`
	ByEffort map[string]string `json:"by_effort,omitempty"`
}

// Severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info" // an expected state, such as an operator override
)

// SourceGenerated marks a Problem about the producer-written profiles file.
const SourceGenerated = router.SourceGenerated

// HasErrors reports whether any problem is an error.
func HasErrors(ps []Problem) bool {
	for _, p := range ps {
		if p.Severity == SeverityError {
			return true
		}
	}
	return false
}

// File mirrors the TOML layout. Pointers distinguish unset from zero so
// defaults can fill in without clobbering an explicit value.
type fileConfig struct {
	SchemaVersion int      `toml:"schema_version"`
	CatalogFile   string   `toml:"catalog_file"`
	CapacityFile  string   `toml:"capacity_file"`
	CapacityFiles []string `toml:"capacity_files"`
	// GeneratedProfilesFile is a producer-written JSON file of profiles, pools,
	// and gateways merged beneath the operator's own declarations.
	GeneratedProfilesFile string        `toml:"generated_profiles_file"`
	Analyzer              fileAnalyzer  `toml:"analyzer"`
	Policy                filePolicy    `toml:"policy"`
	Gateways              []fileGateway `toml:"gateways"`
	Profiles              []fileProfile `toml:"profiles"`
	Pools                 []filePool    `toml:"pools"`
}

// fileGateway is shared by the TOML config and the generated JSON file.
type fileGateway struct {
	ID      string `toml:"id" json:"id"`
	BaseURL string `toml:"base_url" json:"base_url"`
}

type fileAnalyzer struct {
	Provider       string `toml:"provider"`
	Model          string `toml:"model"`
	QuestionsFile  string `toml:"questions_file"`
	APIKeyEnv      string `toml:"api_key_env"`
	BaseURL        string `toml:"base_url"`
	TimeoutSeconds *int   `toml:"timeout_seconds"`
}

type filePolicy struct {
	Mode                       string         `toml:"mode"`
	UncertainReasoningQuantile *float64       `toml:"uncertain_reasoning_quantile"`
	TaskFamilyMargin           *float64       `toml:"task_family_margin"`
	MissingContextThreshold    *float64       `toml:"missing_context_threshold"`
	CapabilityThreshold        *float64       `toml:"capability_threshold"`
	ConsequenceReviewThreshold *float64       `toml:"consequence_review_threshold"`
	HighConsequenceMinLevel    *int           `toml:"high_consequence_min_level"`
	LargeWorkloadThreshold     *float64       `toml:"large_workload_threshold"`
	EffortByLevel              []string       `toml:"effort_by_level"`
	RelaxedLevelReduction      *int           `toml:"relaxed_level_reduction"`
	PriorMaxQualityRankByLevel []int          `toml:"prior_max_quality_rank_by_level"`
	AdequacyRules              []fileAdequacy `toml:"adequacy_rules"`
	Capacity                   fileCapacity   `toml:"capacity"`
}

type fileCapacity struct {
	PreferExpiringIncludedUsage *bool    `toml:"prefer_expiring_included_usage"`
	ExpiryHorizonHours          *float64 `toml:"expiry_horizon_hours"`
	ReservePercent              *float64 `toml:"reserve_percent"`
	MaxSnapshotAgeMinutes       *float64 `toml:"max_snapshot_age_minutes"`
	AllowEstimatedMeasurements  *bool    `toml:"allow_estimated_measurements"`
	EnforceAvailability         *bool    `toml:"enforce_availability"`
}

type fileAdequacy struct {
	ID              string  `toml:"id"`
	TaskFamily      string  `toml:"task_family"`
	ReasoningLevels []int   `toml:"reasoning_levels"`
	Metric          string  `toml:"metric"`
	MetricVersion   string  `toml:"metric_version"`
	Minimum         float64 `toml:"minimum"`
	ProfileMatch    string  `toml:"profile_match"`
	OnMissing       string  `toml:"on_missing"`
}

type fileProfile struct {
	ID              string     `toml:"id" json:"id"`
	Label           string     `toml:"label" json:"label"`
	Model           string     `toml:"model" json:"model"`
	AccessSurface   string     `toml:"access_surface" json:"access_surface"`
	Enabled         *bool      `toml:"enabled" json:"enabled"`
	OutputContracts []string   `toml:"output_contracts" json:"output_contracts"`
	Capabilities    []string   `toml:"capabilities" json:"capabilities"`
	EffortMode      string     `toml:"effort_mode" json:"effort_mode"`
	NativeEffort    string     `toml:"native_effort" json:"native_effort"`
	EffortOptions   []string   `toml:"effort_options" json:"effort_options"`
	MappingVerified bool       `toml:"mapping_verified" json:"mapping_verified"`
	LatencyClass    string     `toml:"latency_class" json:"latency_class"`
	PoolIDs         []string   `toml:"pool_ids" json:"pool_ids"`
	Prior           *filePrior `toml:"prior" json:"prior"`
	Alias           string     `toml:"alias" json:"alias"`
	Gateway         string     `toml:"gateway" json:"gateway"`
	GatewayURL      string     `toml:"gateway_url" json:"gateway_url"`
}

type filePrior struct {
	QualityRank int `toml:"quality_rank" json:"quality_rank"`
	CostRank    int `toml:"cost_rank" json:"cost_rank"`
}

type filePool struct {
	ID                        string   `toml:"id" json:"id"`
	Kind                      string   `toml:"kind" json:"kind"`
	MarginalCost              string   `toml:"marginal_cost" json:"marginal_cost"`
	MappingVerified           bool     `toml:"mapping_verified" json:"mapping_verified"`
	RequiredWindowIDs         []string `toml:"required_window_ids" json:"required_window_ids"`
	ExpiryPreferenceWindowIDs []string `toml:"expiry_preference_window_ids" json:"expiry_preference_window_ids"`
	NonRolloverWindowIDs      []string `toml:"non_rollover_window_ids" json:"non_rollover_window_ids"`
	ExpiryHorizonHours        float64  `toml:"expiry_horizon_hours" json:"expiry_horizon_hours"`
}

// Resolve picks the operator config path: an explicit flag, FROST_CONFIG,
// then the XDG location. It errors, naming all three, when none exists.
func Resolve(explicit string, getenv func(string) string, home string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config %s: %w", explicit, err)
		}
		return explicit, nil
	}
	if env := getenv("FROST_CONFIG"); env != "" {
		if _, err := os.Stat(env); err != nil {
			return "", fmt.Errorf("FROST_CONFIG=%s: %w", env, err)
		}
		return env, nil
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	xdg := filepath.Join(base, "frost", "config.toml")
	if _, err := os.Stat(xdg); err == nil {
		return xdg, nil
	}
	return "", fmt.Errorf("no operator config: pass --config, set FROST_CONFIG, or create %s", xdg)
}

// Load reads and decodes a config file. Unknown keys are errors. Relative
// file references resolve against the config file's directory.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var f fileConfig
	dec := toml.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return nil, fmt.Errorf("config %s: %s", path, strings.TrimSpace(strict.String()))
		}
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if f.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("config %s: unsupported schema_version %d (want %d)", path, f.SchemaVersion, SchemaVersion)
	}
	dir := filepath.Dir(abs)
	sum := sha256.Sum256(raw)
	cfg := &Config{
		SchemaVersion: f.SchemaVersion,
		Path:          abs,
		CatalogFile:   resolveRel(dir, f.CatalogFile),
		CapacityFile:  resolveRel(dir, f.CapacityFile),
		Hash:          hex.EncodeToString(sum[:]),
	}
	cfg.Analyzer = AnalyzerConfig{
		Provider:       or(f.Analyzer.Provider, DefaultProvider),
		Model:          f.Analyzer.Model,
		QuestionsFile:  resolveRel(dir, f.Analyzer.QuestionsFile),
		APIKeyEnv:      or(f.Analyzer.APIKeyEnv, DefaultAPIKeyEnv),
		BaseURL:        f.Analyzer.BaseURL,
		TimeoutSeconds: DefaultTimeoutSeconds,
	}
	if f.Analyzer.TimeoutSeconds != nil {
		cfg.Analyzer.TimeoutSeconds = *f.Analyzer.TimeoutSeconds
	}
	cfg.Policy = mergePolicy(f.Policy)
	for _, p := range f.Profiles {
		cfg.Profiles = append(cfg.Profiles, toProfile(p))
	}
	for _, p := range f.Pools {
		cfg.Pools = append(cfg.Pools, toPool(p))
	}
	for _, g := range f.Gateways {
		cfg.Gateways = append(cfg.Gateways, Gateway(g))
	}
	if cfg.CapacityFile != "" {
		cfg.CapacityFiles = append(cfg.CapacityFiles, cfg.CapacityFile)
	}
	for _, p := range f.CapacityFiles {
		cfg.CapacityFiles = append(cfg.CapacityFiles, resolveRel(dir, p))
	}
	if f.GeneratedProfilesFile != "" {
		cfg.mergeGenerated(resolveRel(dir, f.GeneratedProfilesFile))
	}
	return cfg, nil
}

func toProfile(p fileProfile) router.Profile {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	rp := router.Profile{
		ID:              p.ID,
		Label:           p.Label,
		ModelID:         p.Model,
		AccessSurface:   p.AccessSurface,
		Enabled:         enabled,
		OutputContracts: p.OutputContracts,
		Capabilities:    p.Capabilities,
		EffortMode:      p.EffortMode,
		NativeEffort:    p.NativeEffort,
		EffortOptions:   p.EffortOptions,
		MappingVerified: p.MappingVerified,
		LatencyClass:    p.LatencyClass,
		PoolIDs:         p.PoolIDs,
		Alias:           p.Alias,
	}
	if p.Gateway != "" || p.GatewayURL != "" {
		rp.Gateway = &router.GatewayRef{ID: p.Gateway, BaseURL: p.GatewayURL}
	}
	if p.Prior != nil {
		rp.Prior = &router.Prior{QualityRank: p.Prior.QualityRank, CostRank: p.Prior.CostRank}
	}
	return rp
}

func toPool(p filePool) router.Pool {
	return router.Pool{
		ID:                        p.ID,
		Kind:                      or(p.Kind, router.PoolKindSubscription),
		MarginalCost:              or(p.MarginalCost, router.MarginalIncluded),
		MappingVerified:           p.MappingVerified,
		RequiredWindowIDs:         p.RequiredWindowIDs,
		ExpiryPreferenceWindowIDs: p.ExpiryPreferenceWindowIDs,
		NonRolloverWindowIDs:      p.NonRolloverWindowIDs,
		ExpiryHorizonHours:        p.ExpiryHorizonHours,
	}
}

// LoadLatencySuggestions reads the optional producer output next to the
// resolved catalog. A missing file is expected and returns (nil, path, nil).
func LoadLatencySuggestions(catalogPath string) (*LatencySuggestions, string, error) {
	path := filepath.Join(filepath.Dir(catalogPath), LatencySuggestionsFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, fmt.Errorf("read: %w", err)
	}
	var suggestions LatencySuggestions
	if err := json.Unmarshal(raw, &suggestions); err != nil {
		return nil, path, fmt.Errorf("decode JSON: %w", err)
	}
	if suggestions.SchemaVersion != 1 {
		return nil, path, fmt.Errorf("unsupported schema_version %d (want 1)", suggestions.SchemaVersion)
	}
	for modelID, suggestion := range suggestions.Suggestions {
		if !validLatencySuggestionClass(suggestion.Class) {
			return nil, path, fmt.Errorf("model %q has invalid class %q", modelID, suggestion.Class)
		}
		for effort, class := range suggestion.ByEffort {
			if !validLatencySuggestionClass(class) {
				return nil, path, fmt.Errorf("model %q effort %q has invalid class %q", modelID, effort, class)
			}
		}
	}
	return &suggestions, path, nil
}

// CheckLatencySuggestions reports profiles whose configured latency prior is
// more than one class faster than the producer's suggestion. Per-effort values
// take precedence when they apply to the profile.
func (c *Config) CheckLatencySuggestions(suggestions *LatencySuggestions) []Problem {
	if suggestions == nil {
		return nil
	}
	var problems []Problem
	for i, profile := range c.Profiles {
		suggestion, ok := suggestions.Suggestions[profile.ModelID]
		if !ok || profile.LatencyClass == "" {
			continue
		}
		where := fmt.Sprintf("profiles[%d]", i)
		if profile.ID != "" {
			where += " (" + profile.ID + ")"
		}
		warn := func(class, effort string) {
			if !latencyClassMismatch(profile.LatencyClass, class) {
				return
			}
			context := fmt.Sprintf("model %q", profile.ModelID)
			if effort != "" {
				context += fmt.Sprintf(" at effort %q", effort)
			}
			problems = append(problems, Problem{Severity: SeverityWarning, Message: fmt.Sprintf("%s: latency_class %q is more than one class faster than producer suggestion %q for %s", where, profile.LatencyClass, class, context)})
		}

		switch profile.EffortMode {
		case router.EffortFixed:
			if class, ok := suggestion.ByEffort[profile.NativeEffort]; ok {
				warn(class, profile.NativeEffort)
			} else {
				warn(suggestion.Class, "")
			}
		case router.EffortConfigurable:
			matched := false
			for _, effort := range profile.EffortOptions {
				if class, ok := suggestion.ByEffort[effort]; ok {
					matched = true
					warn(class, effort)
				}
			}
			if !matched {
				warn(suggestion.Class, "")
			}
		default:
			warn(suggestion.Class, "")
		}
	}
	return problems
}

func validLatencySuggestionClass(class string) bool {
	return class == "unknown" || slices.Contains(router.LatencyClasses, class)
}

func latencyClassMismatch(configured, suggested string) bool {
	configuredIndex := slices.Index(router.LatencyClasses, configured)
	suggestedIndex := slices.Index(router.LatencyClasses, suggested)
	return configuredIndex >= 0 && suggestedIndex-configuredIndex > 1
}

func mergePolicy(f filePolicy) router.Policy {
	p := router.DefaultPolicy()
	if f.Mode != "" {
		p.Mode = f.Mode
	}
	setF := func(dst *float64, src *float64) {
		if src != nil {
			*dst = *src
		}
	}
	setF(&p.UncertainReasoningQuantile, f.UncertainReasoningQuantile)
	setF(&p.TaskFamilyMargin, f.TaskFamilyMargin)
	setF(&p.MissingContextThreshold, f.MissingContextThreshold)
	setF(&p.CapabilityThreshold, f.CapabilityThreshold)
	setF(&p.ConsequenceReviewThreshold, f.ConsequenceReviewThreshold)
	setF(&p.LargeWorkloadThreshold, f.LargeWorkloadThreshold)
	if f.HighConsequenceMinLevel != nil {
		v := *f.HighConsequenceMinLevel
		p.HighConsequenceMinLevel = &v
	}
	if f.EffortByLevel != nil {
		p.EffortByLevel = f.EffortByLevel
	}
	if f.RelaxedLevelReduction != nil {
		p.RelaxedLevelReduction = *f.RelaxedLevelReduction
	}
	if f.PriorMaxQualityRankByLevel != nil {
		p.PriorMaxQualityRankByLevel = f.PriorMaxQualityRankByLevel
	}
	c := &p.Capacity
	if f.Capacity.PreferExpiringIncludedUsage != nil {
		c.PreferExpiringIncludedUsage = *f.Capacity.PreferExpiringIncludedUsage
	}
	setF(&c.ExpiryHorizonHours, f.Capacity.ExpiryHorizonHours)
	setF(&c.ReservePercent, f.Capacity.ReservePercent)
	setF(&c.MaxSnapshotAgeMinutes, f.Capacity.MaxSnapshotAgeMinutes)
	if f.Capacity.AllowEstimatedMeasurements != nil {
		c.AllowEstimatedMeasurements = *f.Capacity.AllowEstimatedMeasurements
	}
	if f.Capacity.EnforceAvailability != nil {
		c.EnforceAvailability = *f.Capacity.EnforceAvailability
	}
	for _, r := range f.AdequacyRules {
		p.AdequacyRules = append(p.AdequacyRules, router.AdequacyRule{
			ID:              r.ID,
			TaskFamily:      r.TaskFamily,
			ReasoningLevels: r.ReasoningLevels,
			Metric:          r.Metric,
			MetricVersion:   r.MetricVersion,
			Minimum:         r.Minimum,
			ProfileMatch:    or(r.ProfileMatch, "exact"),
			OnMissing:       or(r.OnMissing, "provisional_only"),
		})
	}
	return p
}

// Validate checks the config against the catalog. Aliases in profile model
// references are rewritten to canonical IDs. taskFamilyOptions and
// outputContractOptions come from the loaded question specification; a nil
// slice skips that check.
func (c *Config) Validate(cat router.Catalog, taskFamilyOptions []string, outputContractOptions []string) []Problem {
	ps := slices.Clone(c.loadProblems)
	errf := func(format string, args ...any) {
		ps = append(ps, Problem{Severity: SeverityError, Message: fmt.Sprintf(format, args...)})
	}
	warnf := func(format string, args ...any) {
		ps = append(ps, Problem{Severity: SeverityWarning, Message: fmt.Sprintf(format, args...)})
	}

	if c.Analyzer.Model == "" {
		errf("analyzer.model is required (pin an explicit version)")
	}
	if c.Analyzer.QuestionsFile == "" {
		errf("analyzer.questions_file is required")
	}
	if c.Analyzer.TimeoutSeconds <= 0 {
		errf("analyzer.timeout_seconds must be positive")
	}
	if c.CatalogFile == "" {
		errf("catalog_file is required")
	}

	pol := c.Policy
	if !slices.Contains([]string{router.ModeAdequate, router.ModeQuality, router.ModeFast, router.ModeRelaxed}, pol.Mode) {
		errf("policy.mode %q is not one of adequate, quality, fast, relaxed", pol.Mode)
	}
	unit := func(name string, v float64) {
		if v <= 0 || v > 1 {
			errf("policy.%s must be in (0, 1], got %g", name, v)
		}
	}
	unit("uncertain_reasoning_quantile", pol.UncertainReasoningQuantile)
	unit("missing_context_threshold", pol.MissingContextThreshold)
	unit("capability_threshold", pol.CapabilityThreshold)
	if pol.TaskFamilyMargin < 0 || pol.TaskFamilyMargin > 1 {
		errf("policy.task_family_margin must be in [0, 1], got %g", pol.TaskFamilyMargin)
	}
	if pol.ConsequenceReviewThreshold < 0 || pol.ConsequenceReviewThreshold > 3 {
		errf("policy.consequence_review_threshold must be in [0, 3], got %g", pol.ConsequenceReviewThreshold)
	}
	if pol.LargeWorkloadThreshold < 0 || pol.LargeWorkloadThreshold > 3 {
		errf("policy.large_workload_threshold must be in [0, 3], got %g", pol.LargeWorkloadThreshold)
	}
	if pol.HighConsequenceMinLevel != nil && (*pol.HighConsequenceMinLevel < 0 || *pol.HighConsequenceMinLevel >= router.ReasoningLevels) {
		errf("policy.high_consequence_min_level must be 0-%d", router.ReasoningLevels-1)
	}
	if pol.RelaxedLevelReduction < 0 || pol.RelaxedLevelReduction >= router.ReasoningLevels {
		errf("policy.relaxed_level_reduction must be 0-%d", router.ReasoningLevels-1)
	}
	if len(pol.EffortByLevel) != router.ReasoningLevels {
		errf("policy.effort_by_level needs %d entries, got %d", router.ReasoningLevels, len(pol.EffortByLevel))
	}
	for i, e := range pol.EffortByLevel {
		if !slices.Contains(router.EffortLadder, e) {
			errf("policy.effort_by_level[%d] %q is not on the effort ladder", i, e)
		}
	}
	if pol.PriorMaxQualityRankByLevel != nil && len(pol.PriorMaxQualityRankByLevel) != router.ReasoningLevels {
		errf("policy.prior_max_quality_rank_by_level needs %d entries, got %d", router.ReasoningLevels, len(pol.PriorMaxQualityRankByLevel))
	}
	ruleIDs := map[string]bool{}
	for i, r := range pol.AdequacyRules {
		where := fmt.Sprintf("policy.adequacy_rules[%d]", i)
		if r.ID == "" {
			errf("%s: id is required", where)
		} else if ruleIDs[r.ID] {
			errf("%s: duplicate id %q", where, r.ID)
		}
		ruleIDs[r.ID] = true
		if r.TaskFamily == "" {
			errf("%s: task_family is required", where)
		} else if taskFamilyOptions != nil && !slices.Contains(taskFamilyOptions, r.TaskFamily) {
			errf("%s: task_family %q is not offered by the question specification", where, r.TaskFamily)
		}
		if len(r.ReasoningLevels) == 0 {
			errf("%s: reasoning_levels must not be empty", where)
		}
		for _, l := range r.ReasoningLevels {
			if l < 0 || l >= router.ReasoningLevels {
				errf("%s: reasoning level %d is outside 0-%d", where, l, router.ReasoningLevels-1)
			}
		}
		if r.Metric == "" || r.MetricVersion == "" {
			errf("%s: metric and metric_version are required", where)
		}
		if r.ProfileMatch != "exact" && r.ProfileMatch != "any" {
			errf("%s: profile_match must be exact or any", where)
		}
		if !slices.Contains([]string{"provisional_only", "exclude", "unknown"}, r.OnMissing) {
			errf("%s: on_missing must be provisional_only, exclude, or unknown", where)
		}
	}

	cp := pol.Capacity
	if cp.ExpiryHorizonHours <= 0 {
		errf("policy.capacity.expiry_horizon_hours must be positive, got %g", cp.ExpiryHorizonHours)
	}
	if cp.MaxSnapshotAgeMinutes <= 0 {
		errf("policy.capacity.max_snapshot_age_minutes must be positive, got %g", cp.MaxSnapshotAgeMinutes)
	}
	if cp.ReservePercent < 0 || cp.ReservePercent > 50 {
		errf("policy.capacity.reserve_percent must be in [0, 50], got %g", cp.ReservePercent)
	}

	seenCapacity := map[string]bool{}
	for i, path := range c.CapacityFiles {
		switch {
		case path == "":
			errf("capacity_files[%d]: path is empty", i)
		case seenCapacity[path]:
			errf("capacity_files[%d]: %s is listed twice", i, path)
		}
		seenCapacity[path] = true
	}

	gateways := map[string]string{}
	var droppedGateways []string
	for i, g := range c.Gateways {
		start := len(ps)
		where := fmt.Sprintf("gateways[%d] (%s)", i, g.ID)
		if g.ID == "" {
			errf("gateways[%d]: id is required", i)
		} else if _, dup := gateways[g.ID]; dup {
			errf("%s: duplicate gateway id", where)
		}
		if err := checkBaseURL(g.BaseURL); err != nil {
			errf("%s: base_url %v", where, err)
		}
		if quarantine(&ps, start, c.generatedGateway[g.ID], "gateway", g.ID) {
			droppedGateways = append(droppedGateways, g.ID)
			continue
		}
		gateways[g.ID] = g.BaseURL
	}
	c.Gateways = slices.DeleteFunc(c.Gateways, func(g Gateway) bool { return slices.Contains(droppedGateways, g.ID) })

	poolIDs := map[string]bool{}
	var droppedPools []string
	for i, p := range c.Pools {
		start := len(ps)
		if p.ID == "" {
			errf("pools[%d]: id is required", i)
			continue
		}
		where := fmt.Sprintf("pools[%d] (%s)", i, p.ID)
		if poolIDs[p.ID] {
			errf("%s: duplicate pool id", where)
		}
		poolIDs[p.ID] = true
		if !slices.Contains([]string{router.PoolKindSubscription, router.PoolKindMetered, router.PoolKindPrepaid, router.PoolKindLocal}, p.Kind) {
			errf("%s: kind %q must be subscription, metered, prepaid, or local", where, p.Kind)
		}
		if p.ExpiryHorizonHours < 0 {
			errf("%s: expiry_horizon_hours must not be negative, got %g", where, p.ExpiryHorizonHours)
		}
		if p.MarginalCost != router.MarginalIncluded && p.MarginalCost != router.MarginalMetered {
			errf("%s: marginal_cost %q must be included or metered", where, p.MarginalCost)
		}
		if len(p.RequiredWindowIDs) == 0 {
			warnf("%s: no required_window_ids; the pool can never be known", where)
		}
		wseen := map[string]bool{}
		for _, w := range p.RequiredWindowIDs {
			if wseen[w] {
				errf("%s: duplicate window id %q", where, w)
			}
			wseen[w] = true
		}
		for _, w := range p.ExpiryPreferenceWindowIDs {
			if !slices.Contains(p.RequiredWindowIDs, w) {
				warnf("%s: expiry window %q is not a required window; it earns preference only when observed", where, w)
			}
		}
		if !p.MappingVerified {
			warnf("%s: mapping_verified is false; capacity is ignored for profiles bound to it", where)
		}
		if quarantine(&ps, start, c.generatedPools[p.ID], "pool", p.ID) {
			droppedPools = append(droppedPools, p.ID)
			delete(poolIDs, p.ID)
		}
	}
	c.Pools = slices.DeleteFunc(c.Pools, func(p Pool) bool { return slices.Contains(droppedPools, p.ID) })

	profileIDs := map[string]bool{}
	enabled := 0
	var unverified []string
	var droppedProfiles []string
	for i := range c.Profiles {
		p := &c.Profiles[i]
		start := len(ps)
		where := fmt.Sprintf("profiles[%d]", i)
		if p.ID == "" {
			errf("%s: id is required", where)
		} else {
			where += " (" + p.ID + ")"
			if profileIDs[p.ID] {
				errf("%s: duplicate profile id", where)
			}
			profileIDs[p.ID] = true
		}
		if p.AccessSurface == "" {
			errf("%s: access_surface is required", where)
		}
		c.checkGatewayProfile(p, where, gateways, errf)
		var model router.Model
		haveModel := false
		if p.ModelID == "" {
			errf("%s: model is required", where)
		} else if id, ok := catalog.Resolve(cat, p.ModelID); ok {
			p.ModelID = id
			model, haveModel = cat.Models[id], true
		} else {
			errf("%s: model %q is not in the catalog", where, p.ModelID)
		}
		if haveModel {
			for _, oc := range p.OutputContracts {
				if !slices.Contains(model.OutputContracts, oc) {
					errf("%s: output contract %q is not supported by model %s", where, oc, model.ID)
				}
			}
			if outputContractOptions != nil {
				for _, oc := range model.OutputContracts {
					if !slices.Contains(outputContractOptions, oc) && oc != router.ContractOther {
						warnf("%s: model contract %q is not an option in the question specification", where, oc)
					}
				}
			}
			if slices.Contains(p.Capabilities, router.CapImageInput) && !slices.Contains(model.InputModalities, "image") {
				warnf("%s: declares image_input but model %s lists no image input modality", where, model.ID)
			}
		}
		switch p.EffortMode {
		case router.EffortFixed:
			if p.NativeEffort == "" {
				errf("%s: effort_mode fixed requires native_effort", where)
			}
		case router.EffortConfigurable:
			if len(p.EffortOptions) == 0 {
				errf("%s: effort_mode configurable requires effort_options", where)
			}
			for _, o := range p.EffortOptions {
				if !slices.Contains(router.EffortLadder, o) {
					errf("%s: effort option %q is not on the effort ladder", where, o)
				}
			}
		case router.EffortNone:
		default:
			errf("%s: effort_mode %q must be fixed, configurable, or none", where, p.EffortMode)
		}
		if p.LatencyClass != "" && !slices.Contains(router.LatencyClasses, p.LatencyClass) {
			errf("%s: latency_class %q is not one of %s", where, p.LatencyClass, strings.Join(router.LatencyClasses, ", "))
		}
		for _, pid := range p.PoolIDs {
			if !poolIDs[pid] {
				errf("%s: pool %q is not declared", where, pid)
			}
		}
		if p.Enabled {
			if p.Prior == nil && !ruleCovers(pol.AdequacyRules, model) {
				warnf("%s: no operator prior and no measured rule coverage; adequacy will be unknown", where)
			}
		}
		if quarantine(&ps, start, p.Source == router.SourceGenerated, "profile", p.ID) {
			droppedProfiles = append(droppedProfiles, p.ID)
			continue
		}
		if p.Enabled {
			enabled++
			if !p.MappingVerified {
				unverified = append(unverified, p.ID)
			}
		}
	}
	c.Profiles = slices.DeleteFunc(c.Profiles, func(p router.Profile) bool {
		return p.Source == router.SourceGenerated && slices.Contains(droppedProfiles, p.ID)
	})
	if len(unverified) > 0 {
		warnf("%d enabled profiles have unverified native mappings (%s); their results stay provisional and no command is offered", len(unverified), strings.Join(unverified, ", "))
	}
	if enabled == 0 {
		warnf("no profile is enabled; every route will return no_match")
	}
	return ps
}

// ruleCovers reports whether any adequacy rule could be satisfied by one of
// the model's measurements (same metric and version).
func ruleCovers(rules []router.AdequacyRule, m router.Model) bool {
	for _, r := range rules {
		for _, ms := range m.Measurements {
			if ms.Metric == r.Metric && ms.MetricVersion == r.MetricVersion {
				return true
			}
		}
	}
	return false
}

func resolveRel(dir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// quarantine handles the findings an entry produced since start. For an
// operator entry nothing changes. For a generated entry, errors become
// warnings that say the entry is ignored, and the entry's other findings are
// discarded since it will never route; clean generated entries keep their
// warnings, tagged as generated. It reports whether the entry must be dropped.
func quarantine(ps *[]Problem, start int, generated bool, kind, id string) bool {
	if !generated {
		return false
	}
	mine := slices.Clone((*ps)[start:])
	*ps = (*ps)[:start]
	dropped := slices.ContainsFunc(mine, func(p Problem) bool { return p.Severity == SeverityError })
	for _, p := range mine {
		p.Source = SourceGenerated
		if dropped {
			if p.Severity != SeverityError {
				continue
			}
			p.Severity = SeverityWarning
			p.Message = fmt.Sprintf("generated %s %q ignored: %s", kind, id, p.Message)
		}
		*ps = append(*ps, p)
	}
	return dropped
}

// checkBaseURL accepts an absolute http or https URL with a host and without
// credentials, a query, or a fragment. Credentials in a URL would leak into
// every recommendation that carries it.
func checkBaseURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a URL", raw)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%q must use http or https", raw)
	case u.Host == "":
		return fmt.Errorf("%q has no host", raw)
	case u.User != nil:
		return errors.New("must not embed credentials")
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("%q must not carry a query or fragment", raw)
	}
	return nil
}

// checkGatewayProfile enforces the gateway surface contract: a gateway
// profile names an alias and exactly one gateway (a configured entry or a
// direct base URL), and only gateway profiles may carry either. A valid
// reference is resolved in place so the router sees an executable target.
func (c *Config) checkGatewayProfile(p *router.Profile, where string, gateways map[string]string, errf func(string, ...any)) {
	if p.AccessSurface != router.SurfaceGateway {
		if p.Alias != "" || p.Gateway != nil {
			errf("%s: alias and gateway apply only to access_surface %q", where, router.SurfaceGateway)
		}
		return
	}
	switch {
	case p.Alias == "":
		errf("%s: a gateway profile requires alias", where)
	case len(p.Alias) > 128 || strings.IndexFunc(p.Alias, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0:
		errf("%s: alias %q must be 1-128 characters without whitespace or control characters", where, p.Alias)
	}
	g := p.Gateway
	switch {
	case g == nil || (g.ID == "" && g.BaseURL == ""):
		errf("%s: a gateway profile requires gateway (a configured gateway id) or gateway_url", where)
	case g.ID != "" && g.BaseURL != "":
		errf("%s: set gateway or gateway_url, not both", where)
	case g.ID != "":
		base, ok := gateways[g.ID]
		if !ok {
			errf("%s: gateway %q is not declared", where, g.ID)
			return
		}
		g.BaseURL = base
	default:
		if err := checkBaseURL(g.BaseURL); err != nil {
			errf("%s: gateway_url %v", where, err)
		}
	}
}
