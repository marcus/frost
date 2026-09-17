package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/marcus/frost/internal/router"
)

// ModelsDev reads models.dev: identity, capabilities, context limits, and
// the model's own provider prices. Benchmark rows embedded in its records
// are not imported.
type ModelsDev struct{}

const (
	modelsDevModelsURL = "https://models.dev/models.json"
	modelsDevAPIURL    = "https://models.dev/api.json"
	modelsDevName      = "models.dev"
)

func (ModelsDev) Name() string     { return modelsDevName }
func (ModelsDev) Restricted() bool { return false }

func (ModelsDev) Fetch(ctx context.Context, client *http.Client, _ Credentials) ([]Payload, error) {
	models, err := fetchJSON(ctx, client, modelsDevName, "models.json", modelsDevModelsURL, nil)
	if err != nil {
		return nil, err
	}
	api, err := fetchJSON(ctx, client, modelsDevName, "api.json", modelsDevAPIURL, nil)
	if err != nil {
		return nil, err
	}
	return []Payload{models, api}, nil
}

// modelsDevModel is the subset of a models.json or api.json model record
// the connector reads. Both files share this shape.
type modelsDevModel struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ToolCall         bool   `json:"tool_call"`
	StructuredOutput bool   `json:"structured_output"`
	Attachment       bool   `json:"attachment"`
	ReleaseDate      string `json:"release_date"`
	LastUpdated      string `json:"last_updated"`
	Modalities       struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
	} `json:"limit"`
	ReasoningOptions []struct {
		Type   string   `json:"type"`
		Values []string `json:"values"`
	} `json:"reasoning_options"`
	Cost *struct {
		Input     *float64 `json:"input"`
		Output    *float64 `json:"output"`
		CacheRead *float64 `json:"cache_read"`
	} `json:"cost"`
}

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Models map[string]modelsDevModel `json:"models"`
}

func (ModelsDev) Normalize(payloads []Payload, overlay Overlay) (Contribution, error) {
	c := Contribution{Source: modelsDevName, Facts: map[string]ModelFacts{}}
	models, ok := payloadNamed(payloads, "models.json")
	if !ok {
		return c, fmt.Errorf("%s: models.json payload missing", modelsDevName)
	}
	var byID map[string]modelsDevModel
	if err := json.Unmarshal(models.Body, &byID); err != nil {
		return c, fmt.Errorf("%s: models.json: %w", modelsDevName, err)
	}
	var providers map[string]modelsDevProvider
	if api, ok := payloadNamed(payloads, "api.json"); ok {
		if err := json.Unmarshal(api.Body, &providers); err != nil {
			return c, fmt.Errorf("%s: api.json: %w", modelsDevName, err)
		}
	} else {
		c.Notes = append(c.Notes, Note{Source: modelsDevName, Kind: "missing_payload", Message: "api.json absent; prices and effort options not imported"})
	}

	sourceIDs := make([]string, 0, len(byID))
	for id := range byID {
		sourceIDs = append(sourceIDs, id)
	}
	sort.Strings(sourceIDs)
	for _, sid := range sourceIDs {
		rec := byID[sid]
		frostID, mapped := overlay.Lookup(modelsDevName, sid)
		if !mapped {
			if !overlay.Ignored(modelsDevName, sid) {
				c.Unmapped = append(c.Unmapped, Unmapped{Source: modelsDevName, SourceID: sid, Label: rec.Name})
			}
			continue
		}
		if _, dup := c.Facts[frostID]; dup {
			c.Notes = append(c.Notes, Note{Source: modelsDevName, Kind: "duplicate_mapping", Message: fmt.Sprintf("%s maps a second source ID %s; first mapping wins", frostID, sid)})
			continue
		}
		facts := ModelFacts{Label: rec.Name, ContextTokens: rec.Limit.Context, ReleaseDate: rec.ReleaseDate}
		for _, m := range rec.Modalities.Input {
			switch m {
			case "text", "image", "audio":
				facts.InputModalities = append(facts.InputModalities, m)
			default:
				c.Notes = append(c.Notes, Note{Source: modelsDevName, Kind: "modality_dropped", Message: fmt.Sprintf("%s: input modality %q has no Frost equivalent", frostID, m)})
			}
		}
		facts.OutputContracts = []string{router.ContractGeneratedText}
		if rec.ToolCall {
			facts.OutputContracts = append(facts.OutputContracts, router.ContractCodeEdit)
		}
		if rec.ToolCall || rec.StructuredOutput {
			facts.OutputContracts = append(facts.OutputContracts, router.ContractStructuredObj)
		}

		// Prices and effort options come from the model's own organization
		// provider (the prefix of the models.json ID), never from resellers.
		org, short, found := strings.Cut(sid, "/")
		if found && providers != nil {
			if prov, ok := providers[org]; ok {
				if pm, ok := prov.Models[short]; ok {
					for _, ro := range pm.ReasoningOptions {
						if ro.Type == "effort" {
							facts.EffortOptions = append(facts.EffortOptions, ro.Values...)
						}
					}
					observed := parseDate(pm.LastUpdated)
					if observed.IsZero() {
						observed = parseDate(rec.LastUpdated)
					}
					if pm.Cost != nil {
						if pm.Cost.Input != nil {
							c.Measurements = append(c.Measurements, Measured{ModelID: frostID, Measurement: router.Measurement{
								Source: modelsDevName + ":" + sid, Metric: "price.input_usd_per_mtok", MetricVersion: "1", Value: *pm.Cost.Input,
								Unit: "usd_per_million_tokens", HigherIsBetter: false, Provider: org, ObservedAt: observed}})
						}
						if pm.Cost.Output != nil {
							c.Measurements = append(c.Measurements, Measured{ModelID: frostID, Measurement: router.Measurement{
								Source: modelsDevName + ":" + sid, Metric: "price.output_usd_per_mtok", MetricVersion: "1", Value: *pm.Cost.Output,
								Unit: "usd_per_million_tokens", HigherIsBetter: false, Provider: org, ObservedAt: observed}})
						}
					}
				} else {
					c.Notes = append(c.Notes, Note{Source: modelsDevName, Kind: "no_provider_record", Message: fmt.Sprintf("%s: provider %s has no record for %s; no price imported", frostID, org, short)})
				}
			}
		}
		c.Facts[frostID] = facts
	}
	slices.SortFunc(c.Unmapped, func(a, b Unmapped) int { return strings.Compare(a.SourceID, b.SourceID) })
	return c, nil
}
