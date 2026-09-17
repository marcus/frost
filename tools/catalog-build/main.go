// Command catalog-build produces Frost's neutral model catalog from public
// sources. It is the only part of the project that talks to those sites;
// frost itself reads the published file.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/marcus/frost/internal/catalog"
	"github.com/marcus/frost/internal/router"
	"github.com/marcus/frost/tools/catalog-build/build"
	"github.com/marcus/frost/tools/catalog-build/source"
)

const (
	exitOK      = 0
	exitInput   = 2
	exitNothing = 3 // every source failed; nothing published
	exitPartial = 4 // a source failed; previous data retained for it
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `catalog-build produces Frost's model catalog from public sources.

Usage:
  catalog-build refresh --out PATH [--restricted-out PATH] [--from-fixtures DIR] [--record DIR] [--dry-run] [--json]
  catalog-build fetch --source NAME --record DIR
  catalog-build diff --current PATH --candidate PATH [--json]
  catalog-build validate PATH
  catalog-build propose-aliases [--from-fixtures DIR] [--json]

Shared flags:
  --overlay PATH     identity table (default: tools/catalog-build/overlay.json, then overlay.json, relative to the working directory)
  --overrides PATH   operator overrides (default: ~/.config/frost/catalog.overrides.json when present)
  --timeout DUR      per-request timeout (default 60s)
  --json             structured result on stdout, diagnostics on stderr

Sources: models.dev, swebench, artificialanalysis (needs ARTIFICIAL_ANALYSIS_API_KEY; restricted output only).
Exit codes: 0 ok; 2 input, overlay, or validation error; 3 every source failed, nothing published; 4 a source failed, partial catalog published.
`)
}

type shared struct {
	overlay   string
	overrides string
	timeout   time.Duration
	jsonOut   bool
}

func addShared(fs *flag.FlagSet, s *shared) {
	fs.StringVar(&s.overlay, "overlay", "", "")
	fs.StringVar(&s.overrides, "overrides", "", "")
	fs.DurationVar(&s.timeout, "timeout", 60*time.Second, "")
	fs.BoolVar(&s.jsonOut, "json", false, "")
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		usage(stderr)
		return exitInput
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "refresh":
		return runRefresh(ctx, rest, stdout, stderr, getenv)
	case "fetch":
		return runFetch(ctx, rest, stdout, stderr, getenv)
	case "diff":
		return runDiff(rest, stdout, stderr)
	case "validate":
		return runValidate(rest, stdout, stderr)
	case "propose-aliases":
		return runPropose(ctx, rest, stdout, stderr, getenv)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	}
	_, _ = fmt.Fprintf(stderr, "catalog-build: unknown command %q\n", cmd)
	usage(stderr)
	return exitInput
}

func fail(stderr io.Writer, jsonOut bool, stdout io.Writer, code int, err error) int {
	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"ok": false, "error": err.Error(), "exit": code})
	}
	_, _ = fmt.Fprintln(stderr, "catalog-build: "+err.Error())
	return code
}

func resolveOverlay(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	for _, c := range []string{"tools/catalog-build/overlay.json", "overlay.json"} {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("overlay not found; pass --overlay PATH")
}

func resolveOverrides(explicit string, getenv func(string) string) string {
	if explicit != "" {
		return explicit
	}
	home := getenv("HOME")
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "frost", "catalog.overrides.json")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "frost", "catalog.overrides.json")
}

func credentials(getenv func(string) string) source.Credentials {
	return source.Credentials{ArtificialAnalysisKey: getenv("ARTIFICIAL_ANALYSIS_API_KEY")}
}

