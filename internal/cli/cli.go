// Package cli is Frost's command-line transport: arguments, stdin and files,
// and human or JSON rendering. Selection logic lives in internal/router.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/frost/internal/analyzer"
	"github.com/marcus/frost/internal/analyzer/typesafe"
	"github.com/marcus/frost/internal/catalog"
	"github.com/marcus/frost/internal/config"
	"github.com/marcus/frost/internal/evidence"
	"github.com/marcus/frost/internal/evidence/jsonl"
	"github.com/marcus/frost/internal/router"
)

// Exit codes, per the plan.
const (
	ExitOK       = 0
	ExitUsage    = 2 // input or configuration error
	ExitNoRoute  = 3 // no_match, conflict, needs_context
	ExitProvider = 4 // analyzer service failure
)

// MaxTaskBytes is Frost's documented input limit.
const MaxTaskBytes = 128 * 1024

// RequestSchemaVersion is the version of the --request JSON object.
const RequestSchemaVersion = 1

// Env is everything the CLI touches outside its arguments, injected so tests
// can run it headless.
type Env struct {
	Args    []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Context context.Context
	Getenv  func(string) string
	Home    string
	Now     func() time.Time
	Version string
	Commit  string
	// NewAnalyzer lets tests substitute the live analyzer.
	NewAnalyzer func(cfg config.AnalyzerConfig, spec *analyzer.Spec, apiKey string) (Analyzer, error)
}

// Analyzer is what route needs from the judgment provider.
type Analyzer interface {
	AnalyzeRaw(ctx context.Context, task router.Task) (router.Assessment, json.RawMessage, error)
}

// ModelLister is implemented by analyzers that can list their models.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// Run executes the CLI and returns the exit code.
func Run(env Env) int {
	env = withDefaults(env)
	if len(env.Args) == 0 {
		usage(env.Stderr)
		return ExitUsage
	}
	cmd, rest := env.Args[0], env.Args[1:]
	switch cmd {
	case "route":
		return runRoute(env, rest)
	case "profiles":
		return runProfiles(env, rest)
	case "config":
		return runConfig(env, rest)
	case "explain":
		return runExplain(env, rest)
	case "version", "--version", "-v":
		outf(env.Stdout, "frost %s (%s)\n", env.Version, env.Commit)
		return ExitOK
	case "help", "--help", "-h":
		usage(env.Stdout)
		return ExitOK
	}
	if strings.HasPrefix(cmd, "-") {
		// Flags before a subcommand: treat as route.
		return runRoute(env, env.Args)
	}
	// Plain `frost "task"` forwards to route.
	return runRoute(env, env.Args)
}

func withDefaults(env Env) Env {
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Context == nil {
		env.Context = context.Background()
	}
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	if env.Now == nil {
		env.Now = func() time.Time { return time.Now().UTC() }
	}
	if env.Version == "" {
		env.Version = "dev"
	}
	if env.NewAnalyzer == nil {
		env.NewAnalyzer = liveAnalyzer
	}
	return env
}

func liveAnalyzer(cfg config.AnalyzerConfig, spec *analyzer.Spec, apiKey string) (Analyzer, error) {
	if cfg.Provider != "typesafe" {
		return nil, fmt.Errorf("unsupported analyzer provider %q", cfg.Provider)
	}
	return typesafe.New(typesafe.Options{
		BaseURL: cfg.BaseURL,
		APIKey:  apiKey,
		Model:   cfg.Model,
		Spec:    spec,
		Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
	})
}

func usage(w io.Writer) {
	out(w, `frost recommends a model and execution profile for a task. It never runs the task.

Usage:
  frost route [flags] [--] "task text"
  frost route --file task.md | --stdin | --request request.json [--json]
  frost route --replay records.jsonl [--json]
  frost profiles list [--json]
  frost config check [--json] [--verify-model]
  frost explain decision.json
  frost version

Route flags:
  --config PATH          operator config (else FROST_CONFIG, else ~/.config/frost/config.toml)
  --json                 one JSON result on stdout; diagnostics on stderr
  --policy MODE          adequate | quality | fast | relaxed
  --allow IDS            comma-separated profile IDs to consider
  --exclude IDS          comma-separated profile IDs to reject
  --effort E             low | medium | high | xhigh (hard requirement)
  --task-family F        override the analyzer's task family
  --output-contract C    generated_text | code_edit | structured_object | typed_decision
  --require-cap CAPS     comma-separated required capabilities
  --latency-ms N         response-time target; with --latency-mode prefer|require
  --latency-milestone M  first_useful_response | complete_response
  --record PATH          append the task text, answers, and decision to a JSONL file
  --replay PATH          recompute decisions from recorded answers; no provider call

Exit codes: 0 recommendation or provisional result; 2 input/config error;
3 needs_context, no_match, or conflict; 4 analyzer service failure.
`)
}

