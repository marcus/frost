package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/frost/internal/analyzer"
	"github.com/marcus/frost/internal/config"
	"github.com/marcus/frost/internal/router"
)

const exampleConfig = "../../config/frost.example.toml"

// stubAnalyzer returns a fixed assessment without any network.
type stubAnalyzer struct {
	level    int
	contract string
	family   string
	missing  float64
	err      error
	calls    int
	lastTask router.Task
	spec     *analyzer.Spec
}

func (s *stubAnalyzer) AnalyzeRaw(_ context.Context, task router.Task) (router.Assessment, json.RawMessage, error) {
	s.calls++
	s.lastTask = task
	if s.err != nil {
		return router.Assessment{}, nil, s.err
	}
	peaked := func(level, n int) router.ScoreAnswer {
		p := make([]float64, n)
		p[level] = 1
		return router.ScoreAnswer{Mean: float64(level), Probabilities: p, Confidence: 1}
	}
	a := router.Assessment{
		Provider: "stub", RequestedModel: "jev-test", ReturnedModel: "jev-test",
		QuestionsVersion: s.spec.Version, QuestionsHash: s.spec.Hash,
		Scores: map[string]router.ScoreAnswer{router.QReasoning: peaked(s.level, 5), router.QWorkload: peaked(1, 4), router.QConsequence: peaked(1, 4)},
		Nouls:  map[string]float64{router.QMissingContext: s.missing, router.QNeedsImageInput: 0, router.QNeedsWebRes: 0, router.QNeedsRepoTools: 0.9},
		Choices: map[string]router.ChoiceAnswer{
			router.QVerification:   {Choice: "tests_and_review", Probabilities: map[string]float64{"tests_and_review": 1}, Confidence: 1},
			router.QTaskFamily:     {Choice: s.family, Probabilities: map[string]float64{s.family: 1}, Confidence: 1},
			router.QOutputContract: {Choice: s.contract, Probabilities: map[string]float64{s.contract: 1}, Confidence: 1},
			router.QResponsiveness: {Choice: router.ResponsivenessAttended, Probabilities: map[string]float64{router.ResponsivenessAttended: 1}, Confidence: 1},
		},
		LatencyMS: 12, Usage: router.Usage{InputTokens: 100, OutputTokens: 10},
	}
	return a, json.RawMessage(`{"stub":true}`), nil
}

func (s *stubAnalyzer) ListModels(context.Context) ([]string, error) {
	return []string{"jev-latest", "jev-preview"}, nil
}

type harness struct {
	stub   *stubAnalyzer
	stdout bytes.Buffer
	stderr bytes.Buffer
	stdin  string
	env    map[string]string
}

func newHarness() *harness {
	return &harness{stub: &stubAnalyzer{level: 1, contract: router.ContractCodeEdit, family: "software_change"}, env: map[string]string{"TYPESAFE_API_KEY": "k"}}
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	return Run(Env{
		Args:    args,
		Stdin:   strings.NewReader(h.stdin),
		Stdout:  &h.stdout,
		Stderr:  &h.stderr,
		Context: context.Background(),
		Getenv:  func(k string) string { return h.env[k] },
		Home:    "/nonexistent",
		Now:     func() time.Time { return time.Date(2026, 9, 17, 1, 0, 0, 0, time.UTC) },
		Version: "test",
		NewAnalyzer: func(cfg config.AnalyzerConfig, spec *analyzer.Spec, key string) (Analyzer, error) {
			h.stub.spec = spec
			return h.stub, nil
		},
	})
}

func TestRouteHumanAndJSON(t *testing.T) {
	h := newHarness()
	if code := h.run("route", "--config", exampleConfig, "Add a --json flag."); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	out := h.stdout.String()
	if !strings.Contains(out, "DeepSeek 4.1 Flash · opencode-go") || !strings.Contains(out, "Basis: provisional, from operator priors") {
		t.Fatalf("human output:\n%s", out)
	}
	if code := h.run("route", "--config", exampleConfig, "--json", "Add a --json flag."); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	var d router.Decision
	if err := json.Unmarshal(h.stdout.Bytes(), &d); err != nil {
		t.Fatalf("json: %v\n%s", err, h.stdout.String())
	}
	if d.Status != router.StatusProvisional || d.Recommendation.ProfileID != "deepseek-4.1-flash" || d.Provenance.ProgramVersion != "test" {
		t.Fatalf("decision %+v", d)
	}
	if len(d.Analysis.Questions.Scores[router.QReasoning].Probabilities) != 5 {
		t.Fatalf("analysis must carry full distributions")
	}
	// The plain alias forwards to route.
	if code := h.run("Add a --json flag.", "--config", exampleConfig); code != ExitUsage {
		t.Fatalf("flags after positional text are task words, so config resolution must fail: exit %d", code)
	}
	if code := h.run("--config", exampleConfig, "Add a --json flag."); code != ExitOK {
		t.Fatalf("flags-first alias: exit %d %s", code, h.stderr.String())
	}
}

