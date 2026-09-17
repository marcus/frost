package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/marcus/frost/internal/catalog"
	"github.com/marcus/frost/tools/catalog-build/build"
)

const fixtures = "testdata/2026-09-16"

func exec(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb, func(k string) string { return env[k] })
	return code, out.String(), errb.String()
}

func TestRefreshFromFixturesEndToEnd(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "public", "catalog.json")
	restricted := filepath.Join(dir, "restricted", "catalog.local.json")
	env := map[string]string{"HOME": dir}
	code, stdout, stderr := exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", out, "--restricted-out", restricted, "--overlay", "overlay.json")
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "new catalog content-") || !strings.Contains(stdout, "published "+out) {
		t.Fatalf("stdout:\n%s", stdout)
	}
	c, _, err := catalog.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models) != 13 {
		t.Fatalf("models %d", len(c.Models))
	}
	// Every operator model from the example catalog is present with valid contracts.
	example, _, err := catalog.Load("../../config/catalog.example.json")
	if err != nil {
		t.Fatal(err)
	}
	for id := range example.Models {
		if _, ok := c.Models[id]; !ok {
			t.Fatalf("produced catalog lacks %s", id)
		}
	}
	for _, m := range c.Models {
		for _, ms := range m.Measurements {
			if strings.HasPrefix(ms.Source, "artificialanalysis:") {
				t.Fatalf("AA data in distributable catalog")
			}
		}
	}
	rc, _, err := catalog.Load(restricted)
	if err != nil {
		t.Fatal(err)
	}
	aa := 0
	for _, m := range rc.Models {
		for _, ms := range m.Measurements {
			if strings.HasPrefix(ms.Source, "artificialanalysis:") {
				aa++
			}
		}
	}
	if aa == 0 {
		t.Fatalf("restricted catalog has no AA rows")
	}
	if _, err := os.Stat(filepath.Join(dir, "public", "latency.suggestions.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("public artifact set contains restricted latency suggestions: %v", err)
	}
	suggestionsPath := filepath.Join(dir, "restricted", "latency.suggestions.json")
	if _, err := os.Stat(suggestionsPath); err != nil {
		t.Fatalf("latency suggestions missing: %v", err)
	}
	var suggestions build.LatencySuggestions
	raw, err := os.ReadFile(suggestionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &suggestions); err != nil {
		t.Fatal(err)
	}
	if suggestions.DataUsage != "restricted_local_only" || !strings.Contains(suggestions.Basis, "restricted local") {
		t.Fatalf("latency suggestions are not explicitly restricted: %+v", suggestions)
	}
	// Golden comparison of the distributable catalog, ignoring generated_at.
	golden, err := os.ReadFile("testdata/golden/catalog.json")
	if err != nil {
		t.Skip("golden file not present")
	}
	produced, _ := os.ReadFile(out)
	if normalize(string(produced)) != normalize(string(golden)) {
		t.Fatalf("produced catalog differs from testdata/golden/catalog.json; regenerate with UPDATE_GOLDEN=1 if the change is intended")
	}
}

