package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/frost/internal/catalog"
	"github.com/marcus/frost/internal/router"
)

// writeConfig builds a temp config directory from the example: top is
// inserted after the capacity_file line (top-level keys), tail is appended
// (tables), and generated, when non-empty, becomes generated.json.
func writeConfig(t *testing.T, top, tail, generated string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"catalog.example.json", "questions-v3.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "config", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(exampleConfig)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(b), `capacity_file = "capacity.json"`, `capacity_file = "capacity.json"`+"\n"+top, 1) + "\n" + tail
	if generated != "" {
		if err := os.WriteFile(filepath.Join(dir, "generated.json"), []byte(generated), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadValidate(t *testing.T, path string) (*Config, []Problem) {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cat, _, err := catalog.Load(cfg.CatalogFile)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, cfg.Validate(cat, families, contracts)
}

func messages(ps []Problem, severity string) string {
	var out []string
	for _, p := range ps {
		if p.Severity == severity {
			out = append(out, p.Message)
		}
	}
	return strings.Join(out, "\n")
}

func profileByID(cfg *Config, id string) *router.Profile {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].ID == id {
			return &cfg.Profiles[i]
		}
	}
	return nil
}

const gatewayTail = `
[[gateways]]
id = "litellm"
base_url = "http://aerie.local:4000/v1"

[[profiles]]
id = "gw-code"
model = "sonnet-5"
access_surface = "gateway"
alias = "code"
gateway = "litellm"
mapping_verified = true
effort_mode = "none"
[profiles.prior]
quality_rank = 4
cost_rank = 13

[[profiles]]
id = "gw-direct"
model = "haiku-4.6"
access_surface = "gateway"
alias = "fast"
gateway_url = "https://gw.example.net/v1"
mapping_verified = true
effort_mode = "none"
[profiles.prior]
quality_rank = 6
cost_rank = 12
`

func TestGatewayProfilesResolveAndValidate(t *testing.T) {
	cfg, ps := loadValidate(t, writeConfig(t, "", gatewayTail, ""))
	if errs := errorsOf(ps); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	code := profileByID(cfg, "gw-code")
	if code.Alias != "code" || code.Gateway == nil || code.Gateway.ID != "litellm" || code.Gateway.BaseURL != "http://aerie.local:4000/v1" {
		t.Fatalf("named gateway must resolve to its base URL: %+v", code.Gateway)
	}
	direct := profileByID(cfg, "gw-direct")
	if direct.Gateway.ID != "" || direct.Gateway.BaseURL != "https://gw.example.net/v1" {
		t.Fatalf("direct url: %+v", direct.Gateway)
	}
	if code.Source != "" {
		t.Fatalf("operator profiles carry no source: %q", code.Source)
	}
}

func TestGatewayProfileErrors(t *testing.T) {
	cases := []struct{ name, tail, want string }{
		{"no alias", `[[profiles]]
id = "x"
model = "sonnet-5"
access_surface = "gateway"
gateway = "litellm"
effort_mode = "none"
[[gateways]]
id = "litellm"
base_url = "http://h/v1"`, "requires alias"},
		{"alias with space", `[[profiles]]
id = "x"
model = "sonnet-5"
access_surface = "gateway"
alias = "my alias"
gateway = "litellm"
effort_mode = "none"
[[gateways]]
id = "litellm"
base_url = "http://h/v1"`, "without whitespace"},
		{"no gateway", `[[profiles]]
id = "x"
model = "sonnet-5"
access_surface = "gateway"
alias = "code"
effort_mode = "none"`, "requires gateway"},
		{"unknown gateway", `[[profiles]]
id = "x"
model = "sonnet-5"
access_surface = "gateway"
alias = "code"
gateway = "ghost"
effort_mode = "none"`, `gateway "ghost" is not declared`},
		{"both", `[[profiles]]
id = "x"
model = "sonnet-5"
access_surface = "gateway"
alias = "code"
gateway = "litellm"
gateway_url = "http://h/v1"
effort_mode = "none"
[[gateways]]
id = "litellm"
base_url = "http://h/v1"`, "not both"},
		{"credentials in url", `[[profiles]]
id = "x"
model = "sonnet-5"
access_surface = "gateway"
alias = "code"
gateway_url = "http://user:secret@h/v1"
effort_mode = "none"`, "must not embed credentials"},
		{"bad scheme", `[[gateways]]
id = "litellm"
base_url = "ftp://h/v1"`, "must use http or https"},
		{"alias on cli profile", `[[profiles]]
id = "x"
model = "sonnet-5"
access_surface = "claude-code"
alias = "code"
effort_mode = "none"`, "apply only to access_surface"},
		{"duplicate gateway", `[[gateways]]
id = "a"
base_url = "http://h/v1"
[[gateways]]
id = "a"
base_url = "http://h2/v1"`, "duplicate gateway id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ps := loadValidate(t, writeConfig(t, "", c.tail, ""))
			if got := messages(ps, SeverityError); !strings.Contains(got, c.want) {
				t.Fatalf("want error containing %q, got:\n%s", c.want, got)
			}
		})
	}
}

