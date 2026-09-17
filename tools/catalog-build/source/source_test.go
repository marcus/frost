package source

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/frost/internal/router"
)

const fixtureDir = "../testdata/2026-09-16"

func load(t *testing.T, src, name string) Payload {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, src, name))
	if err != nil {
		t.Fatal(err)
	}
	return Payload{Source: src, Name: name, Body: b}
}

func overlayForTest() Overlay {
	return Overlay{SchemaVersion: 1, Models: map[string]OverlayModel{
		"sol":    {Label: "Sol", Sources: map[string][]string{"models.dev": {"openai/gpt-5.6-sol"}, "artificialanalysis": {"gpt-5-6-sol-high", "gpt-5-6-sol"}}},
		"opus-5": {Label: "Opus 5", Sources: map[string][]string{"models.dev": {"anthropic/claude-opus-5"}, "swebench": {"claude-opus-4-5-20251101"}, "artificialanalysis": {"claude-opus-5-high"}}},
		"haiku":  {Label: "Haiku", Sources: map[string][]string{"swebench": {"claude-haiku-4-5-20251001"}}},
		"jev":    {Label: "Jev", Sources: map[string][]string{}, OutputContracts: []string{"typed_decision"}},
	}, Ignore: map[string][]string{"swebench": {"Attempts - 2+"}}}
}

func TestModelsDevNormalize(t *testing.T) {
	c, err := ModelsDev{}.Normalize([]Payload{load(t, "models.dev", "models.json"), load(t, "models.dev", "api.json")}, overlayForTest())
	if err != nil {
		t.Fatal(err)
	}
	sol := c.Facts["sol"]
	if sol.Label != "GPT-5.6 Sol" || sol.ContextTokens != 1050000 {
		t.Fatalf("sol facts %+v", sol)
	}
	if strings.Join(sol.InputModalities, ",") != "text,image" {
		t.Fatalf("modalities %v (pdf must be dropped)", sol.InputModalities)
	}
	if strings.Join(sol.OutputContracts, ",") != "generated_text,code_edit,structured_object" {
		t.Fatalf("contracts %v", sol.OutputContracts)
	}
	if strings.Join(sol.EffortOptions, ",") != "none,low,medium,high,xhigh,max" {
		t.Fatalf("effort options %v", sol.EffortOptions)
	}
	prices := 0
	for _, m := range c.Measurements {
		if m.ModelID == "sol" && strings.HasPrefix(m.Measurement.Metric, "price.") {
			prices++
			if m.Measurement.Provider != "openai" || m.Measurement.Source != "models.dev:openai/gpt-5.6-sol" || m.Measurement.HigherIsBetter {
				t.Fatalf("price attribution %+v", m.Measurement)
			}
		}
	}
	if prices != 2 {
		t.Fatalf("expected input and output price, got %d", prices)
	}
	var unmapped []string
	for _, u := range c.Unmapped {
		unmapped = append(unmapped, u.SourceID)
	}
	if !contains(unmapped, "anthropic/claude-fable-5") || contains(unmapped, "openai/gpt-5.6-sol") {
		t.Fatalf("unmapped %v", unmapped)
	}
	dropped := false
	for _, n := range c.Notes {
		if n.Kind == "modality_dropped" && strings.Contains(n.Message, "pdf") {
			dropped = true
		}
	}
	if !dropped {
		t.Fatalf("expected a modality_dropped note: %+v", c.Notes)
	}
}

func TestModelsDevWithoutAPIPayload(t *testing.T) {
	c, err := ModelsDev{}.Normalize([]Payload{load(t, "models.dev", "models.json")}, overlayForTest())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Measurements) != 0 || c.Facts["sol"].Label == "" {
		t.Fatalf("facts without prices expected: %d measurements", len(c.Measurements))
	}
}

func TestSWEBenchNormalize(t *testing.T) {
	c, err := SWEBench{}.Normalize([]Payload{load(t, "swebench", "leaderboards.json")}, overlayForTest())
	if err != nil {
		t.Fatal(err)
	}
	var opus, haiku []router.Measurement
	for _, m := range c.Measurements {
		switch m.ModelID {
		case "opus-5":
			opus = append(opus, m.Measurement)
		case "haiku":
			haiku = append(haiku, m.Measurement)
		}
	}
	if len(opus) != 2 || len(haiku) != 1 {
		t.Fatalf("opus %d haiku %d", len(opus), len(haiku))
	}
	for _, m := range opus {
		if m.Metric != "swebench.verified.resolve_rate" || m.MetricVersion != "verified-2024-08" || m.TaskFamily != "software_change" || m.Effort != "medium" || !m.HigherIsBetter || m.Unit != "fraction" {
			t.Fatalf("measurement %+v", m)
		}
		if m.Value < 0.7 || m.Value > 0.8 {
			t.Fatalf("value %v should be a fraction", m.Value)
		}
		if !strings.HasPrefix(m.Source, "swebench:") || m.Harness == "" || m.ObservedAt.IsZero() {
			t.Fatalf("attribution %+v", m)
		}
	}
	if haiku[0].Effort != "high" {
		t.Fatalf("haiku effort %q", haiku[0].Effort)
	}
	for _, m := range c.Measurements {
		if strings.Contains(m.Measurement.Source, "2+") {
			t.Fatalf("multi-attempt row imported: %s", m.Measurement.Source)
		}
	}
	var unmapped []string
	for _, u := range c.Unmapped {
		unmapped = append(unmapped, u.SourceID)
	}
	if !contains(unmapped, "gpt-5-2-codex") {
		t.Fatalf("unmapped %v", unmapped)
	}
	for _, u := range c.Unmapped {
		if strings.Contains(u.SourceID, "lite") {
			t.Fatalf("Lite board must be ignored")
		}
	}
}