func normalize(s string) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, `"generated_at"`) {
			continue
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

func TestUpdateGolden(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") == "" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "catalog.json")
	if code, _, stderr := exec(t, map[string]string{"HOME": dir}, "refresh", "--from-fixtures", fixtures, "--out", out, "--overlay", "overlay.json"); code != exitOK {
		t.Fatal(stderr)
	}
	b, _ := os.ReadFile(out)
	os.MkdirAll("testdata/golden", 0o755)
	if err := os.WriteFile("testdata/golden/catalog.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshUnchangedIsEmptyDiff(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "catalog.json")
	env := map[string]string{"HOME": dir}
	exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", out, "--overlay", "overlay.json")
	first, _, _ := catalog.Load(out)
	code, stdout, _ := exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", out, "--overlay", "overlay.json", "--json")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	var rep refreshReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.Diff.Empty() || rep.Diff.CandidateVersion != first.Version {
		t.Fatalf("diff %+v", rep.Diff)
	}
	if _, err := os.Stat(filepath.Join(dir, "catalog.previous.json")); err != nil {
		t.Fatalf("previous catalog should exist after a second publish")
	}
}

func TestRefreshExitCodes(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"HOME": dir}
	out := filepath.Join(dir, "catalog.json")
	// A source absent from the fixtures fails; the other publishes; exit 4.
	code, _, stderr := exec(t, env, "refresh", "--from-fixtures", filepath.Join(dir, "nothing"), "--source", "models.dev,swebench", "--out", out, "--overlay", "overlay.json")
	if code != exitNothing {
		t.Fatalf("all failed: exit %d %s", code, stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("nothing should be published when every source fails")
	}
	partial := t.TempDir()
	os.MkdirAll(filepath.Join(partial, "models.dev"), 0o755)
	for _, f := range []string{"models.json", "api.json", "meta.json"} {
		b, _ := os.ReadFile(filepath.Join(fixtures, "models.dev", f))
		os.WriteFile(filepath.Join(partial, "models.dev", f), b, 0o644)
	}
	code, stdout, _ := exec(t, env, "refresh", "--from-fixtures", partial, "--source", "models.dev,swebench", "--out", out, "--overlay", "overlay.json")
	if code != exitPartial || !strings.Contains(stdout, "published") {
		t.Fatalf("partial: exit %d\n%s", code, stdout)
	}
	if _, _, err := catalog.Load(out); err != nil {
		t.Fatalf("partial catalog invalid: %v", err)
	}
	// Dry run publishes nothing.
	dry := filepath.Join(dir, "dry.json")
	if code, _, _ := exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", dry, "--overlay", "overlay.json", "--dry-run"); code != exitOK {
		t.Fatalf("dry run exit %d", code)
	}
	if _, err := os.Stat(dry); err == nil {
		t.Fatalf("dry run must not publish")
	}
	if code, _, _ := exec(t, env, "refresh", "--from-fixtures", fixtures, "--overlay", "overlay.json"); code != exitInput {
		t.Fatalf("missing --out should be an input error: %d", code)
	}
	if code, _, _ := exec(t, env, "bogus"); code != exitInput {
		t.Fatalf("unknown command: %d", code)
	}
}