const generatedOK = `{
  "schema_version": 1,
  "generated_at": "2026-10-21T20:00:00Z",
  "producer": "rook",
  "gateways": [{"id": "rook-litellm", "base_url": "http://aerie.local:4000/v1"}],
  "pools": [
    {"id": "host-5090", "kind": "local", "marginal_cost": "included", "mapping_verified": true,
     "required_window_ids": ["online", "vram_free", "queue_slots"]}
  ],
  "profiles": [
    {"id": "local-code", "model": "sonnet-5", "access_surface": "gateway", "alias": "code",
     "gateway": "rook-litellm", "pool_ids": ["host-5090"], "mapping_verified": true,
     "effort_mode": "none", "latency_class": "fast", "capabilities": ["repository_tools"],
     "prior": {"quality_rank": 4, "cost_rank": 13}}
  ]
}`

func TestGeneratedProfilesMergeBeneathOperator(t *testing.T) {
	path := writeConfig(t, `generated_profiles_file = "generated.json"`, "", generatedOK)
	cfg, ps := loadValidate(t, path)
	if errs := errorsOf(ps); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	p := profileByID(cfg, "local-code")
	if p == nil || p.Source != router.SourceGenerated || p.Gateway.BaseURL != "http://aerie.local:4000/v1" || p.Alias != "code" {
		t.Fatalf("generated profile: %+v", p)
	}
	if len(cfg.Profiles) != 14 || cfg.Profiles[13].ID != "local-code" {
		t.Fatalf("generated profiles follow the operator's: %d", len(cfg.Profiles))
	}
	if len(cfg.Pools) != 6 || cfg.Pools[5].ID != "host-5090" || cfg.Pools[5].Kind != router.PoolKindLocal {
		t.Fatalf("generated pool: %+v", cfg.Pools)
	}
	if cfg.Generated == nil || cfg.Generated.Producer != "rook" || len(cfg.Generated.Hash) != 64 || cfg.Generated.Profiles != 1 {
		t.Fatalf("generated report: %+v", cfg.Generated)
	}
}