// loaded is everything a command needs from disk.
type loaded struct {
	cfg      *config.Config
	catalog  router.Catalog
	catHash  string
	spec     *analyzer.Spec
	problems []config.Problem
}

func load(env Env, explicit string) (*loaded, error) {
	path, err := config.Resolve(explicit, env.Getenv, env.Home)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	cat, hash, err := catalog.Load(cfg.CatalogFile)
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", cfg.CatalogFile, err)
	}
	spec, err := analyzer.LoadSpec(cfg.Analyzer.QuestionsFile)
	if err != nil {
		return nil, fmt.Errorf("questions %s: %w", cfg.Analyzer.QuestionsFile, err)
	}
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("questions %s: %w", cfg.Analyzer.QuestionsFile, err)
	}
	l := &loaded{cfg: cfg, catalog: cat, catHash: hash, spec: spec}
	l.problems = cfg.Validate(cat, spec.ChoiceOptions(router.QTaskFamily), spec.ChoiceOptions(router.QOutputContract))
	for _, p := range l.problems {
		if p.Severity == "error" {
			return l, fmt.Errorf("config %s: %s", path, p.Message)
		}
	}
	return l, nil
}

func (l *loaded) inputs(env Env) router.Inputs {
	return router.Inputs{
		Catalog:        l.catalog,
		Profiles:       l.cfg.Profiles,
		Policy:         l.cfg.Policy,
		Now:            env.Now(),
		ProgramVersion: env.Version,
		CatalogHash:    l.catHash,
		PolicyHash:     l.cfg.Hash,
	}
}

// request is the versioned --request object.
type request struct {
	SchemaVersion int                `json:"schema_version"`
	Task          string             `json:"task"`
	Context       string             `json:"context,omitempty"`
	Constraints   router.Constraints `json:"constraints"`
}

type routeFlags struct {
	fs                                               *flag.FlagSet
	configPath, file, requestPath, record, replay    string
	stdin, jsonOut                                   bool
	policy, allow, exclude, effort, family, contract string
	caps                                             string
	latencyMS                                        int
	latencyMode, latencyMilestone                    string
}

func newRouteFlags(stderr io.Writer) *routeFlags {
	f := &routeFlags{fs: flag.NewFlagSet("route", flag.ContinueOnError)}
	f.fs.SetOutput(stderr)
	f.fs.StringVar(&f.configPath, "config", "", "")
	f.fs.StringVar(&f.file, "file", "", "")
	f.fs.BoolVar(&f.stdin, "stdin", false, "")
	f.fs.StringVar(&f.requestPath, "request", "", "")
	f.fs.BoolVar(&f.jsonOut, "json", false, "")
	f.fs.StringVar(&f.policy, "policy", "", "")
	f.fs.StringVar(&f.allow, "allow", "", "")
	f.fs.StringVar(&f.exclude, "exclude", "", "")
	f.fs.StringVar(&f.effort, "effort", "", "")
	f.fs.StringVar(&f.family, "task-family", "", "")
	f.fs.StringVar(&f.contract, "output-contract", "", "")
	f.fs.StringVar(&f.caps, "require-cap", "", "")
	f.fs.IntVar(&f.latencyMS, "latency-ms", 0, "")
	f.fs.StringVar(&f.latencyMode, "latency-mode", "", "")
	f.fs.StringVar(&f.latencyMilestone, "latency-milestone", "", "")
	f.fs.StringVar(&f.record, "record", "", "")
	f.fs.StringVar(&f.replay, "replay", "", "")
	return f
}