// collect fetches or loads every requested source and normalizes it.
func collect(ctx context.Context, names []string, fixtures, record string, overlay source.Overlay, creds source.Credentials, timeout time.Duration, stderr io.Writer) []build.SourceResult {
	client := &http.Client{Timeout: timeout}
	var results []build.SourceResult
	for _, name := range names {
		src, ok := source.ByName(name)
		if !ok {
			results = append(results, build.SourceResult{Name: name, Status: "failed", Error: "unknown source"})
			continue
		}
		var payloads []source.Payload
		var err error
		if fixtures != "" {
			payloads, err = build.LoadPayloads(fixtures, name)
			if errors.Is(err, os.ErrNotExist) {
				if src.Restricted() {
					results = append(results, build.SourceResult{Name: name, Status: "skipped"})
					continue
				}
				err = fmt.Errorf("no recorded payloads for %s under %s", name, fixtures)
			}
		} else {
			payloads, err = src.Fetch(ctx, client, creds)
			if errors.Is(err, source.ErrSkipped) {
				results = append(results, build.SourceResult{Name: name, Status: "skipped"})
				continue
			}
			if err == nil && record != "" {
				if rerr := build.RecordPayloads(record, payloads); rerr != nil {
					err = fmt.Errorf("record %s: %w", name, rerr)
				}
			}
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "catalog-build: %s: %v\n", name, err)
			results = append(results, build.SourceResult{Name: name, Status: "failed", Error: err.Error()})
			continue
		}
		contrib, err := src.Normalize(payloads, overlay)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "catalog-build: %s: %v\n", name, err)
			results = append(results, build.SourceResult{Name: name, Status: "failed", Error: err.Error()})
			continue
		}
		results = append(results, build.SourceResult{Name: name, Status: "ok", Payloads: payloads, Contribution: contrib})
	}
	return results
}

