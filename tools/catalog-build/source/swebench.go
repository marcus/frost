package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/marcus/frost/internal/router"
)

// SWEBench reads the SWE-bench leaderboard JSON and imports the Verified
// board as software_change measurements. Other boards stay in the fixture
// until a rule needs them.
type SWEBench struct{}

const (
	sweBenchURL     = "https://raw.githubusercontent.com/SWE-bench/swe-bench.github.io/master/data/leaderboards.json"
	sweBenchName    = "swebench"
	sweBenchBoard   = "Verified"
	sweBenchMetric  = "swebench.verified.resolve_rate"
	sweBenchVersion = "verified-2024-08"
)

func (SWEBench) Name() string     { return sweBenchName }
func (SWEBench) Restricted() bool { return false }

func (SWEBench) Fetch(ctx context.Context, client *http.Client, _ Credentials) ([]Payload, error) {
	p, err := fetchJSON(ctx, client, sweBenchName, "leaderboards.json", sweBenchURL, nil)
	if err != nil {
		return nil, err
	}
	return []Payload{p}, nil
}

type sweBenchFile struct {
	Leaderboards []struct {
		Name    string           `json:"name"`
		Results []sweBenchResult `json:"results"`
	} `json:"leaderboards"`
}

type sweBenchResult struct {
	Name            string   `json:"name"`
	Agent           string   `json:"agent"`
	ModelDisplay    string   `json:"model_display"`
	ReasoningEffort *string  `json:"reasoning_effort"`
	Resolved        float64  `json:"resolved"`
	Date            string   `json:"date"`
	Folder          string   `json:"folder"`
	Tags            []string `json:"tags"`
}

func (r sweBenchResult) tag(prefix string) (string, bool) {
	for _, t := range r.Tags {
		if strings.HasPrefix(t, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(t, prefix)), true
		}
	}
	return "", false
}

func (SWEBench) Normalize(payloads []Payload, overlay Overlay) (Contribution, error) {
	c := Contribution{Source: sweBenchName, Facts: map[string]ModelFacts{}}
	p, ok := payloadNamed(payloads, "leaderboards.json")
	if !ok {
		return c, fmt.Errorf("%s: leaderboards.json payload missing", sweBenchName)
	}
	var f sweBenchFile
	if err := json.Unmarshal(p.Body, &f); err != nil {
		return c, fmt.Errorf("%s: %w", sweBenchName, err)
	}
	seenUnmapped := map[string]bool{}
	skipped, untagged := 0, 0
	for _, board := range f.Leaderboards {
		if board.Name != sweBenchBoard {
			continue
		}
		for _, r := range board.Results {
			modelTag, ok := r.tag("Model:")
			if !ok {
				untagged++
				continue
			}
			if overlay.Ignored(sweBenchName, strings.Join(r.Tags, " ")) {
				continue
			}
			if attempts, ok := r.tag("System: Attempts -"); ok && attempts != "1" {
				skipped++
				continue
			}
			frostID, mapped := overlay.Lookup(sweBenchName, modelTag)
			if !mapped {
				if !seenUnmapped[modelTag] {
					seenUnmapped[modelTag] = true
					c.Unmapped = append(c.Unmapped, Unmapped{Source: sweBenchName, SourceID: modelTag, Label: r.ModelDisplay})
				}
				continue
			}
			effort := ""
			if r.ReasoningEffort != nil {
				effort = *r.ReasoningEffort
			}
			c.Measurements = append(c.Measurements, Measured{ModelID: frostID, Measurement: router.Measurement{
				Source:         sweBenchName + ":" + r.Folder,
				Metric:         sweBenchMetric,
				MetricVersion:  sweBenchVersion,
				Value:          r.Resolved / 100,
				Unit:           "fraction",
				HigherIsBetter: true,
				TaskFamily:     "software_change",
				Effort:         effort,
				Harness:        r.Agent,
				ObservedAt:     parseDate(r.Date),
			}})
		}
	}
	if untagged > 0 {
		c.Notes = append(c.Notes, Note{Source: sweBenchName, Kind: "no_model_tag", Message: fmt.Sprintf("%d Verified rows carry no Model tag (agent-only submissions); skipped", untagged)})
	}
	if skipped > 0 {
		c.Notes = append(c.Notes, Note{Source: sweBenchName, Kind: "multi_attempt_skipped", Message: fmt.Sprintf("%d Verified rows with more than one attempt were not imported", skipped)})
	}
	slices.SortFunc(c.Unmapped, func(a, b Unmapped) int { return strings.Compare(a.SourceID, b.SourceID) })
	slices.SortFunc(c.Measurements, func(a, b Measured) int {
		if a.ModelID != b.ModelID {
			return strings.Compare(a.ModelID, b.ModelID)
		}
		return strings.Compare(a.Measurement.Source, b.Measurement.Source)
	})
	return c, nil
}
