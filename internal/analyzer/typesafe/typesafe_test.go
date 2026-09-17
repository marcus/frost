package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/frost/internal/analyzer"
	"github.com/marcus/frost/internal/router"
)

func loadSpec(t *testing.T) *analyzer.Spec {
	t.Helper()
	spec, err := analyzer.LoadSpec(filepath.Join("..", "..", "..", "config", "questions-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// goodResponse builds a valid answer set for every question in the spec.
func goodResponse(spec *analyzer.Spec) map[string]any {
	answers := map[string]any{}
	for id, q := range spec.Questions {
		switch q.Type {
		case "noul":
			answers[id] = map[string]any{"type": "noul", "noul": 0.1}
		case "score":
			levels, _ := spec.ScoreLevels(id)
			probs := map[string]float64{}
			legend := map[string]string{}
			for i := range levels {
				probs[itoa(i)] = 0
				legend[itoa(i)] = levels[i]
			}
			probs["1"] = 0.7
			probs["2"] = 0.3
			answers[id] = map[string]any{"type": "score", "score": 1.3, "confidence": 0.54, "probabilities": probs, "legend": legend}
		case "choice":
			opts := spec.ChoiceOptions(id)
			probs := map[string]float64{}
			for _, o := range opts {
				probs[o] = 0
			}
			probs[opts[0]] = 1
			answers[id] = map[string]any{"type": "choice", "choice": opts[0], "confidence": 1.0, "probabilities": probs}
		}
	}
	return map[string]any{
		"model":   "jev-1.13.0",
		"answers": answers,
		"usage":   map[string]int{"input_tokens": 1200, "output_tokens": 130},
	}
}

func itoa(i int) string { return string(rune('0' + i)) }

type capture struct {
	bodies [][]byte
	auth   string
}

func newClient(t *testing.T, srv *httptest.Server, spec *analyzer.Spec, sleeps *int32) *Client {
	t.Helper()
	c, err := New(Options{
		BaseURL: srv.URL,
		APIKey:  "k",
		Model:   "jev-1.13.0",
		Spec:    spec,
		Sleep: func(time.Duration) {
			if sleeps != nil {
				atomic.AddInt32(sleeps, 1)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAnalyzeNormalizes(t *testing.T) {
	spec := loadSpec(t)
	var cap capture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		cap.auth = r.Header.Get("Authorization")
		var b json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&b)
		cap.bodies = append(cap.bodies, b)
		_ = json.NewEncoder(w).Encode(goodResponse(spec))
	}))
	defer srv.Close()
	c := newClient(t, srv, spec, nil)

	a, raw, err := c.AnalyzeRaw(context.Background(), router.Task{Text: "Fix the typo."})
	if err != nil {
		t.Fatal(err)
	}
	if cap.auth != "Bearer k" {
		t.Errorf("auth header %q", cap.auth)
	}
	if len(raw) == 0 || !json.Valid(raw) {
		t.Error("raw response missing or invalid")
	}
	if a.Provider != "typesafe" || a.ReturnedModel != "jev-1.13.0" || a.RequestedModel != "jev-1.13.0" {
		t.Errorf("identity: %+v", a)
	}
	if a.QuestionsVersion != spec.Version || a.QuestionsHash != spec.Hash {
		t.Error("questions provenance not carried")
	}
	if a.Usage.InputTokens != 1200 || a.Usage.OutputTokens != 130 {
		t.Errorf("usage %+v", a.Usage)
	}
	rs := a.Scores[router.QReasoning]
	if rs.Mean != 1.3 || rs.Confidence != 0.54 || len(rs.Probabilities) != 5 || rs.Probabilities[1] != 0.7 || rs.Probabilities[2] != 0.3 {
		t.Errorf("reasoning score %+v", rs)
	}
	if len(rs.Legend) != 5 || !strings.HasPrefix(rs.Legend[0], "A direct transformation") {
		t.Errorf("legend %v", rs.Legend)
	}
	if a.Nouls[router.QMissingContext] != 0.1 {
		t.Errorf("noul %v", a.Nouls)
	}
	ch := a.Choices[router.QOutputContract]
	if ch.Choice == "" || ch.Confidence != 1 || len(ch.Probabilities) != 5 {
		t.Errorf("choice %+v", ch)
	}

	// State shape: no context key when context is empty.
	var req map[string]any
	_ = json.Unmarshal(cap.bodies[0], &req)
	state := req["state"].(map[string]any)
	if state["task"] != "Fix the typo." {
		t.Errorf("state task %v", state)
	}
	if _, has := state["context"]; has {
		t.Error("context key present when empty")
	}
	if req["model"] != "jev-1.13.0" {
		t.Errorf("model %v", req["model"])
	}
	if _, ok := req["questions"].(map[string]any)["reasoning"]; !ok {
		t.Error("questions not forwarded")
	}

	// With context.
	if _, err := c.Analyze(context.Background(), router.Task{Text: "t", Context: "supporting"}); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(cap.bodies[1], &req)
	if req["state"].(map[string]any)["context"] != "supporting" {
		t.Error("context not forwarded")
	}
}

func TestUnprocessableSurfacesDetailAndDoesNotRetry(t *testing.T) {
	spec := loadSpec(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":"questions.task_family.criteria: invalid"}`))
	}))
	defer srv.Close()
	var sleeps int32
	c := newClient(t, srv, spec, &sleeps)
	_, err := c.Analyze(context.Background(), router.Task{Text: "x"})
	var pe *analyzer.ProviderError
	if !errors.As(err, &pe) || pe.Status != 422 || pe.Retryable {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(pe.Detail, "task_family") {
		t.Errorf("detail %q", pe.Detail)
	}
	if calls != 1 || sleeps != 0 {
		t.Errorf("calls=%d sleeps=%d", calls, sleeps)
	}
}

func TestRateLimitedThenSuccess(t *testing.T) {
	spec := loadSpec(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if n == 2 {
			w.WriteHeader(529)
			return
		}
		_ = json.NewEncoder(w).Encode(goodResponse(spec))
	}))
	defer srv.Close()
	var sleeps int32
	c := newClient(t, srv, spec, &sleeps)
	if _, err := c.Analyze(context.Background(), router.Task{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || sleeps != 2 {
		t.Errorf("calls=%d sleeps=%d", calls, sleeps)
	}
}

func TestRateLimitExhausted(t *testing.T) {
	spec := loadSpec(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer srv.Close()
	var sleeps int32
	c := newClient(t, srv, spec, &sleeps)
	_, err := c.Analyze(context.Background(), router.Task{Text: "x"})
	var pe *analyzer.ProviderError
	if !errors.As(err, &pe) || pe.Status != 429 || !pe.Retryable {
		t.Fatalf("err %v", err)
	}
	if sleeps != 2 {
		t.Errorf("sleeps=%d", sleeps)
	}
}

func TestUnauthorizedNotRetried(t *testing.T) {
	spec := loadSpec(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv, spec, nil)
	_, err := c.Analyze(context.Background(), router.Task{Text: "x"})
	var pe *analyzer.ProviderError
	if !errors.As(err, &pe) || pe.Status != 401 || pe.Retryable || pe.Detail != "" {
		t.Fatalf("err %v", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d", calls)
	}
}

func TestServerErrorRetriedOnce(t *testing.T) {
	spec := loadSpec(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newClient(t, srv, spec, nil)
	if _, err := c.Analyze(context.Background(), router.Task{Text: "x"}); err == nil {
		t.Fatal("expected error")
	}
	if calls != 2 {
		t.Errorf("calls=%d", calls)
	}
}

func TestMalformedDistributionRejected(t *testing.T) {
	spec := loadSpec(t)
	cases := map[string]func(map[string]any){
		"sum": func(r map[string]any) {
			r["answers"].(map[string]any)["reasoning"].(map[string]any)["probabilities"].(map[string]float64)["1"] = 0.2
		},
		"choice not in options": func(r map[string]any) {
			r["answers"].(map[string]any)["verification"].(map[string]any)["choice"] = "vibes"
		},
		"missing answer": func(r map[string]any) {
			delete(r["answers"].(map[string]any), "workload")
		},
		"type mismatch": func(r map[string]any) {
			r["answers"].(map[string]any)["missing_context"] = map[string]any{"type": "score", "score": 1}
		},
		"score out of range": func(r map[string]any) {
			r["answers"].(map[string]any)["reasoning"].(map[string]any)["score"] = 4.5
		},
		"noul out of range": func(r map[string]any) {
			r["answers"].(map[string]any)["needs_web_research"].(map[string]any)["noul"] = 1.5
		},
		"no model": func(r map[string]any) { r["model"] = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			resp := goodResponse(spec)
			mutate(resp)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer srv.Close()
			c := newClient(t, srv, spec, nil)
			if _, err := c.Analyze(context.Background(), router.Task{Text: "x"}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	spec := loadSpec(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		chunk := strings.Repeat("x", 1024*1024)
		for i := 0; i < 5; i++ {
			_, _ = w.Write([]byte(chunk))
		}
	}))
	defer srv.Close()
	c := newClient(t, srv, spec, nil)
	_, err := c.Analyze(context.Background(), router.Task{Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err %v", err)
	}
	if calls != 1 {
		t.Errorf("oversized body must not be retried; calls=%d", calls)
	}
}

func TestContextCancellationStopsRetry(t *testing.T) {
	spec := loadSpec(t)
	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		cancel()
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := newClient(t, srv, spec, nil)
	if _, err := c.Analyze(ctx, router.Task{Text: "x"}); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("calls=%d", calls)
	}
}

func TestListModelsShapes(t *testing.T) {
	spec := loadSpec(t)
	bodies := []string{
		`{"models":[{"name":"jev-1.13.0"},{"name":"jev-latest"}]}`,
		`{"models":[{"id":"jev-latest"},{"id":"jev-1.13.0"}]}`,
		`{"data":[{"id":"jev-1.13.0","object":"model"},{"id":"jev-latest"}]}`,
		`[{"name":"jev-latest"},"jev-1.13.0"]`,
	}
	for _, body := range bodies {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" || r.Method != http.MethodGet {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
			_, _ = w.Write([]byte(body))
		}))
		c := newClient(t, srv, spec, nil)
		got, err := c.ListModels(context.Background())
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if len(got) != 2 || got[0] != "jev-1.13.0" || got[1] != "jev-latest" {
			t.Errorf("%s: got %v", body, got)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := newClient(t, srv, spec, nil)
	_, err := c.ListModels(context.Background())
	var pe *analyzer.ProviderError
	if !errors.As(err, &pe) || pe.Status != 403 {
		t.Fatalf("err %v", err)
	}
}

func TestNewRequiresFields(t *testing.T) {
	spec := loadSpec(t)
	for name, o := range map[string]Options{
		"no key":   {Model: "m", Spec: spec},
		"no model": {APIKey: "k", Spec: spec},
		"no spec":  {APIKey: "k", Model: "m"},
	} {
		if _, err := New(o); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	c, err := New(Options{APIKey: "k", Model: "m", Spec: spec, BaseURL: "https://x.example/"})
	if err != nil {
		t.Fatal(err)
	}
	if c.opt.BaseURL != "https://x.example" || c.opt.Timeout != defaultTimeout || c.opt.MaxAttempts != defaultMaxAttempts {
		t.Errorf("defaults not applied: %+v", c.opt)
	}
}