func runRoute(env Env, args []string) int {
	f := newRouteFlags(env.Stderr)
	if err := f.fs.Parse(args); err != nil {
		return ExitUsage
	}
	l, err := load(env, f.configPath)
	if err != nil {
		return fail(env, f.jsonOut, ExitUsage, err)
	}
	warnProblems(env.Stderr, l.problems)

	if f.replay != "" {
		if f.file != "" || f.stdin || f.requestPath != "" || f.fs.NArg() > 0 || f.record != "" {
			return fail(env, f.jsonOut, ExitUsage, errors.New("--replay cannot be combined with a task input or --record"))
		}
		return runReplay(env, f, l)
	}

	task, err := resolveTask(env, f)
	if err != nil {
		return fail(env, f.jsonOut, ExitUsage, err)
	}

	apiKey := env.Getenv(l.cfg.Analyzer.APIKeyEnv)
	if apiKey == "" {
		return fail(env, f.jsonOut, ExitUsage, fmt.Errorf("set %s in the environment", l.cfg.Analyzer.APIKeyEnv))
	}
	an, err := env.NewAnalyzer(l.cfg.Analyzer, l.spec, apiKey)
	if err != nil {
		return fail(env, f.jsonOut, ExitUsage, err)
	}

	var store *jsonl.Store
	if f.record != "" {
		store, err = jsonl.Open(f.record, true)
		if err != nil {
			return fail(env, f.jsonOut, ExitUsage, err)
		}
	}

	assessment, raw, err := an.AnalyzeRaw(env.Context, task)
	if err != nil {
		return fail(env, f.jsonOut, ExitProvider, err)
	}
	d := router.Recommend(task, assessment, l.inputs(env))
	if store != nil {
		rec := evidence.Record{
			SchemaVersion:  evidence.SchemaVersion,
			RecordedAt:     env.Now(),
			ProgramVersion: env.Version,
			Task:           task,
			Assessment:     assessment,
			RawResponse:    raw,
			Decision:       &d,
			CatalogHash:    l.catHash,
			PolicyHash:     l.cfg.Hash,
		}
		if err := store.Append(rec); err != nil {
			outln(env.Stderr, "record: "+err.Error())
		}
	}
	return emit(env, f.jsonOut, d)
}

func runReplay(env Env, f *routeFlags, l *loaded) int {
	store, err := jsonl.Open(f.replay, false)
	if err != nil {
		return fail(env, f.jsonOut, ExitUsage, err)
	}
	code := ExitOK
	n := 0
	err = store.Each(func(rec evidence.Record) error {
		if rec.Assessment.QuestionsHash != l.spec.Hash {
			return fmt.Errorf("record %d was answered under question hash %s, current spec is %s; changed questions require fresh judgments", n+1, short(rec.Assessment.QuestionsHash), short(l.spec.Hash))
		}
		task := rec.Task
		if f.policy != "" {
			task.Constraints.Policy = f.policy
		}
		d := router.Recommend(task, rec.Assessment, l.inputs(env))
		if n > 0 && !f.jsonOut {
			outln(env.Stdout)
		}
		if !f.jsonOut {
			label := rec.CaseID
			if label == "" {
				label = firstLine(rec.Task.Text)
			}
			outln(env.Stdout, "# "+label)
		}
		if c := emit(env, f.jsonOut, d); c > code {
			code = c
		}
		n++
		return nil
	})
	if err != nil {
		return fail(env, f.jsonOut, ExitUsage, err)
	}
	if n == 0 {
		return fail(env, f.jsonOut, ExitUsage, errors.New("no records to replay"))
	}
	return code
}

func resolveTask(env Env, f *routeFlags) (router.Task, error) {
	var task router.Task
	sources := 0
	if f.file != "" {
		sources++
	}
	if f.stdin {
		sources++
	}
	if f.requestPath != "" {
		sources++
	}
	if f.fs.NArg() > 0 {
		sources++
	}
	if sources != 1 {
		return task, errors.New("provide exactly one task input: positional text, --file, --stdin, or --request")
	}
	switch {
	case f.requestPath != "":
		b, err := readLimited(f.requestPath, MaxTaskBytes*2)
		if err != nil {
			return task, err
		}
		var req request
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			return task, fmt.Errorf("request %s: %w", f.requestPath, err)
		}
		if req.SchemaVersion != RequestSchemaVersion {
			return task, fmt.Errorf("request schema_version must be %d", RequestSchemaVersion)
		}
		task = router.Task{Text: req.Task, Context: req.Context, Constraints: req.Constraints}
	case f.file != "":
		b, err := readLimited(f.file, MaxTaskBytes)
		if err != nil {
			return task, err
		}
		task.Text = string(b)
	case f.stdin:
		b, err := io.ReadAll(io.LimitReader(env.Stdin, MaxTaskBytes+1))
		if err != nil {
			return task, err
		}
		if len(b) > MaxTaskBytes {
			return task, fmt.Errorf("task exceeds %d bytes", MaxTaskBytes)
		}
		task.Text = string(b)
	default:
		task.Text = strings.Join(f.fs.Args(), " ")
	}
	if strings.TrimSpace(task.Text) == "" {
		return task, errors.New("task text is empty")
	}
	if len(task.Text) > MaxTaskBytes {
		return task, fmt.Errorf("task exceeds %d bytes", MaxTaskBytes)
	}
	// Explicit CLI flags override the request object.
	c := &task.Constraints
	if f.policy != "" {
		c.Policy = f.policy
	}
	if f.allow != "" {
		c.AllowedProfiles = splitList(f.allow)
	}
	if f.exclude != "" {
		c.ExcludedProfiles = splitList(f.exclude)
	}
	if f.effort != "" {
		c.Effort = f.effort
	}
	if f.family != "" {
		c.TaskFamily = f.family
	}
	if f.contract != "" {
		c.OutputContract = f.contract
	}
	if f.caps != "" {
		c.RequiredCapabilities = splitList(f.caps)
	}
	if f.latencyMS != 0 || f.latencyMode != "" || f.latencyMilestone != "" {
		if c.Latency == nil {
			c.Latency = &router.LatencyTarget{Mode: router.LatencyPrefer, Milestone: router.MilestoneFirstUseful}
		}
		if f.latencyMS != 0 {
			c.Latency.TargetMS = f.latencyMS
		}
		if f.latencyMode != "" {
			c.Latency.Mode = f.latencyMode
		}
		if f.latencyMilestone != "" {
			c.Latency.Milestone = f.latencyMilestone
		}
	}
	return task, nil
}