func TestRouteExitCodes(t *testing.T) {
	h := newHarness()
	h.stub.missing = 0.9
	if code := h.run("route", "--config", exampleConfig, "--json", "Can you fix it?"); code != ExitNoRoute {
		t.Fatalf("needs_context exit %d", code)
	}
	if !strings.Contains(h.stdout.String(), `"status": "needs_context"`) {
		t.Fatalf("%s", h.stdout.String())
	}
	h.stub.missing = 0
	h.stub.err = &analyzer.ProviderError{Status: 529, Retryable: true}
	if code := h.run("route", "--config", exampleConfig, "--json", "x"); code != ExitProvider {
		t.Fatalf("provider exit %d", code)
	}
	if !strings.Contains(h.stdout.String(), `"kind": "provider"`) {
		t.Fatalf("%s", h.stdout.String())
	}
	h.stub.err = nil
	delete(h.env, "TYPESAFE_API_KEY")
	if code := h.run("route", "--config", exampleConfig, "x"); code != ExitUsage || !strings.Contains(h.stderr.String(), "TYPESAFE_API_KEY") {
		t.Fatalf("missing key: exit %d %s", code, h.stderr.String())
	}
	h.env["TYPESAFE_API_KEY"] = "k"
	if code := h.run("route", "--config", exampleConfig); code != ExitUsage {
		t.Fatalf("no input: exit %d", code)
	}
	if code := h.run("route", "--config", exampleConfig, "--stdin", "also text"); code != ExitUsage {
		t.Fatalf("two inputs: exit %d", code)
	}
	if code := h.run("route", "--config", "/nonexistent/frost.toml", "x"); code != ExitUsage {
		t.Fatalf("bad config: exit %d", code)
	}
	if code := h.run("route", "--config", exampleConfig, "--policy", "turbo", "--json", "x"); code != ExitNoRoute {
		t.Fatalf("conflict exit %d", code)
	}
}

func TestRouteInputsAndPrecedence(t *testing.T) {
	h := newHarness()
	dir := t.TempDir()
	taskFile := filepath.Join(dir, "task.md")
	os.WriteFile(taskFile, []byte("Task from file\nsecond line"), 0o600)
	if code := h.run("route", "--config", exampleConfig, "--file", taskFile); code != ExitOK {
		t.Fatalf("file: %d %s", code, h.stderr.String())
	}
	if h.stub.lastTask.Text != "Task from file\nsecond line" {
		t.Fatalf("task %q", h.stub.lastTask.Text)
	}
	h.stdin = "Task from stdin"
	if code := h.run("route", "--config", exampleConfig, "--stdin"); code != ExitOK || h.stub.lastTask.Text != "Task from stdin" {
		t.Fatalf("stdin: %d %q", code, h.stub.lastTask.Text)
	}
	req := filepath.Join(dir, "req.json")
	os.WriteFile(req, []byte(`{"schema_version":1,"task":"From request","context":"ctx","constraints":{"policy":"quality","excluded_profiles":["fable"]}}`), 0o600)
	if code := h.run("route", "--config", exampleConfig, "--request", req, "--policy", "adequate", "--json"); code != ExitOK {
		t.Fatalf("request: %d %s", code, h.stderr.String())
	}
	if h.stub.lastTask.Context != "ctx" || h.stub.lastTask.Constraints.Policy != "adequate" || len(h.stub.lastTask.Constraints.ExcludedProfiles) != 1 {
		t.Fatalf("precedence: %+v", h.stub.lastTask)
	}
	os.WriteFile(req, []byte(`{"schema_version":2,"task":"x"}`), 0o600)
	if code := h.run("route", "--config", exampleConfig, "--request", req); code != ExitUsage {
		t.Fatalf("bad schema version: %d", code)
	}
	big := filepath.Join(dir, "big.txt")
	os.WriteFile(big, bytes.Repeat([]byte("x"), MaxTaskBytes+1), 0o600)
	if code := h.run("route", "--config", exampleConfig, "--file", big); code != ExitUsage {
		t.Fatalf("oversize must fail without truncation: %d", code)
	}
	if code := h.run("route", "--config", exampleConfig, "--", "--looks-like-a-flag"); code != ExitOK || h.stub.lastTask.Text != "--looks-like-a-flag" {
		t.Fatalf("dash text: %d %q", code, h.stub.lastTask.Text)
	}
	// Explicit constraints reach the policy.
	if code := h.run("route", "--config", exampleConfig, "--json", "--allow", "sol,astra", "--effort", "high", "x"); code != ExitOK {
		t.Fatalf("%d %s", code, h.stderr.String())
	}
	var d router.Decision
	json.Unmarshal(h.stdout.Bytes(), &d)
	if d.Recommendation == nil || d.Recommendation.ProfileID != "sol" {
		t.Fatalf("allow list: %+v", d.Recommendation)
	}
}

