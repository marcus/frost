package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/frost/internal/catalog"
	"github.com/marcus/frost/internal/router"
)

const exampleConfig = "../../config/frost.example.toml"

var families = []string{"software_change", "software_investigation", "research_synthesis", "writing", "structured_decision", "other"}
var contracts = []string{"generated_text", "code_edit", "structured_object", "typed_decision", "other"}

func loadExample(t *testing.T) (*Config, router.Catalog) {
	t.Helper()
	cfg, err := Load(exampleConfig)
	if err != nil {
		t.Fatal(err)
	}
	cat, _, err := catalog.Load(cfg.CatalogFile)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, cat
}

func errorsOf(ps []Problem) []string {
	var out []string
	for _, p := range ps {
		if p.Severity == SeverityError {
			out = append(out, p.Message)
		}
	}
	return out
}

func TestExampleLoadsAndValidates(t *testing.T) {
	cfg, cat := loadExample(t)
	if cfg.SchemaVersion != 1 || cfg.Analyzer.Model != "jev-1.13.0" || cfg.Analyzer.Provider != DefaultProvider {
		t.Fatalf("analyzer %+v", cfg.Analyzer)
	}
	if !filepath.IsAbs(cfg.CatalogFile) || !filepath.IsAbs(cfg.Analyzer.QuestionsFile) {
		t.Fatalf("paths not resolved: %s %s", cfg.CatalogFile, cfg.Analyzer.QuestionsFile)
	}
	if cfg.Analyzer.TimeoutSeconds != 45 || cfg.Analyzer.APIKeyEnv != DefaultAPIKeyEnv {
		t.Fatalf("analyzer defaults %+v", cfg.Analyzer)
	}
	if len(cfg.Hash) != 64 {
		t.Fatalf("hash %q", cfg.Hash)
	}
	if cfg.Policy.Mode != router.ModeAdequate || cfg.Policy.UncertainReasoningQuantile != 0.8 {
		t.Fatalf("policy %+v", cfg.Policy)
	}
	if got := cfg.Policy.PriorMaxQualityRankByLevel; len(got) != 5 || got[0] != 9 || got[4] != 1 {
		t.Fatalf("prior table %v", got)
	}
	if len(cfg.Profiles) != 13 || len(cfg.Pools) != 5 {
		t.Fatalf("profiles %d pools %d", len(cfg.Profiles), len(cfg.Pools))
	}
	ps := cfg.Validate(cat, families, contracts)
	if errs := errorsOf(ps); len(errs) > 0 {
		t.Fatalf("example has errors: %v", errs)
	}
	// Priors match the pilot catalog exactly.
	want := map[string][2]int{
		"fable": {1, 1}, "astra": {1, 2}, "sol": {2, 4}, "opus-5": {2, 3}, "grok-4.6-high": {3, 7},
		"muse-1.3-spark-contributor-xhigh": {3, 11}, "deepseek-4.1-flash": {4, 12}, "terra": {5, 6},
		"sonnet-5": {6, 5}, "gemini-3.8-high": {7, 8}, "luna": {8, 10}, "haiku-4.6": {9, 9},
	}
	for _, p := range cfg.Profiles {
		w, ok := want[p.ID]
		if !ok {
			if p.ID != "jev-typed-decision" || p.Enabled || p.Prior != nil {
				t.Fatalf("unexpected profile %+v", p)
			}
			continue
		}
		if p.Prior == nil || p.Prior.QualityRank != w[0] || p.Prior.CostRank != w[1] {
			t.Fatalf("%s prior %+v want %v", p.ID, p.Prior, w)
		}
		if !p.Enabled {
			t.Fatalf("%s should be enabled", p.ID)
		}
	}
	// Unverified mappings warn, nothing more.
	warned := 0
	for _, p := range ps {
		if p.Severity == SeverityWarning && strings.Contains(p.Message, "unverified") {
			warned++
		}
	}
	if warned != 1 {
		t.Fatalf("expected one aggregated unverified warning, got %d", warned)
	}
}

func TestDefaultsFillUnsetPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	write(t, path, `schema_version = 1
catalog_file = "cat.json"
[analyzer]
model = "jev-1.13.0"
questions_file = "q.json"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	def := router.DefaultPolicy()
	if cfg.Policy.Mode != def.Mode || cfg.Policy.MissingContextThreshold != def.MissingContextThreshold || len(cfg.Policy.EffortByLevel) != 5 {
		t.Fatalf("defaults not applied: %+v", cfg.Policy)
	}
	if cfg.CatalogFile != filepath.Join(dir, "cat.json") {
		t.Fatalf("catalog path %s", cfg.CatalogFile)
	}
}

func TestUnknownKeyIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	write(t, path, "schema_version = 1\ncatalog_file = \"x\"\nbogus = 1\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err = %v", err)
	}
}

func TestSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	write(t, path, "schema_version = 7\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	_, cat := loadExample(t)
	base := func() *Config {
		cfg, _ := loadExample(t)
		return cfg
	}
	cases := map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"duplicate profile": {func(c *Config) { c.Profiles[1].ID = c.Profiles[0].ID }, "duplicate profile id"},
		"unknown model":     {func(c *Config) { c.Profiles[0].ModelID = "nope" }, "not in the catalog"},
		"contract mismatch": {func(c *Config) { c.Profiles[0].OutputContracts = []string{"typed_decision"} }, "not supported by model"},
		"bad effort mode":   {func(c *Config) { c.Profiles[0].EffortMode = "turbo" }, "effort_mode"},
		"fixed no native":   {func(c *Config) { c.Profiles[0].EffortMode = "fixed"; c.Profiles[0].NativeEffort = "" }, "requires native_effort"},
		"configurable none": {func(c *Config) { c.Profiles[0].EffortOptions = nil }, "requires effort_options"},
		"bad effort option": {func(c *Config) { c.Profiles[0].EffortOptions = []string{"max"} }, "effort ladder"},
		"bad latency class": {func(c *Config) { c.Profiles[0].LatencyClass = "instant" }, "latency_class"},
		"bad mode":          {func(c *Config) { c.Policy.Mode = "balanced" }, "policy.mode"},
		"bad quantile":      {func(c *Config) { c.Policy.UncertainReasoningQuantile = 1.5 }, "uncertain_reasoning_quantile"},
		"bad margin":        {func(c *Config) { c.Policy.TaskFamilyMargin = -0.1 }, "task_family_margin"},
		"effort ladder len": {func(c *Config) { c.Policy.EffortByLevel = []string{"low"} }, "effort_by_level"},
		"prior table len":   {func(c *Config) { c.Policy.PriorMaxQualityRankByLevel = []int{1, 2} }, "prior_max_quality_rank_by_level"},
		"rule family": {func(c *Config) {
			c.Policy.AdequacyRules = []router.AdequacyRule{{ID: "r", TaskFamily: "poetry", ReasoningLevels: []int{1}, Metric: "m", MetricVersion: "1", ProfileMatch: "exact", OnMissing: "unknown"}}
		}, "not offered by the question specification"},
		"rule level": {func(c *Config) {
			c.Policy.AdequacyRules = []router.AdequacyRule{{ID: "r", TaskFamily: "writing", ReasoningLevels: []int{9}, Metric: "m", MetricVersion: "1", ProfileMatch: "exact", OnMissing: "unknown"}}
		}, "outside 0-4"},
		"rule on_missing": {func(c *Config) {
			c.Policy.AdequacyRules = []router.AdequacyRule{{ID: "r", TaskFamily: "writing", ReasoningLevels: []int{1}, Metric: "m", MetricVersion: "1", ProfileMatch: "exact", OnMissing: "skip"}}
		}, "on_missing"},
		"rule profile_match": {func(c *Config) {
			c.Policy.AdequacyRules = []router.AdequacyRule{{ID: "r", TaskFamily: "writing", ReasoningLevels: []int{1}, Metric: "m", MetricVersion: "1", ProfileMatch: "fuzzy", OnMissing: "unknown"}}
		}, "profile_match"},
		"unknown pool":   {func(c *Config) { c.Profiles[1].PoolIDs = []string{"ghost"} }, "not declared"},
		"duplicate pool": {func(c *Config) { c.Pools = append(c.Pools, c.Pools[0]) }, "duplicate pool id"},
		"no model":       {func(c *Config) { c.Analyzer.Model = "" }, "analyzer.model"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			tc.mutate(cfg)
			errs := errorsOf(cfg.Validate(cat, families, contracts))
			for _, e := range errs {
				if strings.Contains(e, tc.want) {
					return
				}
			}
			t.Fatalf("no error containing %q in %v", tc.want, errs)
		})
	}
}

func TestValidationWarningsAndAliasRewrite(t *testing.T) {
	cfg, cat := loadExample(t)
	cfg.Profiles[0].Prior = nil
	cfg.Profiles[1].Capabilities = append(cfg.Profiles[1].Capabilities, router.CapImageInput) // astra: text only
	for i := range cfg.Profiles {
		if cfg.Profiles[i].ID == "jev-typed-decision" {
			cfg.Profiles[i].ModelID = "jev" // alias
		}
	}
	ps := cfg.Validate(cat, families, contracts)
	if errs := errorsOf(ps); len(errs) > 0 {
		t.Fatalf("unexpected errors %v", errs)
	}
	var msgs []string
	for _, p := range ps {
		msgs = append(msgs, p.Message)
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"adequacy will be unknown", "no image input modality"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing warning %q in\n%s", want, joined)
		}
	}
	for _, p := range cfg.Profiles {
		if p.ID == "jev-typed-decision" && p.ModelID != "jev-1.13.0" {
			t.Fatalf("alias not rewritten: %s", p.ModelID)
		}
	}
	// Disabling everything warns.
	for i := range cfg.Profiles {
		cfg.Profiles[i].Enabled = false
	}
	found := false
	for _, p := range cfg.Validate(cat, nil, nil) {
		if strings.Contains(p.Message, "no profile is enabled") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected no-enabled warning")
	}
}

func TestResolveOrder(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.toml")
	env := filepath.Join(dir, "env.toml")
	xdgHome := filepath.Join(dir, "xdg")
	xdg := filepath.Join(xdgHome, "frost", "config.toml")
	home := filepath.Join(dir, "home")
	homeCfg := filepath.Join(home, ".config", "frost", "config.toml")
	for _, p := range []string{explicit, env, xdg, homeCfg} {
		write(t, p, "schema_version = 1\n")
	}
	getenv := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	if got, _ := Resolve(explicit, getenv(map[string]string{"FROST_CONFIG": env}), home); got != explicit {
		t.Fatalf("explicit wins: %s", got)
	}
	if got, _ := Resolve("", getenv(map[string]string{"FROST_CONFIG": env, "XDG_CONFIG_HOME": xdgHome}), home); got != env {
		t.Fatalf("env wins: %s", got)
	}
	if got, _ := Resolve("", getenv(map[string]string{"XDG_CONFIG_HOME": xdgHome}), home); got != xdg {
		t.Fatalf("xdg: %s", got)
	}
	if got, _ := Resolve("", getenv(map[string]string{}), home); got != homeCfg {
		t.Fatalf("home: %s", got)
	}
	if _, err := Resolve("", getenv(map[string]string{}), filepath.Join(dir, "empty")); err == nil || !strings.Contains(err.Error(), "--config") {
		t.Fatalf("missing config err = %v", err)
	}
	if _, err := Resolve(filepath.Join(dir, "nope.toml"), getenv(nil), home); err == nil {
		t.Fatal("missing explicit path should error")
	}
	if _, err := Resolve("", getenv(map[string]string{"FROST_CONFIG": filepath.Join(dir, "nope.toml")}), home); err == nil {
		t.Fatal("missing FROST_CONFIG path should error")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