func TestGeneratedCollisionPrecedence(t *testing.T) {
	gen := `{"schema_version":1,"profiles":[
	  {"id":"sol","model":"sonnet-5","access_surface":"gateway","alias":"a","gateway_url":"http://h/v1","effort_mode":"none","prior":{"quality_rank":1,"cost_rank":1}},
	  {"id":"dupe","model":"sonnet-5","access_surface":"gateway","alias":"a","gateway_url":"http://h/v1","effort_mode":"none"},
	  {"id":"dupe","model":"sonnet-5","access_surface":"gateway","alias":"b","gateway_url":"http://h/v1","effort_mode":"none"},
	  {"id":"","model":"sonnet-5","access_surface":"gateway","alias":"c","gateway_url":"http://h/v1","effort_mode":"none"},
	  {"id":"keeper","model":"sonnet-5","access_surface":"gateway","alias":"k","gateway_url":"http://h/v1","effort_mode":"none"}
	]}`
	cfg, ps := loadValidate(t, writeConfig(t, `generated_profiles_file = "generated.json"`, "", gen))
	if errs := errorsOf(ps); len(errs) > 0 {
		t.Fatalf("a generated collision must never be an error: %v", errs)
	}
	sol := profileByID(cfg, "sol")
	if sol.Source != "" || sol.AccessSurface != "codex" {
		t.Fatalf("the operator's sol must win: %+v", sol)
	}
	if profileByID(cfg, "dupe") != nil {
		t.Fatal("a duplicated generated id is ambiguous and must be dropped entirely")
	}
	if profileByID(cfg, "keeper") == nil {
		t.Fatal("well-formed generated profiles survive their neighbors' problems")
	}
	info, warn := messages(ps, SeverityInfo), messages(ps, SeverityWarning)
	if !strings.Contains(info, `generated profile "sol" ignored: the operator's config declares one`) {
		t.Fatalf("info: %q", info)
	}
	if !strings.Contains(warn, `generated profile "dupe" ignored: the id appears 2 times`) || !strings.Contains(warn, "id is required") {
		t.Fatalf("warnings: %q", warn)
	}
	for _, p := range ps {
		if strings.HasPrefix(p.Message, "generated ") && p.Source != SourceGenerated {
			t.Fatalf("generated findings carry their source: %+v", p)
		}
	}
}

func TestInvalidGeneratedEntriesAreQuarantinedNotFatal(t *testing.T) {
	gen := `{"schema_version":1,
	 "gateways":[{"id":"bad","base_url":"not a url"},{"id":"good","base_url":"http://h/v1"}],
	 "pools":[{"id":"badpool","kind":"weird","required_window_ids":["w"]},{"id":"goodpool","required_window_ids":["w"],"mapping_verified":true}],
	 "profiles":[
	  {"id":"no-such-model","model":"made-up","access_surface":"gateway","alias":"a","gateway":"good","effort_mode":"none"},
	  {"id":"bad-gateway","model":"sonnet-5","access_surface":"gateway","alias":"a","gateway":"bad","effort_mode":"none"},
	  {"id":"bad-pool","model":"sonnet-5","access_surface":"gateway","alias":"a","gateway":"good","pool_ids":["badpool"],"effort_mode":"none"},
	  {"id":"no-alias","model":"sonnet-5","access_surface":"gateway","gateway":"good","effort_mode":"none"},
	  {"id":"fine","model":"sonnet-5","access_surface":"gateway","alias":"a","gateway":"good","pool_ids":["goodpool"],"effort_mode":"none","prior":{"quality_rank":3,"cost_rank":3}}
	 ]}`
	cfg, ps := loadValidate(t, writeConfig(t, `generated_profiles_file = "generated.json"`, "", gen))
	if errs := errorsOf(ps); len(errs) > 0 {
		t.Fatalf("generated problems must degrade to warnings, got errors: %v", errs)
	}
	for _, id := range []string{"no-such-model", "bad-gateway", "bad-pool", "no-alias"} {
		if profileByID(cfg, id) != nil {
			t.Errorf("%s should have been dropped", id)
		}
	}
	if profileByID(cfg, "fine") == nil || len(cfg.Gateways) != 1 || cfg.Gateways[0].ID != "good" || len(cfg.Pools) != 6 {
		t.Fatalf("survivors: profiles %d gateways %+v pools %d", len(cfg.Profiles), cfg.Gateways, len(cfg.Pools))
	}
	warn := messages(ps, SeverityWarning)
	for _, want := range []string{
		`generated profile "no-such-model" ignored`, `generated profile "bad-gateway" ignored`,
		`generated profile "bad-pool" ignored`, `generated profile "no-alias" ignored`,
		`generated pool "badpool" ignored`, `generated gateway "bad" ignored`,
	} {
		if !strings.Contains(warn, want) {
			t.Errorf("missing warning %q in:\n%s", want, warn)
		}
	}
}

