// Command probe exercises the proposed router using synthetic cases or one task.
// It recommends profiles; it never executes the task or launches another model.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type Question struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}
type Answer struct {
	Type          string             `json:"type"`
	Score         *float64           `json:"score,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   map[string]int    `json:"usage"`
}
type Case struct {
	ID             string    `json:"id"`
	Split          string    `json:"split"`
	Task           string    `json:"task"`
	ReasoningRange []float64 `json:"reasoning_range,omitempty"`
	WorkloadRange  []float64 `json:"workload_range,omitempty"`
	MissingContext bool      `json:"missing_context"`
}
type Profile struct {
	ID           string  `json:"id"`
	Label        string  `json:"label"`
	Harness      string  `json:"harness"`
	QualityRank  int     `json:"quality_rank"`
	CostRank     int     `json:"cost_rank"`
	NativeEffort *string `json:"native_effort"`
	Enabled      bool    `json:"enabled"`
}
type Policy struct {
	Version string   `json:"version"`
	Quality []int    `json:"max_quality_rank_by_reasoning_level"`
	Effort  []string `json:"recommended_effort_by_reasoning_level"`
	Missing float64  `json:"missing_context_threshold"`
	Review  float64  `json:"consequence_review_threshold"`
}
type Catalog struct {
	Version    string    `json:"version"`
	Provenance string    `json:"provenance"`
	Policy     Policy    `json:"policy"`
	Profiles   []Profile `json:"profiles"`
}
type Recommendation struct {
	Status               string   `json:"status"`
	Profile              *Profile `json:"profile,omitempty"`
	ReasoningLevel       int      `json:"reasoning_level"`
	Effort               string   `json:"effort_intent,omitempty"`
	NativeEffortVerified bool     `json:"native_effort_verified"`
	Review               string   `json:"review"`
	Reasons              []string `json:"reasons"`
	Checks               []string `json:"failed_checks,omitempty"`
}
type Record struct {
	Case           Case           `json:"case"`
	StartedAt      time.Time      `json:"started_at"`
	LatencyMS      int64          `json:"latency_ms"`
	QuestionHash   string         `json:"question_sha256"`
	CatalogHash    string         `json:"catalog_sha256"`
	RequestedModel string         `json:"requested_model"`
	Response       Response       `json:"response"`
	Recommendation Recommendation `json:"recommendation"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	task := flag.String("task", "", "Task text; omit with --cases or use piped stdin")
	cases := flag.String("cases", "", "JSONL case file (labels are never sent to TypeSafe)")
	questionsPath := flag.String("questions", "experiments/questions-v2.json", "Question specification")
	catalogPath := flag.String("catalog", "experiments/catalog.json", "Editable profile catalog and bootstrap policy")
	output := flag.String("out", "", "New JSONL evidence file; existing files are never replaced")
	replay := flag.String("replay", "", "Recompute recommendations from saved evidence without API calls")
	model := flag.String("model", "jev-1.13.0", "TypeSafe judgment model")
	repeat := flag.Int("repeat", 1, "Number of live repetitions (1-5)")
	jsonOutput := flag.Bool("json", false, "Print full JSONL records on stdout")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments; use --task or stdin")
	}
	if *repeat < 1 || *repeat > 5 {
		return errors.New("repeat must be 1-5")
	}
	var catalog Catalog
	cb, err := readJSON(*catalogPath, &catalog)
	if err != nil {
		return err
	}
	if len(catalog.Policy.Quality) != 5 || len(catalog.Policy.Effort) != 5 {
		return errors.New("policy requires five reasoning levels")
	}
	if len(catalog.Profiles) == 0 {
		return errors.New("empty profile catalog")
	}
	seen := map[string]bool{}
	for _, p := range catalog.Profiles {
		if p.ID == "" || seen[p.ID] || p.QualityRank < 1 || p.CostRank < 1 {
			return errors.New("invalid or duplicate profile")
		}
		seen[p.ID] = true
	}
	var questions map[string]Question
	qb, err := readJSON(*questionsPath, &questions)
	if err != nil {
		return err
	}
	for _, id := range []string{"reasoning", "workload", "consequence", "missing_context", "verification"} {
		if _, ok := questions[id]; !ok {
			return fmt.Errorf("missing question %s", id)
		}
	}
	var dest *os.File
	if *output != "" {
		dest, err = os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer func() { _ = dest.Close() }()
	}
	emit := func(r Record) error {
		if err := validate(r.Response, questions); err != nil {
			return err
		}
		r.CatalogHash = hash(cb)
		r.Recommendation = recommend(r.Response, catalog)
		if r.Case.Split != "manual" {
			r.Recommendation.Checks = check(r.Case, r.Response, catalog.Policy.Missing)
		}
		if dest != nil {
			if err := json.NewEncoder(dest).Encode(r); err != nil {
				return err
			}
			if err := dest.Sync(); err != nil {
				return err
			}
		}
		if *jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(r)
		}
		pick := r.Recommendation.Status
		if p := r.Recommendation.Profile; p != nil {
			pick = p.Label + " / suggested effort " + r.Recommendation.Effort + " (native mapping unverified)"
			if p.NativeEffort != nil {
				pick = p.Label + " (fixed native effort from catalog; task intent " + r.Recommendation.Effort + ")"
			}
		}
		fmt.Printf("%s: %s | reasoning %.2f/4, workload %.2f/3 | %s\n", r.Case.ID, pick, *r.Response.Answers["reasoning"].Score, *r.Response.Answers["workload"].Score, r.Recommendation.Review)
		if len(r.Recommendation.Checks) > 0 {
			fmt.Printf("  Outside draft rubric: %s\n", strings.Join(r.Recommendation.Checks, ", "))
		}
		return nil
	}
	if *replay != "" {
		if *task != "" || *cases != "" {
			return errors.New("replay cannot be combined with task or cases")
		}
		if err := readLines(*replay, func(b []byte) error {
			var r Record
			if err := json.Unmarshal(b, &r); err != nil {
				return err
			}
			if r.QuestionHash != hash(qb) {
				return errors.New("replay questions differ from recorded question hash")
			}
			return emit(r)
		}); err != nil {
			return err
		}
	} else {
		inputs := []Case{}
		if *cases != "" {
			if *task != "" {
				return errors.New("choose task or cases")
			}
			if err := readLines(*cases, func(b []byte) error {
				var c Case
				if err := json.Unmarshal(b, &c); err != nil {
					return err
				}
				inputs = append(inputs, c)
				return nil
			}); err != nil {
				return err
			}
		} else {
			text := *task
			if text == "" {
				st, err := os.Stdin.Stat()
				if err != nil {
					return err
				}
				if st.Mode()&os.ModeCharDevice != 0 {
					return errors.New("provide --task, --cases, or piped stdin")
				}
				b, err := io.ReadAll(io.LimitReader(os.Stdin, 128*1024+1))
				if err != nil {
					return err
				}
				text = string(b)
			}
			inputs = append(inputs, Case{ID: "manual", Split: "manual", Task: text})
		}
		for _, c := range inputs {
			if strings.TrimSpace(c.Task) == "" || len(c.Task) > 128*1024 {
				return errors.New("task must contain 1-131072 bytes")
			}
		}
		key := os.Getenv("TYPESAFE_API_KEY")
		if key == "" {
			return errors.New("set TYPESAFE_API_KEY in the environment")
		}
		// Successful calls are persisted before starting the next billable call.
		for n := 0; n < *repeat; n++ {
			for _, c := range inputs {
				started := time.Now().UTC()
				resp, err := ask(key, *model, c.Task, questions)
				if err != nil {
					return fmt.Errorf("case %s: %w", c.ID, err)
				}
				if err := emit(Record{Case: c, StartedAt: started, LatencyMS: time.Since(started).Milliseconds(), QuestionHash: hash(qb), RequestedModel: *model, Response: resp}); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Analyzed %s (%d/%d), %d ms\n", c.ID, n+1, *repeat, time.Since(started).Milliseconds())
			}
		}
	}
	return nil
}
func readJSON(path string, v any) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	return b, nil
}
func readLines(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 4*1024*1024)
	for s.Scan() {
		if len(bytes.TrimSpace(s.Bytes())) == 0 {
			continue
		}
		if err := fn(s.Bytes()); err != nil {
			return err
		}
	}
	return s.Err()
}
func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func ask(key, model, task string, questions map[string]Question) (Response, error) {
	var result Response
	body, err := json.Marshal(map[string]any{"model": model, "state": map[string]string{"task": task}, "questions": questions})
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.typesafe.ai/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil {
		return result, err
	}
	if len(b) > 4*1024*1024 {
		return result, errors.New("response exceeds limit")
	}
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("TypeSafe HTTP %d (response body omitted)", resp.StatusCode)
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return result, err
	}
	return result, validate(result, questions)
}
func validate(r Response, qs map[string]Question) error {
	if r.Model == "" {
		return errors.New("missing response model")
	}
	for id, q := range qs {
		a, ok := r.Answers[id]
		if !ok || a.Type != q.Type {
			return fmt.Errorf("missing or mismatched answer %s", id)
		}
		if q.Type == "noul" {
			if a.Noul == nil || !finiteRange(*a.Noul, 0, 1) {
				return fmt.Errorf("invalid noul %s", id)
			}
			continue
		}
		if a.Confidence == nil || !finiteRange(*a.Confidence, 0, 1) {
			return fmt.Errorf("invalid confidence %s", id)
		}
		expected := map[string]bool{}
		switch q.Type {
		case "score":
			var levels []string
			if err := json.Unmarshal(q.Criteria, &levels); err != nil {
				return err
			}
			if len(levels) < 2 {
				return errors.New("score needs levels")
			}
			if a.Score == nil || !finiteRange(*a.Score, 0, float64(len(levels)-1)) {
				return fmt.Errorf("invalid score %s", id)
			}
			for i := range levels {
				expected[fmt.Sprint(i)] = true
			}
		case "choice":
			var options map[string]string
			if err := json.Unmarshal(q.Criteria, &options); err != nil {
				return err
			}
			for k := range options {
				expected[k] = true
			}
			if !expected[a.Choice] {
				return fmt.Errorf("invalid choice %s", id)
			}
		default:
			return fmt.Errorf("unknown question type %s", q.Type)
		}
		sum := 0.0
		for k, p := range a.Probabilities {
			if !expected[k] || !finiteRange(p, 0, 1) {
				return fmt.Errorf("invalid probabilities %s", id)
			}
			sum += p
		}
		if len(a.Probabilities) != len(expected) || math.Abs(sum-1) > 0.02 {
			return fmt.Errorf("incomplete distribution %s", id)
		}
	}
	return nil
}
func finiteRange(x, lo, hi float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && x >= lo && x <= hi
}
func recommend(r Response, c Catalog) Recommendation {
	level := int(math.Round(*r.Answers["reasoning"].Score))
	out := Recommendation{Status: "provisional", ReasoningLevel: level, Effort: c.Policy.Effort[level], Review: r.Answers["verification"].Choice, Reasons: []string{"Bootstrap recommendation from user priors; not measured model success probability."}}
	if *r.Answers["consequence"].Score >= c.Policy.Review {
		out.Review = "independent review and targeted verification"
	}
	if *r.Answers["missing_context"].Noul >= c.Policy.Missing {
		out.Status = "needs_context"
		out.Effort = ""
		out.Reasons = append(out.Reasons, "Describe the intended change and relevant project or material.")
		return out
	}
	candidates := []Profile{}
	for _, p := range c.Profiles {
		if p.Enabled && p.QualityRank <= c.Policy.Quality[level] {
			candidates = append(candidates, p)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CostRank == candidates[j].CostRank {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].CostRank > candidates[j].CostRank
	})
	if len(candidates) == 0 {
		out.Status = "no_match"
		return out
	}
	out.Profile = &candidates[0]
	out.Reasons = append(out.Reasons, fmt.Sprintf("Cheapest ordinal cost among profiles with quality rank <= %d.", c.Policy.Quality[level]))
	if out.Profile.NativeEffort != nil {
		out.Reasons = append(out.Reasons, "Catalog profile is fixed at native effort "+*out.Profile.NativeEffort+"; generic effort intent does not override it.")
	}
	return out
}
func check(c Case, r Response, threshold float64) []string {
	failed := []string{}
	for id, bounds := range map[string][]float64{"reasoning": c.ReasoningRange, "workload": c.WorkloadRange} {
		if len(bounds) == 2 {
			v := *r.Answers[id].Score
			if v < bounds[0] || v > bounds[1] {
				failed = append(failed, id)
			}
		}
	}
	if (*r.Answers["missing_context"].Noul >= threshold) != c.MissingContext {
		failed = append(failed, "missing_context")
	}
	sort.Strings(failed)
	return failed
}
