package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marcus/frost/internal/catalog"
	"github.com/marcus/frost/internal/router"
	"github.com/marcus/frost/tools/catalog-build/source"
)

var fixedNow = time.Date(2026, 9, 17, 3, 20, 0, 0, time.UTC)

const fixtures = "../testdata/2026-09-16"

func testOverlay(t *testing.T) source.Overlay {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.json")
	raw := `{"schema_version":1,"models":{
	  "sol":{"label":"Sol","sources":{"models.dev":["openai/gpt-5.6-sol"],"artificialanalysis":["gpt-5-6-sol-high","gpt-5-6-sol"]},"generation_mechanism":"autoregressive"},
	  "opus-5":{"label":"Opus 5","sources":{"models.dev":["anthropic/claude-opus-5"],"swebench":["claude-opus-4-5-20251101"],"artificialanalysis":["claude-opus-5-high"]}},
	  "haiku":{"label":"Haiku","sources":{"swebench":["claude-haiku-4-5-20251001"]}},
	  "jev":{"label":"Jev","sources":{},"aliases":["jev-alias"],"output_contracts":["typed_decision"],"generation_mechanism":"undisclosed"}
	},"ignore":{"swebench":["Attempts - 2+"]}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := LoadOverlay(path)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func results(t *testing.T, overlay source.Overlay, names ...string) []SourceResult {
	t.Helper()
	var out []SourceResult
	for _, name := range names {
		src, _ := source.ByName(name)
		payloads, err := LoadPayloads(fixtures, name)
		if err != nil {
			t.Fatal(err)
		}
		contrib, err := src.Normalize(payloads, overlay)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, SourceResult{Name: name, Status: "ok", Payloads: payloads, Contribution: contrib})
	}
	return out
}

func modelByID(c CatalogFile, id string) router.Model {
	for _, m := range c.Models {
		if m.ID == id {
			return m
		}
	}
	return router.Model{}
}

func TestOverlayRejectsDuplicateClaims(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.json")
	os.WriteFile(path, []byte(`{"schema_version":1,"models":{"a":{"label":"A","sources":{"models.dev":["x/y"]}},"b":{"label":"B","sources":{"models.dev":["x/y"]}}}}`), 0o600)
	if _, err := LoadOverlay(path); err == nil || !strings.Contains(err.Error(), "claimed by both") {
		t.Fatalf("err %v", err)
	}
	os.WriteFile(path, []byte(`{"schema_version":1,"models":{"a":{"label":"A","sources":{"nope":["x"]}}}}`), 0o600)
	if _, err := LoadOverlay(path); err == nil || !strings.Contains(err.Error(), "unknown source") {
		t.Fatalf("err %v", err)
	}
}

func TestAssembleFromFixtures(t *testing.T) {
	overlay := testOverlay(t)
	asm, err := Assemble(results(t, overlay, "models.dev", "swebench", "artificialanalysis"), overlay, Overrides{SchemaVersion: 1}, nil, fixedNow, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Parse(mustJSON(asm.Catalog)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(asm.Catalog.Version, "2026-09-17-") || len(asm.Catalog.Version) != len("2026-09-17-")+8 {
		t.Fatalf("version %q", asm.Catalog.Version)
	}
	sol := modelByID(asm.Catalog, "sol")
	if sol.ContextTokens != 1050000 || sol.GenerationMechanism != "autoregressive" || strings.Join(sol.InputModalities, ",") != "text,image" {
		t.Fatalf("sol %+v", sol)
	}
	// AA measurements are absent from the distributable catalog and present in the restricted one.
	for _, m := range sol.Measurements {
		if strings.HasPrefix(m.Source, "artificialanalysis:") {
			t.Fatalf("restricted measurement leaked into distributable catalog: %+v", m)
		}
	}
	rsol := modelByID(*asm.Restricted, "sol")
	aa := 0
	for _, m := range rsol.Measurements {
		if strings.HasPrefix(m.Source, "artificialanalysis:") {
			aa++
		}
	}
	if aa == 0 || !strings.HasSuffix(asm.Restricted.Version, "-restricted") {
		t.Fatalf("restricted catalog missing AA rows (%d) or version suffix %q", aa, asm.Restricted.Version)
	}
	// SWE-bench rows land on opus-5 with their effort, and haiku (no models.dev) still gets defaults.
	opus := modelByID(asm.Catalog, "opus-5")
	swe := 0
	for _, m := range opus.Measurements {
		if m.Metric == "swebench.verified.resolve_rate" {
			swe++
			if m.Effort != "medium" {
				t.Fatalf("effort must stay on the measurement: %+v", m)
			}
		}
	}
	if swe != 2 {
		t.Fatalf("opus-5 swebench rows %d", swe)
	}
	haiku := modelByID(asm.Catalog, "haiku")
	if strings.Join(haiku.OutputContracts, ",") != "generated_text" || strings.Join(haiku.InputModalities, ",") != "text" {
		t.Fatalf("haiku defaults %+v", haiku)
	}
	jev := modelByID(asm.Catalog, "jev")
	if strings.Join(jev.OutputContracts, ",") != "typed_decision" || jev.Aliases[0] != "jev-alias" {
		t.Fatalf("jev %+v", jev)
	}
	// Latency suggestions use the first-listed AA slug (sol high, TTFT 2.5 -> medium) and mark others unknown.
	if s := asm.Suggestions.Suggestions["sol"]; s.Class != "medium" || s.SourceID != "gpt-5-6-sol-high" {
		t.Fatalf("sol suggestion %+v", s)
	}
	if got := asm.Suggestions.Suggestions["sol"].ByEffort; got["high"] != "medium" || got["max"] != "slow" {
		t.Fatalf("by_effort %v", got)
	}
	if asm.Suggestions.Suggestions["haiku"].Class != "unknown" || asm.Suggestions.Suggestions["opus-5"].Class != "fast" {
		t.Fatalf("suggestions %+v", asm.Suggestions.Suggestions)
	}
	if len(asm.EffortOptions["sol"]) == 0 {
		t.Fatalf("effort options should be surfaced")
	}
	// Unmapped records from every source are collected and sorted.
	ids := map[string]bool{}
	for _, u := range asm.Unmapped {
		ids[u.Source+"/"+u.SourceID] = true
	}
	if !ids["models.dev/anthropic/claude-fable-5"] || !ids["swebench/gpt-5-2-codex"] || !ids["artificialanalysis/synthetic-unmapped"] {
		t.Fatalf("unmapped %+v", asm.Unmapped)
	}
}

func TestUnchangedRefreshProducesEmptyDiffAndSameVersion(t *testing.T) {
	overlay := testOverlay(t)
	first, err := Assemble(results(t, overlay, "models.dev", "swebench"), overlay, Overrides{SchemaVersion: 1}, nil, fixedNow, false)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := catalog.Parse(mustJSON(first.Catalog))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Assemble(results(t, overlay, "models.dev", "swebench"), overlay, Overrides{SchemaVersion: 1}, &cur, fixedNow.Add(2*time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Catalog.Version != first.Catalog.Version {
		t.Fatalf("version changed %s -> %s", first.Catalog.Version, second.Catalog.Version)
	}
	d := Compute(&cur, second)
	if !d.Empty() {
		var buf bytes.Buffer
		Render(&buf, d)
		t.Fatalf("expected empty diff:\n%s", buf.String())
	}
}

func TestFailedSourceRetainsPreviousData(t *testing.T) {
	overlay := testOverlay(t)
	first, _ := Assemble(results(t, overlay, "models.dev", "swebench"), overlay, Overrides{SchemaVersion: 1}, nil, fixedNow, false)
	cur, _ := catalog.Parse(mustJSON(first.Catalog))
	failed := results(t, overlay, "models.dev")
	failed = append(failed, SourceResult{Name: "swebench", Status: "failed", Error: "HTTP 503"})
	asm, err := Assemble(failed, overlay, Overrides{SchemaVersion: 1}, &cur, fixedNow.Add(24*time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	opus := modelByID(asm.Catalog, "opus-5")
	kept := 0
	for _, m := range opus.Measurements {
		if strings.HasPrefix(m.Source, "swebench:") {
			kept++
			if !m.ObservedAt.Equal(time.Date(2025, 12, 15, 0, 0, 0, 0, time.UTC)) && !m.ObservedAt.Equal(time.Date(2025, 11, 24, 0, 0, 0, 0, time.UTC)) {
				t.Fatalf("observed_at must be the original: %v", m.ObservedAt)
			}
		}
	}
	if kept != 2 || len(asm.Retained) != 1 || asm.Retained[0] != "swebench" {
		t.Fatalf("kept %d retained %v", kept, asm.Retained)
	}
	d := Compute(&cur, asm)
	if len(d.RemovedMeasurements) != 0 {
		t.Fatalf("retained rows must not show as removed: %+v", d.RemovedMeasurements)
	}
	// models.dev failing keeps facts from the previous catalog.
	asm2, err := Assemble([]SourceResult{{Name: "models.dev", Status: "failed", Error: "timeout"}, results(t, overlay, "swebench")[0]}, overlay, Overrides{SchemaVersion: 1}, &cur, fixedNow, false)
	if err != nil {
		t.Fatal(err)
	}
	if modelByID(asm2.Catalog, "sol").ContextTokens != 1050000 {
		t.Fatalf("context limit lost when models.dev failed")
	}
}

func TestRestrictedSourceRetainsRestrictedPriorWhenUnavailable(t *testing.T) {
	overlay := testOverlay(t)
	first, err := Assemble(results(t, overlay, "models.dev", "swebench", "artificialanalysis"), overlay, Overrides{SchemaVersion: 1}, nil, fixedNow, true)
	if err != nil {
		t.Fatal(err)
	}
	publicPrior, _ := catalog.Parse(mustJSON(first.Catalog))
	restrictedPrior, _ := catalog.Parse(mustJSON(*first.Restricted))
	wantAA := countMeasurements(*first.Restricted, "artificialanalysis:")
	if wantAA == 0 {
		t.Fatal("fixture must establish restricted measurements")
	}

	for _, status := range []string{"skipped", "failed"} {
		t.Run(status, func(t *testing.T) {
			current := results(t, overlay, "models.dev", "swebench")
			current = append(current, SourceResult{Name: "artificialanalysis", Status: status, Error: "HTTP 503"})
			asm, err := AssembleWithRestrictedPrevious(current, overlay, Overrides{SchemaVersion: 1}, &publicPrior, &restrictedPrior, fixedNow.Add(time.Hour), true)
			if err != nil {
				t.Fatal(err)
			}
			if got := countMeasurements(*asm.Restricted, "artificialanalysis:"); got != wantAA {
				t.Fatalf("retained AA measurements = %d, want %d", got, wantAA)
			}
			if got := countMeasurements(asm.Catalog, "artificialanalysis:"); got != 0 {
				t.Fatalf("public catalog leaked %d restricted measurements", got)
			}
			if !slices.Contains(asm.Retained, "artificialanalysis") {
				t.Fatalf("retained sources %v", asm.Retained)
			}
		})
	}
}

func countMeasurements(c CatalogFile, sourcePrefix string) int {
	count := 0
	for _, model := range c.Models {
		for _, measurement := range model.Measurements {
			if strings.HasPrefix(measurement.Source, sourcePrefix) {
				count++
			}
		}
	}
	return count
}

func TestOverridesApplyLastAndAreLabeled(t *testing.T) {
	overlay := testOverlay(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.overrides.json")
	os.WriteFile(path, []byte(`{"schema_version":1,"models":{"sol":{
	  "set":{"context_tokens":400000,"label":"Sol (tuned)"},
	  "remove_measurements":[{"metric":"price.output_usd_per_mtok"}],
	  "add_measurements":[{"source":"local-eval","metric":"swebench.verified.resolve_rate","metric_version":"verified-2024-08","value":0.81,"unit":"fraction","higher_is_better":true,"task_family":"software_change"}]
	}}}`), 0o600)
	ov, err := LoadOverrides(path)
	if err != nil {
		t.Fatal(err)
	}
	asm, err := Assemble(results(t, overlay, "models.dev"), overlay, ov, nil, fixedNow, false)
	if err != nil {
		t.Fatal(err)
	}
	sol := modelByID(asm.Catalog, "sol")
	if sol.ContextTokens != 400000 || sol.Label != "Sol (tuned)" {
		t.Fatalf("set not applied: %+v", sol)
	}
	metrics := map[string]int{}
	for _, m := range sol.Measurements {
		metrics[m.Metric]++
	}
	if metrics["price.output_usd_per_mtok"] != 0 || metrics["swebench.verified.resolve_rate"] != 1 || metrics["price.input_usd_per_mtok"] != 1 {
		t.Fatalf("measurement overrides: %v", metrics)
	}
	if got := strings.Join(asm.Overridden["sol"], ","); got != "context_tokens,label,measurements(+1),measurements(-1)" {
		t.Fatalf("overridden %q", got)
	}
	d := Compute(nil, asm)
	if d.Overridden["sol"] == nil {
		t.Fatalf("diff must carry overridden fields")
	}
	// Overrides survive a refresh: the next assembly still has them.
	cur, _ := catalog.Parse(mustJSON(asm.Catalog))
	again, _ := Assemble(results(t, overlay, "models.dev"), overlay, ov, &cur, fixedNow.Add(time.Hour), false)
	if modelByID(again.Catalog, "sol").ContextTokens != 400000 || !Compute(&cur, again).Empty() {
		t.Fatalf("override clobbered on refresh")
	}
	os.WriteFile(path, []byte(`{"schema_version":1,"models":{"sol":{"set":{"id":"x"}}}}`), 0o600)
	if _, err := LoadOverrides(path); err == nil || !strings.Contains(err.Error(), "cannot be overridden") {
		t.Fatalf("err %v", err)
	}
	if ov, err := LoadOverrides(filepath.Join(dir, "missing.json")); err != nil || len(ov.Models) != 0 {
		t.Fatalf("missing overrides must be empty: %v %+v", err, ov)
	}
}

func TestDiffReportsModelsAndMeasurements(t *testing.T) {
	overlay := testOverlay(t)
	first, _ := Assemble(results(t, overlay, "models.dev"), overlay, Overrides{SchemaVersion: 1}, nil, fixedNow, false)
	cur, _ := catalog.Parse(mustJSON(first.Catalog))
	delete(overlay.Models, "jev")
	second, _ := Assemble(results(t, overlay, "models.dev", "swebench"), overlay, Overrides{SchemaVersion: 1}, &cur, fixedNow, false)
	d := Compute(&cur, second)
	if len(d.RemovedModels) != 1 || d.RemovedModels[0] != "jev" {
		t.Fatalf("removed %v", d.RemovedModels)
	}
	if d.AddedMeasurements["opus-5"] != 2 {
		t.Fatalf("added %v", d.AddedMeasurements)
	}
	var buf bytes.Buffer
	Render(&buf, d)
	if !strings.Contains(buf.String(), "- model jev") || !strings.Contains(buf.String(), "+ opus-5: 2 measurement(s)") {
		t.Fatalf("render:\n%s", buf.String())
	}
}

func TestPublishIsAtomicAndKeepsPrevious(t *testing.T) {
	overlay := testOverlay(t)
	asm, _ := Assemble(results(t, overlay, "models.dev"), overlay, Overrides{SchemaVersion: 1}, nil, fixedNow, false)
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := Publish(path, asm.Catalog); err != nil {
		t.Fatal(err)
	}
	if _, _, err := catalog.Load(path); err != nil {
		t.Fatal(err)
	}
	asm.Catalog.Version = "v2"
	if err := Publish(path, asm.Catalog); err != nil {
		t.Fatal(err)
	}
	prev, err := os.ReadFile(filepath.Join(dir, "catalog.previous.json"))
	if err != nil || !bytes.Contains(prev, []byte(`"version": "2026-09-17-`)) {
		t.Fatalf("previous catalog missing: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".catalog-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	bad := asm.Catalog
	bad.Models[0].OutputContracts = []string{"nonsense"}
	if err := Publish(path, bad); err == nil {
		t.Fatalf("invalid catalog must not publish")
	}
	if c, _, err := catalog.Load(path); err != nil || c.Version != "v2" {
		t.Fatalf("failed publish must leave the current file intact: %v", err)
	}
}

func TestProposeAliases(t *testing.T) {
	overlay := testOverlay(t)
	props := ProposeAliases([]source.Unmapped{{Source: "models.dev", SourceID: "anthropic/claude-opus-5-fast", Label: "Claude Opus 5 Fast"}, {Source: "swebench", SourceID: "zzz", Label: "Unrelated"}}, overlay)
	if len(props) != 2 || len(props[0].Suggestions) == 0 || props[0].Suggestions[0] != "opus-5" {
		t.Fatalf("proposals %+v", props)
	}
	if len(props[1].Suggestions) != 0 {
		t.Fatalf("unrelated record should get no suggestion: %+v", props[1])
	}
}

func TestClassFor(t *testing.T) {
	cases := []struct {
		ttft, tps float64
		want      string
	}{{0.3, 200, "extra_fast"}, {0.8, 100, "fast"}, {0.4, 50, "medium"}, {2.9, 10, "medium"}, {3.5, 300, "slow"}}
	for _, c := range cases {
		if got := ClassFor(c.ttft, c.tps); got != c.want {
			t.Errorf("%v/%v: %s want %s", c.ttft, c.tps, got, c.want)
		}
	}
}

func TestRecordAndLoadPayloadsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"leaderboards":[]}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	in := []source.Payload{{Source: "swebench", Name: "leaderboards.json", URL: "u", FetchedAt: fixedNow, SHA256: digest, Body: body}}
	if err := RecordPayloads(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadPayloads(dir, "swebench")
	if err != nil || len(out) != 1 || string(out[0].Body) != `{"leaderboards":[]}` || out[0].SHA256 != digest || !out[0].FetchedAt.Equal(fixedNow) {
		t.Fatalf("round trip %+v %v", out, err)
	}
	if _, err := LoadPayloads(dir, "models.dev"); !os.IsNotExist(err) {
		t.Fatalf("missing source should be ErrNotExist: %v", err)
	}
	var meta map[string]any
	raw, _ := os.ReadFile(filepath.Join(dir, "swebench", "meta.json"))
	if err := json.Unmarshal(raw, &meta); err != nil || meta["source"] != "swebench" {
		t.Fatalf("meta %v %v", meta, err)
	}
}

func TestLoadPayloadsRejectsTamperedFixture(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"leaderboards":[]}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	payload := source.Payload{Source: "swebench", Name: "leaderboards.json", URL: "u", FetchedAt: fixedNow, SHA256: digest, Body: body}
	if err := RecordPayloads(dir, []source.Payload{payload}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "swebench", "leaderboards.json"), []byte(`{"leaderboards":[{"tampered":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPayloads(dir, "swebench"); err == nil || !strings.Contains(err.Error(), "swebench/leaderboards.json: sha256 mismatch") || !strings.Contains(err.Error(), digest) {
		t.Fatalf("tampered fixture error = %v", err)
	}
}
