package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/marcus/frost/internal/router"
)

// ArtificialAnalysis reads the free-tier language model endpoint with a
// bring-your-own key. Its measurements are restricted: internal use with
// attribution, no redistribution.
type ArtificialAnalysis struct{}

const (
	aaURL      = "https://artificialanalysis.ai/api/v2/language/models/free"
	aaName     = "artificialanalysis"
	aaMaxPages = 20
)

func (ArtificialAnalysis) Name() string     { return aaName }
func (ArtificialAnalysis) Restricted() bool { return true }

func (ArtificialAnalysis) Fetch(ctx context.Context, client *http.Client, creds Credentials) ([]Payload, error) {
	if creds.ArtificialAnalysisKey == "" {
		return nil, ErrSkipped
	}
	headers := map[string]string{"x-api-key": creds.ArtificialAnalysisKey}
	var out []Payload
	for page := 1; page <= aaMaxPages; page++ {
		url := aaURL
		if page > 1 {
			url = fmt.Sprintf("%s?page=%d", aaURL, page)
		}
		p, err := fetchJSON(ctx, client, aaName, fmt.Sprintf("page-%d.json", page), url, headers)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
		var probe struct {
			Pagination struct {
				HasMore bool `json:"has_more"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(p.Body, &probe); err != nil {
			return nil, fmt.Errorf("%s: page %d: %w", aaName, page, err)
		}
		if !probe.Pagination.HasMore {
			break
		}
	}
	return out, nil
}

type aaPage struct {
	IntelligenceIndexVersion json.Number `json:"intelligence_index_version"`
	Data                     []aaModel   `json:"data"`
}

type aaModel struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	ReleaseDate string `json:"release_date"`
	Evaluations struct {
		Intelligence *float64 `json:"artificial_analysis_intelligence_index"`
		Coding       *float64 `json:"artificial_analysis_coding_index"`
		Agentic      *float64 `json:"artificial_analysis_agentic_index"`
	} `json:"evaluations"`
	Pricing struct {
		Input  *float64 `json:"price_1m_input_tokens"`
		Output *float64 `json:"price_1m_output_tokens"`
	} `json:"pricing"`
	Performance struct {
		TokPerSec *float64 `json:"median_output_tokens_per_second"`
		TTFT      *float64 `json:"median_time_to_first_token_seconds"`
	} `json:"performance"`
}

var aaEffortInName = regexp.MustCompile(`\b(low|medium|high|xhigh|max) effort\b|\((low|medium|high|xhigh|max)\)`)

// effortFromName reads AA's naming convention ("GPT-5.6 Sol (high)",
// "Claude Opus 5 (Adaptive Reasoning, High Effort)"). Empty when absent.
func effortFromName(name string) string {
	m := aaEffortInName.FindStringSubmatch(strings.ToLower(name))
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

func (ArtificialAnalysis) Normalize(payloads []Payload, overlay Overlay) (Contribution, error) {
	c := Contribution{Source: aaName, Facts: map[string]ModelFacts{}}
	if len(payloads) == 0 {
		return c, fmt.Errorf("%s: no payloads", aaName)
	}
	version := ""
	var fetched time.Time
	seenUnmapped := map[string]bool{}
	for _, p := range payloads {
		var page aaPage
		if err := json.Unmarshal(p.Body, &page); err != nil {
			return c, fmt.Errorf("%s: %s: %w", aaName, p.Name, err)
		}
		if version == "" {
			version = page.IntelligenceIndexVersion.String()
		}
		if fetched.IsZero() {
			fetched = p.FetchedAt
		}
		for _, m := range page.Data {
			frostID, mapped := overlay.Lookup(aaName, m.Slug)
			if !mapped {
				if !seenUnmapped[m.Slug] && !overlay.Ignored(aaName, m.Slug) {
					seenUnmapped[m.Slug] = true
					c.Unmapped = append(c.Unmapped, Unmapped{Source: aaName, SourceID: m.Slug, Label: m.Name})
				}
				continue
			}
			effort := effortFromName(m.Name)
			src := aaName + ":" + m.Slug
			observed := fetched
			add := func(metric, unit string, value *float64, higher bool) {
				if value == nil {
					return
				}
				c.Measurements = append(c.Measurements, Measured{ModelID: frostID, Measurement: router.Measurement{
					Source: src, Metric: metric, MetricVersion: version, Value: *value, Unit: unit,
					HigherIsBetter: higher, Effort: effort, Provider: "artificialanalysis-median", ObservedAt: observed}})
			}
			add("aa.intelligence_index", "index", m.Evaluations.Intelligence, true)
			add("aa.coding_index", "index", m.Evaluations.Coding, true)
			add("aa.agentic_index", "index", m.Evaluations.Agentic, true)
			add("aa.output_tokens_per_second", "tokens_per_second", m.Performance.TokPerSec, true)
			add("aa.ttft_seconds_median", "seconds", m.Performance.TTFT, false)
			if m.Pricing.Input != nil {
				c.Measurements = append(c.Measurements, Measured{ModelID: frostID, Measurement: router.Measurement{
					Source: src, Metric: "price.input_usd_per_mtok", MetricVersion: "1", Value: *m.Pricing.Input, Unit: "usd_per_million_tokens",
					HigherIsBetter: false, Effort: effort, Provider: "artificialanalysis-median", ObservedAt: observed}})
			}
			if m.Pricing.Output != nil {
				c.Measurements = append(c.Measurements, Measured{ModelID: frostID, Measurement: router.Measurement{
					Source: src, Metric: "price.output_usd_per_mtok", MetricVersion: "1", Value: *m.Pricing.Output, Unit: "usd_per_million_tokens",
					HigherIsBetter: false, Effort: effort, Provider: "artificialanalysis-median", ObservedAt: observed}})
			}
			if m.Performance.TTFT != nil && m.Performance.TokPerSec != nil {
				c.Latency = append(c.Latency, LatencyObservation{ModelID: frostID, SourceID: m.Slug, Effort: effort, TTFTSec: *m.Performance.TTFT, TokPerSec: *m.Performance.TokPerSec, ObservedAt: observed, Source: src})
			}
		}
	}
	slices.SortFunc(c.Unmapped, func(a, b Unmapped) int { return strings.Compare(a.SourceID, b.SourceID) })
	return c, nil
}