func TestRecordAndReplay(t *testing.T) {
	h := newHarness()
	rec := filepath.Join(t.TempDir(), "run.jsonl")
	if code := h.run("route", "--config", exampleConfig, "--record", rec, "Investigate lost writes."); code != ExitOK {
		t.Fatalf("%d %s", code, h.stderr.String())
	}
	h.stub.level = 3
	if code := h.run("route", "--config", exampleConfig, "--record", rec, "Prove the queue is linearizable."); code != ExitOK {
		t.Fatalf("%d %s", code, h.stderr.String())
	}
	if h.stub.calls != 2 {
		t.Fatalf("calls %d", h.stub.calls)
	}
	b, _ := os.ReadFile(rec)
	if lines := bytes.Count(b, []byte("\n")); lines != 2 {
		t.Fatalf("expected 2 records, got %d", lines)
	}
	if !bytes.Contains(b, []byte("Investigate lost writes.")) {
		t.Fatalf("record must store the task text explicitly")
	}
	// Replay recomputes without calling the analyzer, and honors a policy override.
	if code := h.run("route", "--config", exampleConfig, "--replay", rec, "--json", "--policy", "quality"); code != ExitOK {
		t.Fatalf("%d %s", code, h.stderr.String())
	}
	if h.stub.calls != 2 {
		t.Fatalf("replay must not call the analyzer")
	}
	dec := json.NewDecoder(&h.stdout)
	var ids []string
	for dec.More() {
		var d router.Decision
		if err := dec.Decode(&d); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.Recommendation.ProfileID)
	}
	if len(ids) != 2 || ids[0] != "astra" { // fable and astra tie on quality; the cheaper one wins the tie
		t.Fatalf("replayed %v", ids)
	}
	if code := h.run("route", "--config", exampleConfig, "--replay", rec, "extra task"); code != ExitUsage {
		t.Fatalf("replay with task input: %d", code)
	}
	// A changed question hash refuses replay.
	tampered := filepath.Join(t.TempDir(), "old.jsonl")
	os.WriteFile(tampered, bytes.ReplaceAll(b, []byte(`"questions_hash":"`), []byte(`"questions_hash":"stale`)), 0o600)
	if code := h.run("route", "--config", exampleConfig, "--replay", tampered); code != ExitUsage || !strings.Contains(h.stderr.String(), "fresh judgments") {
		t.Fatalf("stale hash: %d %s", code, h.stderr.String())
	}
}

func TestProfilesConfigExplain(t *testing.T) {
	h := newHarness()
	if code := h.run("profiles", "list", "--config", exampleConfig); code != ExitOK || !strings.Contains(h.stdout.String(), "deepseek-4.1-flash") {
		t.Fatalf("%d %s", code, h.stdout.String())
	}
	if code := h.run("profiles", "list", "--config", exampleConfig, "--json"); code != ExitOK || !strings.Contains(h.stdout.String(), `"profiles"`) {
		t.Fatalf("%d", code)
	}
	if code := h.run("config", "check", "--config", exampleConfig); code != ExitOK || !strings.HasSuffix(strings.TrimSpace(h.stdout.String()), "ok") {
		t.Fatalf("%d %s", code, h.stdout.String())
	}
	if code := h.run("config", "check", "--config", exampleConfig, "--json", "--verify-model"); code != ExitOK {
		t.Fatalf("%d %s", code, h.stdout.String())
	}
	var out map[string]any
	json.Unmarshal(h.stdout.Bytes(), &out)
	if out["ok"] != true || !strings.Contains(out["pinned_model"].(string), "not listed") {
		t.Fatalf("%v", out)
	}
	if code := h.run("config", "check", "--config", "/nonexistent.toml", "--json"); code != ExitUsage {
		t.Fatalf("%d", code)
	}
	// explain renders a saved decision.
	h.run("route", "--config", exampleConfig, "--json", "x")
	saved := filepath.Join(t.TempDir(), "d.json")
	os.WriteFile(saved, h.stdout.Bytes(), 0o600)
	if code := h.run("explain", saved); code != ExitOK || !strings.Contains(h.stdout.String(), "Basis:") {
		t.Fatalf("%d %s", code, h.stdout.String())
	}
	if code := h.run("version"); code != ExitOK || !strings.Contains(h.stdout.String(), "frost test") {
		t.Fatalf("%s", h.stdout.String())
	}
	if code := h.run(); code != ExitUsage {
		t.Fatalf("no args: %d", code)
	}
}

func TestConfigResolutionOrder(t *testing.T) {
	h := newHarness()
	abs, _ := filepath.Abs(exampleConfig)
	h.env["FROST_CONFIG"] = abs
	if code := h.run("profiles", "list"); code != ExitOK {
		t.Fatalf("FROST_CONFIG: %d %s", code, h.stderr.String())
	}
	delete(h.env, "FROST_CONFIG")
	if code := h.run("profiles", "list"); code != ExitUsage || !strings.Contains(h.stderr.String(), "config") {
		t.Fatalf("no config anywhere: %d %s", code, h.stderr.String())
	}
	_ = errors.New
}