func TestUnusableGeneratedFilesNeverBreakTheConfig(t *testing.T) {
	for name, body := range map[string]string{
		"garbage":        `{nope`,
		"wrong version":  `{"schema_version":2}`,
		"unknown field":  `{"schema_version":1,"extra":true}`,
		"trailing":       `{"schema_version":1} {"schema_version":1}`,
		"bad timestamp":  `{"schema_version":1,"generated_at":"yesterday"}`,
		"too large":      `{"schema_version":1,"producer":"` + strings.Repeat("x", MaxGeneratedBytes) + `"}`,
		"unknown nested": `{"schema_version":1,"profiles":[{"id":"x","surprise":1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg, ps := loadValidate(t, writeConfig(t, `generated_profiles_file = "generated.json"`, "", body))
			if errs := errorsOf(ps); len(errs) > 0 {
				t.Fatalf("errors: %v", errs)
			}
			if len(cfg.Profiles) != 13 || cfg.Generated.Hash != "" {
				t.Fatalf("an unusable file contributes nothing: %d profiles, hash %q", len(cfg.Profiles), cfg.Generated.Hash)
			}
			if got := messages(ps, SeverityWarning); !strings.Contains(got, "ignored entirely") {
				t.Fatalf("warnings: %q", got)
			}
		})
	}
	// A missing file is a warning too: the producer may not have run yet.
	cfg, ps := loadValidate(t, writeConfig(t, `generated_profiles_file = "absent.json"`, "", ""))
	if errs := errorsOf(ps); len(errs) > 0 || !cfg.Generated.Missing || !strings.Contains(messages(ps, SeverityWarning), "does not exist") {
		t.Fatalf("missing: errs %v generated %+v", errs, cfg.Generated)
	}
}

func TestCapacityFilesAreOrderedAndBackwardCompatible(t *testing.T) {
	cfg, ps := loadValidate(t, writeConfig(t, `capacity_files = ["rook.json", "/abs/other.json"]`, "", ""))
	if errs := errorsOf(ps); len(errs) > 0 {
		t.Fatal(errs)
	}
	dir := filepath.Dir(cfg.Path)
	want := []string{filepath.Join(dir, "capacity.json"), filepath.Join(dir, "rook.json"), "/abs/other.json"}
	if strings.Join(cfg.CapacityFiles, "|") != strings.Join(want, "|") {
		t.Fatalf("capacity_file comes first, then capacity_files: %v", cfg.CapacityFiles)
	}
	if cfg.CapacityFile != filepath.Join(dir, "capacity.json") {
		t.Fatalf("capacity_file keeps its meaning: %q", cfg.CapacityFile)
	}
	_, ps = loadValidate(t, writeConfig(t, `capacity_files = ["capacity.json", ""]`, "", ""))
	got := messages(ps, SeverityError)
	if !strings.Contains(got, "listed twice") || !strings.Contains(got, "path is empty") {
		t.Fatalf("errors: %q", got)
	}
}

func TestPoolKindLocalAndHorizon(t *testing.T) {
	tail := `
[[pools]]
id = "host"
kind = "local"
mapping_verified = true
required_window_ids = ["online"]

[[pools]]
id = "anthropic-credit"
kind = "prepaid"
marginal_cost = "included"
mapping_verified = true
required_window_ids = ["monthly"]
expiry_preference_window_ids = ["monthly"]
non_rollover_window_ids = ["monthly"]
expiry_horizon_hours = 168
`
	cfg, ps := loadValidate(t, writeConfig(t, "", tail, ""))
	if errs := errorsOf(ps); len(errs) > 0 {
		t.Fatal(errs)
	}
	var credit router.Pool
	for _, p := range cfg.Pools {
		if p.ID == "anthropic-credit" {
			credit = p
		}
	}
	if credit.Kind != router.PoolKindPrepaid || credit.ExpiryHorizonHours != 168 {
		t.Fatalf("%+v", credit)
	}
	_, ps = loadValidate(t, writeConfig(t, "", strings.Replace(tail, "= 168", "= -1", 1), ""))
	if !strings.Contains(messages(ps, SeverityError), "expiry_horizon_hours must not be negative") {
		t.Fatalf("%v", ps)
	}
}
