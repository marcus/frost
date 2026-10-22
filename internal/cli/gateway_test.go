package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/frost/internal/router"
)

// workConfig copies the frozen legacy fixtures into a temp directory so a test
// can add gateways, generated profiles, and extra capacity producers without
// touching the shared files. top lands after the capacity_file line; tail is
// appended as tables.
func workConfig(t *testing.T, top, tail string, files map[string]string) (cfg, dir string) {
	t.Helper()
	dir = t.TempDir()
	for _, f := range []string{"catalog.json", "questions-v3.json", "capacity.json"} {
		b, err := os.ReadFile(filepath.Join("testdata", "legacy", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join("testdata", "legacy", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(b), `capacity_file = "capacity.json"`, `capacity_file = "capacity.json"`+"\n"+top, 1) + "\n" + tail
	cfg = filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, dir
}

const gwTail = `
[[gateways]]
id = "litellm"
base_url = "http://aerie.local:4000/v1"

[[pools]]
id = "host-5090"
kind = "local"
marginal_cost = "included"
mapping_verified = true
required_window_ids = ["online", "vram_free", "queue_slots"]

[[profiles]]
id = "gw-code"
model = "haiku-4.6"
access_surface = "gateway"
alias = "code"
gateway = "litellm"
pool_ids = ["host-5090"]
mapping_verified = true
effort_mode = "none"
capabilities = ["repository_tools", "web_research"]
latency_class = "fast"
[profiles.prior]
quality_rank = 4
cost_rank = 13
`

const rookSnapshot = `{"schema_version":1,"generated_at":"2026-09-17T00:59:50Z","producer":"rook","pools":[
 {"id":"host-5090","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:59:50Z","valid_until":"2026-09-17T01:14:50Z","windows":[
  {"id":"online","remaining_percent":100,"resets_at":null},
  {"id":"vram_free","remaining_percent":40,"resets_at":null},
  {"id":"queue_slots","remaining_percent":80,"resets_at":null}]}]}`

func decode(t *testing.T, h *harness) router.Decision {
	t.Helper()
	var d router.Decision
	if err := json.Unmarshal(h.stdout.Bytes(), &d); err != nil {
		t.Fatalf("json: %v\n%s", err, h.stdout.String())
	}
	return d
}

func TestGatewayRouteExposesAliasAndGateway(t *testing.T) {
	cfg, _ := workConfig(t, "", gwTail, nil)
	h := newHarness()
	h.stub.level = 1
	if code := h.run("route", "--config", cfg, "--json", "--surface", "gateway", "Rename a variable."); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	// The documented, stable shape a LiteLLM pre-call hook reads.
	var raw struct {
		Recommendation struct {
			ProfileID     string `json:"profile_id"`
			AccessSurface string `json:"access_surface"`
			Gateway       struct {
				ID      string `json:"id"`
				BaseURL string `json:"base_url"`
				Alias   string `json:"alias"`
			} `json:"gateway"`
		} `json:"recommendation"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	r := raw.Recommendation
	if r.ProfileID != "gw-code" || r.AccessSurface != "gateway" || r.Gateway.Alias != "code" || r.Gateway.ID != "litellm" || r.Gateway.BaseURL != "http://aerie.local:4000/v1" {
		t.Fatalf("recommendation %+v", r)
	}
	// The same via a request object, the way a hook passes constraints.
	req := filepath.Join(t.TempDir(), "req.json")
	os.WriteFile(req, []byte(`{"schema_version":1,"task":"Rename a variable.","constraints":{"access_surfaces":["gateway"]}}`), 0o600)
	if code := h.run("route", "--config", cfg, "--json", "--request", req); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if d := decode(t, h); d.Recommendation.Gateway == nil || d.Recommendation.Gateway.Alias != "code" {
		t.Fatalf("request constraint: %+v", d.Recommendation)
	}
	// Human output names the alias, and only gateway profiles are candidates.
	if code := h.run("route", "--config", cfg, "--surface", "gateway", "Rename a variable."); code != ExitOK || !strings.Contains(h.stdout.String(), "Haiku 4.6 · gateway alias code") {
		t.Fatalf("exit %d\n%s", code, h.stdout.String())
	}
	// Alternatives that are gateway profiles carry the target too.
	if code := h.run("route", "--config", cfg, "--json", "Rename a variable."); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	d := decode(t, h)
	for _, s := range append([]router.Selection{*d.Recommendation}, d.Alternatives...) {
		if (s.AccessSurface == "gateway") != (s.Gateway != nil) {
			t.Fatalf("gateway target must accompany exactly the gateway surface: %+v", s)
		}
	}
	// With no gateway profile configured the surface filter is an honest no_match.
	if code := h.run("route", "--config", filepath.Join("testdata", "legacy", "config.toml"), "--json", "--surface", "gateway", "x"); code != ExitNoRoute {
		t.Fatalf("exit %d", code)
	}
	if d := decode(t, h); d.Status != router.StatusNoMatch {
		t.Fatalf("status %s", d.Status)
	}
	// Profiles list shows the alias.
	if code := h.run("profiles", "list", "--config", cfg); code != ExitOK || !strings.Contains(h.stdout.String(), "alias code") {
		t.Fatalf("exit %d\n%s", code, h.stdout.String())
	}
}

const generatedFile = `{"schema_version":1,"generated_at":"2026-09-17T00:59:00Z","producer":"rook",
 "gateways":[{"id":"litellm","base_url":"http://aerie.local:4000/v1"}],
 "pools":[{"id":"host-5090","kind":"local","mapping_verified":true,"required_window_ids":["online","vram_free","queue_slots"]}],
 "profiles":[
  {"id":"rook-local-code","model":"haiku-4.6","access_surface":"gateway","alias":"code","gateway":"litellm","pool_ids":["host-5090"],
   "mapping_verified":true,"effort_mode":"none","capabilities":["repository_tools","web_research"],"latency_class":"fast",
   "prior":{"quality_rank":4,"cost_rank":13}},
  {"id":"rook-broken","model":"no-such-model","access_surface":"gateway","alias":"x","gateway":"litellm","effort_mode":"none"}
 ]}`

func TestGeneratedProfilesRouteCheckAndDegrade(t *testing.T) {
	cfg, dir := workConfig(t, `generated_profiles_file = "profiles.generated.json"`, "", map[string]string{"profiles.generated.json": generatedFile, "rook.json": rookSnapshot})
	h := newHarness()
	h.stub.level = 1
	if code := h.run("route", "--config", cfg, "--capacity", filepath.Join(dir, "capacity.json"), "--capacity", filepath.Join(dir, "rook.json"), "--json", "--surface", "gateway", "Rename a variable."); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	d := decode(t, h)
	if d.Recommendation.ProfileID != "rook-local-code" || d.Recommendation.Gateway.Alias != "code" || d.Recommendation.Availability != "available" {
		t.Fatalf("%+v", d.Recommendation)
	}
	if len(d.Provenance.GeneratedProfilesHash) != 64 {
		t.Fatalf("provenance must pin the generated file: %+v", d.Provenance)
	}
	if !strings.Contains(h.stderr.String(), `generated profile "rook-broken" ignored`) {
		t.Fatalf("a dropped generated profile is announced on stderr: %q", h.stderr.String())
	}
	if code := h.run("profiles", "list", "--config", cfg, "--json"); code != ExitOK || !strings.Contains(h.stdout.String(), `"source": "generated"`) {
		t.Fatalf("exit %d\n%s", code, h.stdout.String())
	}

	// config check validates the generated file and stays ok.
	if code := h.run("config", "check", "--config", cfg); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, h.stdout.String())
	}
	out := h.stdout.String()
	if !strings.Contains(out, "generated ") || !strings.Contains(out, "rook") || !strings.Contains(out, `warning: generated profile "rook-broken" ignored`) {
		t.Fatalf("config check output:\n%s", out)
	}
	if code := h.run("config", "check", "--config", cfg, "--json"); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	var chk struct {
		OK       bool `json:"ok"`
		Problems []struct {
			Severity, Message, Source string
		} `json:"problems"`
		Generated map[string]any `json:"generated_profiles"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &chk); err != nil {
		t.Fatal(err)
	}
	foundSource := false
	for _, p := range chk.Problems {
		if p.Source == "generated" {
			foundSource = true
		}
	}
	if !chk.OK || !foundSource || chk.Generated["producer"] != "rook" || chk.Generated["profiles"] != float64(1) {
		t.Fatalf("%+v", chk)
	}

	// A corrupted file loses the generated profiles but not the router.
	os.WriteFile(filepath.Join(dir, "profiles.generated.json"), []byte("{oops"), 0o600)
	if code := h.run("route", "--config", cfg, "--json", "Rename a variable."); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if d := decode(t, h); d.Recommendation == nil || d.Recommendation.ProfileID == "rook-local-code" || d.Provenance.GeneratedProfilesHash != "" {
		t.Fatalf("%+v", d.Recommendation)
	}
	if !strings.Contains(h.stderr.String(), "ignored entirely") {
		t.Fatalf("stderr: %q", h.stderr.String())
	}

	// The operator can retire a generated profile by declaring its id.
	os.WriteFile(filepath.Join(dir, "profiles.generated.json"), []byte(generatedFile), 0o600)
	off := `
[[profiles]]
id = "rook-local-code"
model = "haiku-4.6"
access_surface = "gateway"
alias = "code"
gateway_url = "http://aerie.local:4000/v1"
enabled = false
effort_mode = "none"
`
	cfg2, _ := workConfig(t, `generated_profiles_file = "profiles.generated.json"`, off, map[string]string{"profiles.generated.json": generatedFile})
	if code := h.run("route", "--config", cfg2, "--json", "--surface", "gateway", "Rename a variable."); code != ExitNoRoute {
		t.Fatalf("a disabled operator override must beat the generated profile: exit %d\n%s", code, h.stdout.String())
	}
}

func TestMultipleCapacityProducers(t *testing.T) {
	staleRook := strings.NewReplacer("2026-09-17T00:59:50Z", "2026-09-17T00:10:00Z", "2026-09-17T01:14:50Z", "2026-09-17T00:25:00Z").Replace(rookSnapshot)
	cfg, dir := workConfig(t, `capacity_files = ["rook.json"]`, gwTail, map[string]string{"rook.json": rookSnapshot})
	h := newHarness()
	h.stub.level = 1

	avail := func(args ...string) map[string]string {
		t.Helper()
		if code := h.run(append([]string{"capacity", "check", "--config", cfg, "--json"}, args...)...); code != ExitOK {
			t.Fatalf("exit %d\n%s%s", code, h.stdout.String(), h.stderr.String())
		}
		var out struct {
			Profiles map[string]struct{ Availability string } `json:"profiles"`
			Producer string                                   `json:"producer"`
		}
		if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		m := map[string]string{"producer": out.Producer}
		for id, p := range out.Profiles {
			m[id] = p.Availability
		}
		return m
	}
	got := avail()
	if got["gw-code"] != "available" || got["sol"] != "available" || got["producer"] != "codexbar-example+rook" {
		t.Fatalf("both producers fresh: %v", got)
	}

	// Rook's producer goes stale: only its host pool degrades to unknown.
	os.WriteFile(filepath.Join(dir, "rook.json"), []byte(staleRook), 0o600)
	got = avail()
	if got["gw-code"] != "unknown" || got["sol"] != "available" {
		t.Fatalf("a stale producer degrades only its own pools: %v", got)
	}
	if code := h.run("route", "--config", cfg, "--json", "--surface", "gateway", "x"); code != ExitOK {
		t.Fatalf("unknown must not exclude: exit %d", code)
	}
	if d := decode(t, h); d.Recommendation.ProfileID != "gw-code" || d.Recommendation.Availability != "unknown" {
		t.Fatalf("%+v", d.Recommendation)
	}

	// A missing producer: a note on stderr for route, an error for check.
	os.Remove(filepath.Join(dir, "rook.json"))
	if code := h.run("route", "--config", cfg, "--json", "--surface", "gateway", "x"); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	d := decode(t, h)
	if !strings.Contains(h.stderr.String(), "rook.json does not exist; its pools are unknown") || !d.Provenance.CapacityUsed || d.Recommendation.Availability != "unknown" {
		t.Fatalf("stderr %q provenance %+v", h.stderr.String(), d.Provenance)
	}
	if code := h.run("capacity", "check", "--config", cfg); code != ExitUsage {
		t.Fatalf("check is strict: exit %d\n%s", code, h.stdout.String())
	}
	out := h.stdout.String()
	if !strings.Contains(out, "rook.json: missing; its pools are unknown") || !strings.Contains(out, "pool codex-main") {
		t.Fatalf("check still reports the healthy producer:\n%s", out)
	}

	// An invalid configured producer does not poison the other one.
	os.WriteFile(filepath.Join(dir, "rook.json"), []byte(`{"schema_version":1,"generated_at":"2026-09-17T00:59:00Z","pools":[{"id":"ghost","source_status":"ok","measurement":"exact","observed_at":null,"valid_until":null,"windows":[]}]}`), 0o600)
	if code := h.run("route", "--config", cfg, "--json", "x"); code != ExitOK || !strings.Contains(h.stderr.String(), "cannot add pools") {
		t.Fatalf("exit %d stderr %q", code, h.stderr.String())
	}
	if d := decode(t, h); !d.Provenance.CapacityUsed {
		t.Fatalf("codexbar's pools must still count: %+v", d.Provenance)
	}

	// An explicit path that fails is an input error, as before.
	if code := h.run("route", "--config", cfg, "--capacity", filepath.Join(dir, "capacity.json"), "--capacity", filepath.Join(dir, "rook.json"), "x"); code != ExitUsage {
		t.Fatalf("explicit invalid producer: exit %d", code)
	}

	// capacity_file alone behaves as it always did.
	cfgOne, _ := workConfig(t, "", "", nil)
	if code := h.run("capacity", "check", "--config", cfgOne, "--json"); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if code := h.run("config", "check", "--config", cfgOne); code != ExitOK || !strings.Contains(h.stdout.String(), "capacity  file "+filepath.Join(filepath.Dir(cfgOne), "capacity.json")+";") {
		t.Fatalf("exit %d\n%s", code, h.stdout.String())
	}
	if code := h.run("config", "check", "--config", cfg); code != ExitOK || !strings.Contains(h.stdout.String(), "capacity  files ") {
		t.Fatalf("exit %d\n%s", code, h.stdout.String())
	}
}

// The credit pool binds like any other expiring included-usage pool.
func TestAnthropicCreditPoolPreferredNearExpiry(t *testing.T) {
	tail := `
[[pools]]
id = "anthropic-credit"
kind = "prepaid"
marginal_cost = "included"
mapping_verified = true
required_window_ids = ["monthly"]
expiry_preference_window_ids = ["monthly"]
non_rollover_window_ids = ["monthly"]
expiry_horizon_hours = 168

[[pools]]
id = "plain"
kind = "metered"
marginal_cost = "included"
mapping_verified = true
required_window_ids = ["w"]
expiry_preference_window_ids = ["w"]

[[gateways]]
id = "litellm"
base_url = "http://aerie.local:4000/v1"

[[profiles]]
id = "z-anthropic-api-sonnet"
model = "sonnet-5"
access_surface = "gateway"
alias = "claude-sonnet"
gateway = "litellm"
pool_ids = ["anthropic-credit"]
mapping_verified = true
effort_mode = "none"
capabilities = ["repository_tools", "web_research"]
[profiles.prior]
quality_rank = 4
cost_rank = 5

[[profiles]]
id = "gw-cheap"
model = "haiku-4.6"
access_surface = "gateway"
alias = "chat"
gateway = "litellm"
pool_ids = ["plain"]
mapping_verified = true
effort_mode = "none"
capabilities = ["repository_tools", "web_research"]
[profiles.prior]
quality_rank = 4
cost_rank = 5
`
	credit := func(resets string) string {
		return `{"schema_version":1,"generated_at":"2026-09-17T00:59:50Z","producer":"anthropic-credit","pools":[
 {"id":"anthropic-credit","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:59:50Z","valid_until":"2026-09-17T01:14:50Z","windows":[
  {"id":"monthly","remaining_percent":72,"resets_at":"` + resets + `","duration_seconds":2592000}]},
 {"id":"plain","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:59:50Z","valid_until":"2026-09-17T01:14:50Z","windows":[
  {"id":"w","remaining_percent":72,"resets_at":"2026-10-30T00:00:00Z","duration_seconds":604800}]}]}`
	}
	h := newHarness()
	h.stub.level = 1
	run := func(resets string) router.Decision {
		cfg, _ := workConfig(t, `capacity_files = ["credit.json"]`, tail, map[string]string{"credit.json": credit(resets)})
		if code := h.run("route", "--config", cfg, "--json", "--surface", "gateway", "x"); code != ExitOK {
			t.Fatalf("exit %d: %s", code, h.stderr.String())
		}
		return decode(t, h)
	}
	// Both profiles tie on quality and cost, and the credit profile sorts last
	// by ID, so only the expiry preference can put it first.
	far := run("2026-10-10T00:00:00Z")
	if far.Recommendation.ProfileID != "gw-cheap" || far.Recommendation.ExpiryPreferred {
		t.Fatalf("a credit 23 days from expiry earns no preference: %+v", far.Recommendation)
	}
	near := run("2026-09-20T00:00:00Z")
	if near.Recommendation.ProfileID != "z-anthropic-api-sonnet" || !near.Recommendation.ExpiryPreferred {
		t.Fatalf("a credit 3 days from expiry is preferred on its pool's 7 day horizon: %+v", near.Recommendation)
	}
}