func runProfiles(env Env, args []string) int {
	if len(args) == 0 || args[0] != "list" {
		outln(env.Stderr, "usage: frost profiles list [--json] [--config PATH]")
		return ExitUsage
	}
	fs := flag.NewFlagSet("profiles list", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonOut := fs.Bool("json", false, "")
	configPath := fs.String("config", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return ExitUsage
	}
	l, err := load(env, *configPath)
	if err != nil {
		return fail(env, *jsonOut, ExitUsage, err)
	}
	warnProblems(env.Stderr, l.problems)
	if *jsonOut {
		return writeJSON(env, map[string]any{"schema_version": router.SchemaVersion, "profiles": l.cfg.Profiles})
	}
	for _, p := range l.cfg.Profiles {
		state := "enabled"
		if !p.Enabled {
			state = "disabled"
		}
		m := l.catalog.Models[p.ModelID]
		label := p.Label
		if label == "" {
			label = m.Label
		}
		line := fmt.Sprintf("%-32s %-28s %-14s %s", p.ID, label, p.AccessSurface, state)
		if p.Prior != nil {
			line += fmt.Sprintf("  prior q%d c%d", p.Prior.QualityRank, p.Prior.CostRank)
		}
		if p.LatencyClass != "" {
			line += "  " + p.LatencyClass
		}
		if !p.MappingVerified {
			line += "  unverified"
		}
		outln(env.Stdout, line)
	}
	return ExitOK
}

func runConfig(env Env, args []string) int {
	if len(args) == 0 || args[0] != "check" {
		outln(env.Stderr, "usage: frost config check [--json] [--config PATH] [--verify-model]")
		return ExitUsage
	}
	fs := flag.NewFlagSet("config check", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonOut := fs.Bool("json", false, "")
	configPath := fs.String("config", "", "")
	verify := fs.Bool("verify-model", false, "")
	if err := fs.Parse(args[1:]); err != nil {
		return ExitUsage
	}
	l, err := load(env, *configPath)
	problems := []config.Problem{}
	if l != nil {
		problems = append(problems, l.problems...)
	}
	if err != nil && (l == nil || len(problems) == 0) {
		problems = append(problems, config.Problem{Severity: "error", Message: err.Error()})
	}
	modelStatus := "not checked"
	if err == nil && *verify {
		modelStatus = verifyModel(env, l)
		switch {
		case strings.HasPrefix(modelStatus, "error: "):
			problems = append(problems, config.Problem{Severity: "error", Message: strings.TrimPrefix(modelStatus, "error: ")})
		case strings.HasPrefix(modelStatus, "warning: "):
			problems = append(problems, config.Problem{Severity: "warning", Message: strings.TrimPrefix(modelStatus, "warning: ")})
		}
	}
	ok := true
	for _, p := range problems {
		if p.Severity == "error" {
			ok = false
		}
	}
	if *jsonOut {
		out := map[string]any{"schema_version": router.SchemaVersion, "ok": ok, "problems": problems, "pinned_model": modelStatus}
		if l != nil && l.cfg != nil {
			out["config"] = l.cfg.Path
			out["catalog"] = l.cfg.CatalogFile
			out["catalog_version"] = l.catalog.Version
			out["questions"] = l.cfg.Analyzer.QuestionsFile
			out["questions_version"] = l.spec.Version
			out["policy"] = l.cfg.Policy
		}
		code := writeJSON(env, out)
		if !ok {
			return ExitUsage
		}
		return code
	}
	if l != nil && l.cfg != nil {
		outf(env.Stdout, "config    %s\n", l.cfg.Path)
		outf(env.Stdout, "catalog   %s (%s)\n", l.cfg.CatalogFile, l.catalog.Version)
		outf(env.Stdout, "questions %s (%s)\n", l.cfg.Analyzer.QuestionsFile, l.spec.Version)
		outf(env.Stdout, "analyzer  %s %s\n", l.cfg.Analyzer.Provider, l.cfg.Analyzer.Model)
		outf(env.Stdout, "policy    %s mode, quantile %.2f\n", l.cfg.Policy.Mode, l.cfg.Policy.UncertainReasoningQuantile)
		outf(env.Stdout, "model     %s\n", modelStatus)
	}
	for _, p := range problems {
		outf(env.Stdout, "%s: %s\n", p.Severity, p.Message)
	}
	if ok {
		outln(env.Stdout, "ok")
		return ExitOK
	}
	return ExitUsage
}

func verifyModel(env Env, l *loaded) string {
	apiKey := env.Getenv(l.cfg.Analyzer.APIKeyEnv)
	if apiKey == "" {
		return "error: " + l.cfg.Analyzer.APIKeyEnv + " is not set; cannot verify the pinned model"
	}
	an, err := env.NewAnalyzer(l.cfg.Analyzer, l.spec, apiKey)
	if err != nil {
		return "error: " + err.Error()
	}
	lister, ok := an.(ModelLister)
	if !ok {
		return "not supported by this analyzer"
	}
	models, err := lister.ListModels(env.Context)
	if err != nil {
		return "error: " + err.Error()
	}
	for _, m := range models {
		if m == l.cfg.Analyzer.Model {
			return l.cfg.Analyzer.Model + " is offered"
		}
	}
	return fmt.Sprintf("warning: pinned model %s is not listed by the models endpoint, which enumerates %s; the evaluation endpoint accepts pinned versions it does not list, so this is not proof the pin is retired", l.cfg.Analyzer.Model, strings.Join(models, ", "))
}

func runExplain(env Env, args []string) int {
	if len(args) != 1 {
		outln(env.Stderr, "usage: frost explain decision.json")
		return ExitUsage
	}
	b, err := readLimited(args[0], 16*1024*1024)
	if err != nil {
		return fail(env, false, ExitUsage, err)
	}
	var d router.Decision
	if err := json.Unmarshal(b, &d); err != nil {
		return fail(env, false, ExitUsage, fmt.Errorf("decision %s: %w", args[0], err))
	}
	if d.SchemaVersion != router.SchemaVersion {
		return fail(env, false, ExitUsage, fmt.Errorf("decision schema_version %d is not supported", d.SchemaVersion))
	}
	RenderHuman(env.Stdout, d)
	return ExitOK
}

func emit(env Env, jsonOut bool, d router.Decision) int {
	if jsonOut {
		writeJSON(env, d)
	} else {
		RenderHuman(env.Stdout, d)
	}
	switch d.Status {
	case router.StatusNeedsContext, router.StatusNoMatch, router.StatusConflict:
		return ExitNoRoute
	}
	return ExitOK
}

func fail(env Env, jsonOut bool, code int, err error) int {
	kind := "input"
	if code == ExitProvider {
		kind = "provider"
	}
	if jsonOut {
		writeJSON(env, map[string]any{"schema_version": router.SchemaVersion, "error": map[string]any{"kind": kind, "message": err.Error()}})
	}
	outln(env.Stderr, "frost: "+err.Error())
	return code
}

func writeJSON(env Env, v any) int {
	enc := json.NewEncoder(env.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		outln(env.Stderr, "frost: "+err.Error())
		return ExitUsage
	}
	return ExitOK
}

func warnProblems(w io.Writer, problems []config.Problem) {
	for _, p := range problems {
		if p.Severity != "error" {
			outln(w, "config warning: "+p.Message)
		}
	}
}

func readLimited(path string, limit int) ([]byte, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return b, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 72 {
		s = s[:72] + "…"
	}
	return s
}