func sourceNames(flagValue string) []string {
	if flagValue == "" {
		var names []string
		for _, s := range source.All() {
			names = append(names, s.Name())
		}
		return names
	}
	var out []string
	for _, n := range strings.Split(flagValue, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

type refreshReport struct {
	OK           bool                 `json:"ok"`
	Exit         int                  `json:"exit"`
	Published    string               `json:"published,omitempty"`
	Restricted   string               `json:"restricted_published,omitempty"`
	Suggestions  string               `json:"latency_suggestions,omitempty"`
	DryRun       bool                 `json:"dry_run"`
	Sources      []build.SourceResult `json:"sources"`
	Diff         build.Diff           `json:"diff"`
	ModelCount   int                  `json:"model_count"`
	Measurements int                  `json:"measurement_count"`
}

type outputPath struct {
	label     string
	path      string
	canonical string
	info      os.FileInfo
}

// validateOutputPaths resolves lexical aliases, symlinked parents, symlinked
// files, and existing hard links before refresh can mutate any destination.
func validateOutputPaths(out, restricted string) (string, error) {
	suggestionsBase := out
	if restricted != "" {
		suggestionsBase = restricted
	}
	suggestions := filepath.Join(filepath.Dir(suggestionsBase), "latency.suggestions.json")
	outputs := []outputPath{
		{label: "--out", path: out},
		{label: "--out backup", path: build.PreviousPath(out)},
		{label: "latency suggestions", path: suggestions},
	}
	if restricted != "" {
		outputs = append(outputs,
			outputPath{label: "--restricted-out", path: restricted},
			outputPath{label: "--restricted-out backup", path: build.PreviousPath(restricted)},
		)
	}
	for i := range outputs {
		canonical, err := canonicalOutputPath(outputs[i].path)
		if err != nil {
			return "", fmt.Errorf("resolve %s %q: %w", outputs[i].label, outputs[i].path, err)
		}
		outputs[i].canonical = canonical
		info, err := os.Stat(outputs[i].path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect %s %q: %w", outputs[i].label, outputs[i].path, err)
		}
		outputs[i].info = info
	}
	for i := range outputs {
		for j := i + 1; j < len(outputs); j++ {
			same := outputs[i].canonical == outputs[j].canonical
			if outputs[i].info != nil && outputs[j].info != nil {
				same = same || os.SameFile(outputs[i].info, outputs[j].info)
			}
			if same {
				return "", fmt.Errorf("%s %q and %s %q resolve to the same output; output paths must be distinct", outputs[i].label, outputs[i].path, outputs[j].label, outputs[j].path)
			}
		}
	}
	return suggestions, nil
}

func canonicalOutputPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(abs)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(abs), nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func runRefresh(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sh shared
	addShared(fs, &sh)
	out := fs.String("out", "", "")
	restrictedOut := fs.String("restricted-out", "", "")
	fixtures := fs.String("from-fixtures", "", "")
	record := fs.String("record", "", "")
	sources := fs.String("source", "", "")
	dryRun := fs.Bool("dry-run", false, "")
	if err := fs.Parse(args); err != nil {
		return exitInput
	}
	if *out == "" {
		return fail(stderr, sh.jsonOut, stdout, exitInput, errors.New("--out PATH is required"))
	}
	suggestionsOut, err := validateOutputPaths(*out, *restrictedOut)
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	overlayPath, err := resolveOverlay(sh.overlay)
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	overlay, err := build.LoadOverlay(overlayPath)
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	overrides, err := build.LoadOverrides(resolveOverrides(sh.overrides, getenv))
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	current, err := build.LoadCurrent(*out)
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, fmt.Errorf("current catalog: %w", err))
	}
	var restrictedCurrent *router.Catalog
	if *restrictedOut != "" {
		restrictedCurrent, err = build.LoadCurrent(*restrictedOut)
		if err != nil {
			return fail(stderr, sh.jsonOut, stdout, exitInput, fmt.Errorf("current restricted catalog: %w", err))
		}
	}
	results := collect(ctx, sourceNames(*sources), *fixtures, *record, overlay, credentials(getenv), sh.timeout, stderr)
	okCount, partialCount := 0, 0
	for _, r := range results {
		switch r.Status {
		case "ok":
			okCount++
		case "failed":
			partialCount++
		case "skipped":
			src, _ := source.ByName(r.Name)
			if *restrictedOut != "" && src != nil && src.Restricted() {
				partialCount++
			}
		}
	}
	report := refreshReport{Sources: results, DryRun: *dryRun}
	if okCount == 0 {
		report.Exit = exitNothing
		if sh.jsonOut {
			_ = json.NewEncoder(stdout).Encode(report)
		}
		_, _ = fmt.Fprintln(stderr, "catalog-build: every source failed; nothing published")
		return exitNothing
	}
	var previousSuggestions *build.LatencySuggestions
	if *restrictedOut != "" && sourceUnavailable(results, "artificialanalysis") {
		previousSuggestions, err = build.LoadLatencySuggestions(suggestionsOut)
		if err != nil {
			return fail(stderr, sh.jsonOut, stdout, exitInput, fmt.Errorf("current latency suggestions: %w", err))
		}
	}
	asm, err := build.AssembleWithRestrictedPrevious(results, overlay, overrides, current, restrictedCurrent, time.Now().UTC(), *restrictedOut != "")
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	if previousSuggestions != nil {
		asm.Suggestions = build.RetainLatencySuggestions(*previousSuggestions)
	}
	report.Diff = build.Compute(current, asm)
	report.ModelCount = len(asm.Catalog.Models)
	for _, m := range asm.Catalog.Models {
		report.Measurements += len(m.Measurements)
	}
	if !sh.jsonOut {
		build.Render(stdout, report.Diff)
	}
	if !*dryRun {
		if err := build.Publish(*out, asm.Catalog); err != nil {
			return fail(stderr, sh.jsonOut, stdout, exitInput, fmt.Errorf("publish: %w", err))
		}
		report.Published = *out
		if asm.Restricted != nil {
			if err := build.Publish(*restrictedOut, *asm.Restricted); err != nil {
				return fail(stderr, sh.jsonOut, stdout, exitInput, fmt.Errorf("publish restricted: %w", err))
			}
			report.Restricted = *restrictedOut
		}
		if err := build.WriteJSONAtomic(suggestionsOut, asm.Suggestions); err != nil {
			return fail(stderr, sh.jsonOut, stdout, exitInput, fmt.Errorf("latency suggestions: %w", err))
		}
		report.Suggestions = suggestionsOut
	}
	report.Exit = exitOK
	if partialCount > 0 {
		report.Exit = exitPartial
	}
	report.OK = report.Exit == exitOK
	if sh.jsonOut {
		_ = json.NewEncoder(stdout).Encode(report)
	} else if !*dryRun {
		_, _ = fmt.Fprintf(stdout, "published %s (%d models, %d measurements)\n", *out, report.ModelCount, report.Measurements)
		if report.Restricted != "" {
			_, _ = fmt.Fprintf(stdout, "published restricted %s\n", report.Restricted)
		}
	}
	return report.Exit
}

func sourceUnavailable(results []build.SourceResult, name string) bool {
	for _, result := range results {
		if result.Name == name {
			return result.Status == "failed" || result.Status == "skipped"
		}
	}
	return false
}