func TestAAWithoutKeyIsSkippedAndNeverWrittenToOut(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "catalog.json")
	env := map[string]string{"HOME": dir, "ARTIFICIAL_ANALYSIS_API_KEY": "present-but-fixtures-used"}
	code, stdout, _ := exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", out, "--overlay", "overlay.json", "--json")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	var rep refreshReport
	json.Unmarshal([]byte(stdout), &rep)
	if rep.Restricted != "" {
		t.Fatalf("restricted catalog written without --restricted-out")
	}
	c, _, _ := catalog.Load(out)
	for _, m := range c.Models {
		for _, ms := range m.Measurements {
			if strings.HasPrefix(ms.Source, "artificialanalysis:") {
				t.Fatalf("AA leaked into --out")
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "latency.suggestions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suggestions build.LatencySuggestions
	if err := json.Unmarshal(raw, &suggestions); err != nil {
		t.Fatal(err)
	}
	for id, suggestion := range suggestions.Suggestions {
		if suggestion.Class != "unknown" || suggestion.TTFTSec != nil || suggestion.TokPerSec != nil || suggestion.SourceID != "" || suggestion.Source != "" || len(suggestion.ByEffort) != 0 {
			t.Fatalf("public suggestion %s contains restricted AA data: %+v", id, suggestion)
		}
	}
}

func TestRefreshRetainsRestrictedAAWhenSkipped(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "catalog.json")
	restricted := filepath.Join(dir, "catalog.local.json")
	env := map[string]string{"HOME": dir}
	if code, _, stderr := exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", out, "--restricted-out", restricted, "--overlay", "overlay.json"); code != exitOK {
		t.Fatalf("initial refresh: exit %d: %s", code, stderr)
	}
	wantAA := catalogSourceCount(t, restricted, "artificialanalysis:")
	if wantAA == 0 {
		t.Fatal("initial restricted catalog has no AA measurements")
	}

	partialFixtures := filepath.Join(dir, "partial-fixtures")
	for _, sourceName := range []string{"models.dev", "swebench"} {
		sourceDir := filepath.Join(partialFixtures, sourceName)
		if err := os.MkdirAll(sourceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(filepath.Join(fixtures, sourceName))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(fixtures, sourceName, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sourceDir, entry.Name()), raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	code, stdout, stderr := exec(t, env, "refresh", "--from-fixtures", partialFixtures, "--out", out, "--restricted-out", restricted, "--overlay", "overlay.json", "--json")
	if code != exitPartial {
		t.Fatalf("refresh with skipped AA: exit %d, want %d\nstdout: %s\nstderr: %s", code, exitPartial, stdout, stderr)
	}
	var report refreshReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	if report.OK || !slices.Contains(report.Diff.Retained, "artificialanalysis") {
		t.Fatalf("report must identify retained partial data: %+v", report)
	}
	foundSkipped := false
	for _, result := range report.Sources {
		if result.Name == "artificialanalysis" && result.Status == "skipped" {
			foundSkipped = true
		}
	}
	if !foundSkipped {
		t.Fatalf("AA source was not reported skipped: %+v", report.Sources)
	}
	if got := catalogSourceCount(t, restricted, "artificialanalysis:"); got != wantAA {
		t.Fatalf("AA measurements after skipped refresh = %d, want retained %d", got, wantAA)
	}
	if got := catalogSourceCount(t, out, "artificialanalysis:"); got != 0 {
		t.Fatalf("public catalog leaked %d AA measurements", got)
	}
}

func TestRefreshRejectsEquivalentOutputPaths(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(dir, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Fatal(err)
	}
	hardA := filepath.Join(dir, "hard-a.json")
	hardB := filepath.Join(dir, "hard-b.json")
	if err := os.WriteFile(hardA, []byte("last-good"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(hardA, hardB); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		out        string
		restricted string
	}{
		{name: "same path", out: filepath.Join(dir, "same.json"), restricted: filepath.Join(dir, "same.json")},
		{name: "clean alias", out: filepath.Join(dir, "clean.json"), restricted: filepath.Join(dir, ".", "clean.json")},
		{name: "symlinked parent", out: filepath.Join(realDir, "catalog.json"), restricted: filepath.Join(aliasDir, "catalog.json")},
		{name: "existing hard links", out: hardA, restricted: hardB},
		{name: "public is suggestions", out: filepath.Join(dir, "latency.suggestions.json")},
		{name: "restricted is suggestions", out: filepath.Join(dir, "catalog.json"), restricted: filepath.Join(dir, "latency.suggestions.json")},
		{name: "public is restricted backup", out: filepath.Join(dir, "restricted.previous.json"), restricted: filepath.Join(dir, "restricted.json")},
		{name: "restricted is public backup", out: filepath.Join(dir, "public.json"), restricted: filepath.Join(dir, "public.previous.json")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := exec(t, map[string]string{"HOME": dir}, "refresh", "--out", tc.out, "--restricted-out", tc.restricted, "--overlay", "overlay.json", "--json")
			if code != exitInput || !strings.Contains(stderr, "output paths must be distinct") {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
		})
	}
	raw, err := os.ReadFile(hardA)
	if err != nil || string(raw) != "last-good" {
		t.Fatalf("collision check mutated existing output: %q, %v", raw, err)
	}

	out := filepath.Join(dir, "distinct", "catalog.json")
	restricted := filepath.Join(dir, "local", "catalog.local.json")
	suggestions, err := validateOutputPaths(out, restricted)
	if err != nil || suggestions != filepath.Join(dir, "local", "latency.suggestions.json") {
		t.Fatalf("distinct paths: suggestions=%q err=%v", suggestions, err)
	}
}

func TestRefreshRejectsBackupCollisionBeforeSecondPublish(t *testing.T) {
	dir := t.TempDir()
	seedDir := filepath.Join(dir, "seed")
	seedPublic := filepath.Join(seedDir, "catalog.json")
	seedRestricted := filepath.Join(seedDir, "catalog.local.json")
	env := map[string]string{"HOME": dir}
	if code, _, stderr := exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", seedPublic, "--restricted-out", seedRestricted, "--overlay", "overlay.json"); code != exitOK {
		t.Fatalf("seed refresh: exit %d: %s", code, stderr)
	}
	publicBytes, err := os.ReadFile(seedPublic)
	if err != nil {
		t.Fatal(err)
	}
	restrictedBytes, err := os.ReadFile(seedRestricted)
	if err != nil {
		t.Fatal(err)
	}

	restricted := filepath.Join(dir, "restricted.json")
	public := build.PreviousPath(restricted)
	if err := os.WriteFile(public, publicBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restricted, restrictedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", public, "--restricted-out", restricted, "--overlay", "overlay.json", "--json")
	if code != exitInput || !strings.Contains(stderr, "output paths must be distinct") {
		t.Fatalf("second publish: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	afterPublic, _ := os.ReadFile(public)
	afterRestricted, _ := os.ReadFile(restricted)
	if !bytes.Equal(afterPublic, publicBytes) || !bytes.Equal(afterRestricted, restrictedBytes) {
		t.Fatal("collision rejection must leave both last-good catalogs unchanged")
	}
	if got := catalogSourceCount(t, public, "artificialanalysis:"); got != 0 {
		t.Fatalf("public output leaked %d restricted measurements", got)
	}
	if got := catalogSourceCount(t, restricted, "artificialanalysis:"); got == 0 {
		t.Fatal("restricted output lost its AA measurements")
	}
}

func catalogSourceCount(t *testing.T, path, sourcePrefix string) int {
	t.Helper()
	c, _, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
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

func TestOverridesFlowThroughRefresh(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "catalog.json")
	ov := filepath.Join(dir, "catalog.overrides.json")
	os.WriteFile(ov, []byte(`{"schema_version":1,"models":{"astra":{"set":{"context_tokens":123}}}}`), 0o600)
	env := map[string]string{"HOME": dir}
	code, stdout, _ := exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", out, "--overlay", "overlay.json", "--overrides", ov)
	if code != exitOK || !strings.Contains(stdout, "! astra overridden: context_tokens") {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
	c, _, _ := catalog.Load(out)
	if c.Models["astra"].ContextTokens != 123 {
		t.Fatalf("override not applied")
	}
	// The default overrides location under $XDG_CONFIG_HOME/frost is honored.
	xdg := filepath.Join(dir, "xdg")
	os.MkdirAll(filepath.Join(xdg, "frost"), 0o755)
	os.WriteFile(filepath.Join(xdg, "frost", "catalog.overrides.json"), []byte(`{"schema_version":1,"models":{"astra":{"set":{"context_tokens":456}}}}`), 0o600)
	exec(t, map[string]string{"HOME": dir, "XDG_CONFIG_HOME": xdg}, "refresh", "--from-fixtures", fixtures, "--out", out, "--overlay", "overlay.json")
	c, _, _ = catalog.Load(out)
	if c.Models["astra"].ContextTokens != 456 {
		t.Fatalf("default overrides path not honored")
	}
}

func TestValidateDiffAndPropose(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "catalog.json")
	env := map[string]string{"HOME": dir}
	exec(t, env, "refresh", "--from-fixtures", fixtures, "--out", out, "--overlay", "overlay.json")
	code, stdout, _ := exec(t, env, "validate", out)
	if code != exitOK || !strings.HasPrefix(stdout, "ok content-") {
		t.Fatalf("validate %d %s", code, stdout)
	}
	if code, _, _ := exec(t, env, "validate", "../../config/frost.example.toml"); code != exitInput {
		t.Fatalf("validate on a non-catalog should fail")
	}
	code, stdout, _ = exec(t, env, "diff", "--current", "../../config/catalog.example.json", "--candidate", out)
	if code != exitOK || !strings.Contains(stdout, "catalog example-2026-09-16 -> content-") {
		t.Fatalf("diff %d\n%s", code, stdout)
	}
	code, stdout, _ = exec(t, env, "propose-aliases", "--from-fixtures", fixtures, "--overlay", "overlay.json", "--source", "models.dev,swebench", "--json")
	if code != exitOK {
		t.Fatalf("propose %d", code)
	}
	var rep struct {
		Proposals []struct {
			SourceID    string   `json:"source_id"`
			Suggestions []string `json:"suggestions"`
		} `json:"proposals"`
	}
	json.Unmarshal([]byte(stdout), &rep)
	found := false
	for _, p := range rep.Proposals {
		if p.SourceID == "anthropic/claude-fable-5" && len(p.Suggestions) > 0 && p.Suggestions[0] == "fable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected claude-fable-5 to propose fable: %+v", rep.Proposals)
	}
}

func TestOverlayMapsEveryExampleModel(t *testing.T) {
	example, _, err := catalog.Load("../../config/catalog.example.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile("overlay.json")
	var overlay struct {
		Models map[string]json.RawMessage `json:"models"`
	}
	json.Unmarshal(raw, &overlay)
	for id := range example.Models {
		if _, ok := overlay.Models[id]; !ok {
			t.Errorf("overlay lacks example model %s", id)
		}
	}
}