func TestArtificialAnalysisNormalize(t *testing.T) {
	c, err := ArtificialAnalysis{}.Normalize([]Payload{load(t, "artificialanalysis", "page-1.json"), load(t, "artificialanalysis", "page-2.json")}, overlayForTest())
	if err != nil {
		t.Fatal(err)
	}
	byMetric := map[string]router.Measurement{}
	for _, m := range c.Measurements {
		if m.ModelID == "sol" && m.Measurement.Effort == "high" {
			byMetric[m.Measurement.Metric] = m.Measurement
		}
	}
	if byMetric["aa.coding_index"].Value != 68 || byMetric["aa.coding_index"].MetricVersion != "9.9" || byMetric["aa.ttft_seconds_median"].HigherIsBetter {
		t.Fatalf("sol high measurements %+v", byMetric)
	}
	if byMetric["price.input_usd_per_mtok"].Provider != "artificialanalysis-median" || byMetric["price.input_usd_per_mtok"].MetricVersion != "1" {
		t.Fatalf("price %+v", byMetric["price.input_usd_per_mtok"])
	}
	maxEffort := false
	for _, m := range c.Measurements {
		if m.ModelID == "sol" && m.Measurement.Effort == "max" {
			maxEffort = true
		}
	}
	if !maxEffort {
		t.Fatalf("base AA record should carry effort max")
	}
	if len(c.Latency) != 3 {
		t.Fatalf("latency observations %d", len(c.Latency))
	}
	if len(c.Unmapped) != 2 || c.Unmapped[0].SourceID != "deepseek-v4-1-flash" || c.Unmapped[1].SourceID != "synthetic-unmapped" {
		t.Fatalf("unmapped %+v", c.Unmapped)
	}
	if !(ArtificialAnalysis{}).Restricted() || (ModelsDev{}).Restricted() || (SWEBench{}).Restricted() {
		t.Fatalf("restriction flags wrong")
	}
}

func TestEffortFromName(t *testing.T) {
	cases := map[string]string{
		"GPT-5.6 Sol (high)": "high",
		"GPT-5.6 Sol (max)":  "max",
		"Claude Opus 5 (Adaptive Reasoning, High Effort)":                     "high",
		"Claude Fable 5.1 (Adaptive Reasoning, Max Effort, Default Fallback)": "max",
		"DeepSeek V4.1 Flash (Reasoning, Max Effort)":                         "max",
		"Muse Spark 1.3 (xhigh)":                                              "xhigh",
		"Solar Pro 4":                                                         "",
	}
	for name, want := range cases {
		if got := effortFromName(name); got != want {
			t.Errorf("%q: got %q want %q", name, got, want)
		}
	}
}

func TestAAFetchSkipsWithoutKeyAndPaginates(t *testing.T) {
	if _, err := (ArtificialAnalysis{}).Fetch(context.Background(), http.DefaultClient, Credentials{}); !errors.Is(err, ErrSkipped) {
		t.Fatalf("want ErrSkipped, got %v", err)
	}
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		pages++
		more := r.URL.Query().Get("page") == ""
		_, _ = w.Write([]byte(`{"tier":"free","intelligence_index_version":1,"pagination":{"has_more":` + map[bool]string{true: "true", false: "false"}[more] + `},"data":[]}`))
	}))
	defer srv.Close()
	client := &http.Client{Transport: rewriteTransport{srv.URL}}
	got, err := (ArtificialAnalysis{}).Fetch(context.Background(), client, Credentials{ArtificialAnalysisKey: "k"})
	if err != nil || len(got) != 2 || pages != 2 {
		t.Fatalf("payloads %d pages %d err %v", len(got), pages, err)
	}
	if got[1].Name != "page-2.json" || got[0].SHA256 == "" {
		t.Fatalf("payload naming %+v", got)
	}
}

func TestFetchNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer srv.Close()
	client := &http.Client{Transport: rewriteTransport{srv.URL}}
	if _, err := (ModelsDev{}).Fetch(context.Background(), client, Credentials{}); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err %v", err)
	}
}

// rewriteTransport sends every request to the test server regardless of host.
type rewriteTransport struct{ base string }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := r.base + req.URL.Path
	if req.URL.RawQuery != "" {
		u += "?" + req.URL.RawQuery
	}
	clone := req.Clone(req.Context())
	var err error
	clone.URL, err = clone.URL.Parse(u)
	if err != nil {
		return nil, err
	}
	clone.Host = clone.URL.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