func runFetch(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sh shared
	addShared(fs, &sh)
	name := fs.String("source", "", "")
	record := fs.String("record", "", "")
	if err := fs.Parse(args); err != nil {
		return exitInput
	}
	if *name == "" || *record == "" {
		return fail(stderr, sh.jsonOut, stdout, exitInput, errors.New("--source NAME and --record DIR are required"))
	}
	src, ok := source.ByName(*name)
	if !ok {
		return fail(stderr, sh.jsonOut, stdout, exitInput, fmt.Errorf("unknown source %q", *name))
	}
	payloads, err := src.Fetch(ctx, &http.Client{Timeout: sh.timeout}, credentials(getenv))
	if errors.Is(err, source.ErrSkipped) {
		return fail(stderr, sh.jsonOut, stdout, exitInput, fmt.Errorf("%s needs a credential in the environment", *name))
	}
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitNothing, err)
	}
	if err := build.RecordPayloads(*record, payloads); err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	if sh.jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"ok": true, "source": *name, "payloads": payloads})
	} else {
		for _, p := range payloads {
			_, _ = fmt.Fprintf(stdout, "recorded %s/%s (%d bytes, sha256 %s)\n", *name, p.Name, len(p.Body), p.SHA256[:12])
		}
	}
	return exitOK
}

func runDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sh shared
	addShared(fs, &sh)
	current := fs.String("current", "", "")
	candidate := fs.String("candidate", "", "")
	if err := fs.Parse(args); err != nil {
		return exitInput
	}
	if *candidate == "" {
		return fail(stderr, sh.jsonOut, stdout, exitInput, errors.New("--candidate PATH is required"))
	}
	var cur *router.Catalog
	if *current != "" {
		c, _, err := catalog.Load(*current)
		if err != nil {
			return fail(stderr, sh.jsonOut, stdout, exitInput, err)
		}
		cur = &c
	}
	cand, _, err := catalog.Load(*candidate)
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	asm := build.Assembly{Catalog: build.CatalogFile{Version: cand.Version}}
	for _, id := range sortedIDs(cand) {
		asm.Catalog.Models = append(asm.Catalog.Models, cand.Models[id])
	}
	d := build.Compute(cur, asm)
	if sh.jsonOut {
		_ = json.NewEncoder(stdout).Encode(d)
	} else {
		build.Render(stdout, d)
	}
	return exitOK
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sh shared
	addShared(fs, &sh)
	if err := fs.Parse(args); err != nil {
		return exitInput
	}
	if fs.NArg() != 1 {
		return fail(stderr, sh.jsonOut, stdout, exitInput, errors.New("validate takes one catalog path"))
	}
	c, hash, err := catalog.Load(fs.Arg(0))
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	n := 0
	for _, m := range c.Models {
		n += len(m.Measurements)
	}
	if sh.jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"ok": true, "version": c.Version, "sha256": hash, "models": len(c.Models), "measurements": n})
	} else {
		_, _ = fmt.Fprintf(stdout, "ok %s: %d models, %d measurements, sha256 %s\n", c.Version, len(c.Models), n, hash[:12])
	}
	return exitOK
}

func runPropose(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("propose-aliases", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sh shared
	addShared(fs, &sh)
	fixtures := fs.String("from-fixtures", "", "")
	sources := fs.String("source", "", "")
	if err := fs.Parse(args); err != nil {
		return exitInput
	}
	overlayPath, err := resolveOverlay(sh.overlay)
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	overlay, err := build.LoadOverlay(overlayPath)
	if err != nil {
		return fail(stderr, sh.jsonOut, stdout, exitInput, err)
	}
	results := collect(ctx, sourceNames(*sources), *fixtures, "", overlay, credentials(getenv), sh.timeout, stderr)
	var unmapped []source.Unmapped
	for _, r := range results {
		if r.Status == "ok" {
			unmapped = append(unmapped, r.Contribution.Unmapped...)
		}
	}
	proposals := build.ProposeAliases(unmapped, overlay)
	if sh.jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"ok": true, "proposals": proposals})
		return exitOK
	}
	for _, p := range proposals {
		_, _ = fmt.Fprintf(stdout, "%s %s (%s): %s\n", p.Source, p.SourceID, p.Label, strings.Join(p.Suggestions, ", "))
	}
	return exitOK
}

func sortedIDs(c router.Catalog) []string {
	ids := make([]string, 0, len(c.Models))
	for id := range c.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
