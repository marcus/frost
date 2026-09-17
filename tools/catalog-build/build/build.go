// Package build assembles a Frost catalog from source contributions, the
// previous catalog, and operator overrides, then diffs and publishes it.
package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/marcus/frost/internal/catalog"
	"github.com/marcus/frost/internal/router"
	"github.com/marcus/frost/tools/catalog-build/source"
)

// LoadOverlay reads and checks the reviewed identity table.
func LoadOverlay(path string) (source.Overlay, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return source.Overlay{}, err
	}
	var o source.Overlay
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return o, fmt.Errorf("overlay %s: %w", path, err)
	}
	if o.SchemaVersion != 1 {
		return o, fmt.Errorf("overlay %s: schema_version must be 1", path)
	}
	seen := map[string]string{}
	for id, m := range o.Models {
		if m.Label == "" {
			return o, fmt.Errorf("overlay %s: model %s needs a label", path, id)
		}
		for src, ids := range m.Sources {
			if _, ok := source.ByName(src); !ok {
				return o, fmt.Errorf("overlay %s: model %s names unknown source %q", path, id, src)
			}
			for _, sid := range ids {
				key := src + "\x00" + sid
				if owner, dup := seen[key]; dup {
					return o, fmt.Errorf("overlay %s: %s ID %q is claimed by both %s and %s", path, src, sid, owner, id)
				}
				seen[key] = id
			}
		}
	}
	return o, nil
}

// Overrides is the operator's local patch applied after every source.
type Overrides struct {
	SchemaVersion int                      `json:"schema_version"`
	Models        map[string]ModelOverride `json:"models"`
}

// ModelOverride patches one model. Set fields replace source values;
// measurements are added verbatim or removed by matching every non-empty
// selector field.
type ModelOverride struct {
	Set                map[string]json.RawMessage `json:"set,omitempty"`
	AddMeasurements    []router.Measurement       `json:"add_measurements,omitempty"`
	RemoveMeasurements []MeasurementSelector      `json:"remove_measurements,omitempty"`
}

// MeasurementSelector matches measurements by any combination of fields.
type MeasurementSelector struct {
	Source        string `json:"source,omitempty"`
	Metric        string `json:"metric,omitempty"`
	MetricVersion string `json:"metric_version,omitempty"`
	Effort        string `json:"effort,omitempty"`
	Provider      string `json:"provider,omitempty"`
}

func (s MeasurementSelector) matches(m router.Measurement) bool {
	return (s.Source == "" || s.Source == m.Source) &&
		(s.Metric == "" || s.Metric == m.Metric) &&
		(s.MetricVersion == "" || s.MetricVersion == m.MetricVersion) &&
		(s.Effort == "" || s.Effort == m.Effort) &&
		(s.Provider == "" || s.Provider == m.Provider)
}

var settableFields = []string{"label", "aliases", "output_contracts", "input_modalities", "generation_mechanism", "context_tokens"}

// LoadOverrides reads the operator overrides file. A missing file is an
// empty override set.
func LoadOverrides(path string) (Overrides, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Overrides{SchemaVersion: 1}, nil
	}
	if err != nil {
		return Overrides{}, err
	}
	var o Overrides
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return o, fmt.Errorf("overrides %s: %w", path, err)
	}
	if o.SchemaVersion != 1 {
		return o, fmt.Errorf("overrides %s: schema_version must be 1", path)
	}
	for id, m := range o.Models {
		for k := range m.Set {
			if !slices.Contains(settableFields, k) {
				return o, fmt.Errorf("overrides %s: model %s: field %q cannot be overridden (allowed: %s)", path, id, k, strings.Join(settableFields, ", "))
			}
		}
	}
	return o, nil
}

// SourceResult is what one source produced, or why it did not.
type SourceResult struct {
	Name         string              `json:"name"`
	Status       string              `json:"status"` // ok | skipped | failed
	Error        string              `json:"error,omitempty"`
	Payloads     []source.Payload    `json:"payloads,omitempty"`
	Contribution source.Contribution `json:"-"`
}

// Assembly is the produced catalog with everything the diff needs.
type Assembly struct {
	Catalog       CatalogFile
	Restricted    *CatalogFile // catalog plus restricted measurements, when requested
	Overridden    map[string][]string
	Unmapped      []source.Unmapped
	Notes         []source.Note
	EffortOptions map[string][]string
	Suggestions   LatencySuggestions
	Retained      []string // sources whose previous data was kept because they failed
}

// CatalogFile is the on-disk catalog shape the producer writes.
type CatalogFile struct {
	SchemaVersion int            `json:"schema_version"`
	Version       string         `json:"version"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Models        []router.Model `json:"models"`
}

// Assemble merges contributions using the public catalog as the only prior.
// Call AssembleWithRestrictedPrevious when restricted source data can be
// retained from a separately published catalog.
func Assemble(results []SourceResult, overlay source.Overlay, overrides Overrides, previous *router.Catalog, now time.Time, restrictedOut bool) (Assembly, error) {
	return AssembleWithRestrictedPrevious(results, overlay, overrides, previous, nil, now, restrictedOut)
}

// AssembleWithRestrictedPrevious merges contributions. Failed and skipped
// sources keep measurements from their matching prior catalog; models.dev also
// keeps its prior public model facts.
func AssembleWithRestrictedPrevious(results []SourceResult, overlay source.Overlay, overrides Overrides, previous, restrictedPrevious *router.Catalog, now time.Time, restrictedOut bool) (Assembly, error) {
	a := Assembly{Overridden: map[string][]string{}, EffortOptions: map[string][]string{}}
	facts := map[string]source.ModelFacts{}
	var measurements []source.Measured
	var restricted []source.Measured
	var latency []source.LatencyObservation
	for _, r := range results {
		src, _ := source.ByName(r.Name)
		switch r.Status {
		case "ok":
			for id, f := range r.Contribution.Facts {
				facts[id] = f
				if len(f.EffortOptions) > 0 {
					a.EffortOptions[id] = f.EffortOptions
				}
			}
			if src != nil && src.Restricted() {
				restricted = append(restricted, r.Contribution.Measurements...)
				if restrictedOut {
					latency = append(latency, r.Contribution.Latency...)
				}
			} else {
				measurements = append(measurements, r.Contribution.Measurements...)
				latency = append(latency, r.Contribution.Latency...)
			}
			a.Unmapped = append(a.Unmapped, r.Contribution.Unmapped...)
			a.Notes = append(a.Notes, r.Contribution.Notes...)
		case "failed", "skipped":
			prior := previous
			if src != nil && src.Restricted() {
				prior = restrictedPrevious
			}
			if prior != nil {
				a.Retained = append(a.Retained, r.Name)
				for id, m := range prior.Models {
					for _, ms := range m.Measurements {
						if strings.HasPrefix(ms.Source, r.Name+":") {
							kept := source.Measured{ModelID: id, Measurement: ms}
							if src != nil && src.Restricted() {
								restricted = append(restricted, kept)
							} else {
								measurements = append(measurements, kept)
							}
						}
					}
				}
			}
			if r.Status == "failed" {
				a.Notes = append(a.Notes, source.Note{Source: r.Name, Kind: "source_failed", Message: r.Error})
			} else {
				message := "no credential; nothing imported"
				if prior != nil {
					message = "no credential; previous data retained"
				}
				a.Notes = append(a.Notes, source.Note{Source: r.Name, Kind: "source_skipped", Message: message})
			}
		}
	}

	ids := make([]string, 0, len(overlay.Models))
	for id := range overlay.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	build := func(withRestricted bool) []router.Model {
		var models []router.Model
		for _, id := range ids {
			om := overlay.Models[id]
			m := router.Model{ID: id, Label: om.Label, Aliases: om.Aliases, GenerationMechanism: om.GenerationMechanism, OutputContracts: om.OutputContracts, InputModalities: om.InputModalities}
			if f, ok := facts[id]; ok {
				if f.Label != "" && om.Label == "" {
					m.Label = f.Label
				}
				if len(f.OutputContracts) > 0 {
					m.OutputContracts = f.OutputContracts
				}
				if len(f.InputModalities) > 0 {
					m.InputModalities = f.InputModalities
				}
				m.ContextTokens = f.ContextTokens
			} else if previous != nil {
				if pm, ok := previous.Models[id]; ok && slices.Contains(a.Retained, "models.dev") {
					if len(m.OutputContracts) == 0 {
						m.OutputContracts = pm.OutputContracts
					}
					if len(m.InputModalities) == 0 {
						m.InputModalities = pm.InputModalities
					}
					m.ContextTokens = pm.ContextTokens
				}
			}
			if len(m.OutputContracts) == 0 {
				m.OutputContracts = []string{router.ContractGeneratedText}
			}
			if len(m.InputModalities) == 0 {
				m.InputModalities = []string{"text"}
			}
			for _, ms := range measurements {
				if ms.ModelID == id {
					m.Measurements = append(m.Measurements, ms.Measurement)
				}
			}
			if withRestricted {
				for _, ms := range restricted {
					if ms.ModelID == id {
						m.Measurements = append(m.Measurements, ms.Measurement)
					}
				}
			}
			overridden, err := applyOverride(&m, overrides.Models[id])
			if err != nil {
				panic(err) // validated at load time
			}
			if len(overridden) > 0 {
				a.Overridden[id] = overridden
			}
			sortMeasurements(m.Measurements)
			models = append(models, m)
		}
		return models
	}
	publicModels := build(false)
	version, err := contentVersion(publicModels, "")
	if err != nil {
		return a, err
	}
	a.Catalog = CatalogFile{SchemaVersion: catalog.SchemaVersion, Version: version, GeneratedAt: now.UTC(), Models: publicModels}
	if restrictedOut {
		restrictedModels := build(true)
		restrictedVersion, err := contentVersion(restrictedModels, "-restricted")
		if err != nil {
			return a, err
		}
		rc := CatalogFile{SchemaVersion: catalog.SchemaVersion, Version: restrictedVersion, GeneratedAt: now.UTC(), Models: restrictedModels}
		a.Restricted = &rc
	}
	a.Suggestions = suggestLatency(latency, overlay, now, restrictedOut)
	slices.SortFunc(a.Unmapped, func(x, y source.Unmapped) int {
		if x.Source != y.Source {
			return strings.Compare(x.Source, y.Source)
		}
		return strings.Compare(x.SourceID, y.SourceID)
	})
	if _, err := catalog.Parse(mustJSON(a.Catalog)); err != nil {
		return a, fmt.Errorf("assembled catalog is invalid: %w", err)
	}
	if a.Restricted != nil {
		if _, err := catalog.Parse(mustJSON(*a.Restricted)); err != nil {
			return a, fmt.Errorf("assembled restricted catalog is invalid: %w", err)
		}
	}
	return a, nil
}

func contentVersion(models []router.Model, suffix string) (string, error) {
	content := struct {
		SchemaVersion int            `json:"schema_version"`
		Models        []router.Model `json:"models"`
	}{SchemaVersion: catalog.SchemaVersion, Models: models}
	raw, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("encode catalog content for version: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "content-" + hex.EncodeToString(sum[:]) + suffix, nil
}

func applyOverride(m *router.Model, o ModelOverride) ([]string, error) {
	var touched []string
	for field, raw := range o.Set {
		var err error
		switch field {
		case "label":
			err = json.Unmarshal(raw, &m.Label)
		case "aliases":
			err = json.Unmarshal(raw, &m.Aliases)
		case "output_contracts":
			err = json.Unmarshal(raw, &m.OutputContracts)
		case "input_modalities":
			err = json.Unmarshal(raw, &m.InputModalities)
		case "generation_mechanism":
			err = json.Unmarshal(raw, &m.GenerationMechanism)
		case "context_tokens":
			err = json.Unmarshal(raw, &m.ContextTokens)
		}
		if err != nil {
			return nil, fmt.Errorf("override %s.%s: %w", m.ID, field, err)
		}
		touched = append(touched, field)
	}
	if len(o.RemoveMeasurements) > 0 {
		kept := m.Measurements[:0]
		removed := 0
		for _, ms := range m.Measurements {
			drop := false
			for _, sel := range o.RemoveMeasurements {
				if sel.matches(ms) {
					drop = true
					break
				}
			}
			if drop {
				removed++
			} else {
				kept = append(kept, ms)
			}
		}
		m.Measurements = kept
		if removed > 0 {
			touched = append(touched, fmt.Sprintf("measurements(-%d)", removed))
		}
	}
	if len(o.AddMeasurements) > 0 {
		for _, ms := range o.AddMeasurements {
			if ms.Source == "" {
				ms.Source = "override"
			}
			m.Measurements = append(m.Measurements, ms)
		}
		touched = append(touched, fmt.Sprintf("measurements(+%d)", len(o.AddMeasurements)))
	}
	sort.Strings(touched)
	return touched, nil
}

func sortMeasurements(ms []router.Measurement) {
	slices.SortFunc(ms, func(a, b router.Measurement) int {
		return strings.Compare(measurementKey(a), measurementKey(b))
	})
}

func measurementKey(m router.Measurement) string {
	return strings.Join([]string{m.Metric, m.MetricVersion, m.Source, m.Effort, m.Harness, m.Provider}, "|")
}

func mustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

// PreviousPath returns the backup path Publish uses for an existing catalog.
func PreviousPath(path string) string {
	dir := filepath.Dir(path)
	return filepath.Join(dir, strings.TrimSuffix(filepath.Base(path), ".json")+".previous.json")
}

// Publish validates and atomically replaces the catalog at path, keeping the
// previous file at PreviousPath(path).
func Publish(path string, c CatalogFile) error {
	raw := mustJSON(c)
	if _, err := catalog.Parse(raw); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".catalog-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := os.Stat(path); err == nil {
		prev := PreviousPath(path)
		if err := os.Rename(path, prev); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
	}
	return os.Rename(tmpName, path)
}

// WriteJSONAtomic writes any document with the same temp-and-rename rule.
func WriteJSONAtomic(path string, v any) error {
	raw := mustJSON(v)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// LoadCurrent reads an existing catalog; a missing file returns nil.
func LoadCurrent(path string) (*router.Catalog, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	c, _, err := catalog.Load(path)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// LatencySuggestions is the coarse class per model derived from source
// medians. Restricted source values are included only for an explicitly
// requested restricted catalog and are never written into the catalog.
type LatencySuggestions struct {
	SchemaVersion int                          `json:"schema_version"`
	GeneratedAt   time.Time                    `json:"generated_at"`
	DataUsage     string                       `json:"data_usage"`
	Basis         string                       `json:"basis"`
	Suggestions   map[string]LatencySuggestion `json:"suggestions"`
}

// LatencySuggestion explains one class. Class comes from the model's
// first-listed source record; ByEffort lists the class of every effort
// variant the source publishes, because thinking time dominates public
// time-to-first-token for reasoning models and the operator's profile runs
// at one effort.
type LatencySuggestion struct {
	Class      string            `json:"class"`
	TTFTSec    *float64          `json:"ttft_seconds_median,omitempty"`
	TokPerSec  *float64          `json:"output_tokens_per_second,omitempty"`
	SourceID   string            `json:"source_id,omitempty"`
	Source     string            `json:"source,omitempty"`
	ObservedAt time.Time         `json:"observed_at,omitempty"`
	ByEffort   map[string]string `json:"by_effort,omitempty"`
}

// ClassFor applies the plan's thresholds.
func ClassFor(ttft, tps float64) string {
	switch {
	case ttft < 0.5 && tps > 150:
		return "extra_fast"
	case ttft < 1 && tps > 60:
		return "fast"
	case ttft < 3:
		return "medium"
	}
	return "slow"
}

func suggestLatency(obs []source.LatencyObservation, overlay source.Overlay, now time.Time, restrictedOut bool) LatencySuggestions {
	dataUsage := "public"
	basis := "no public latency observations available; classes are unknown"
	if restrictedOut {
		dataUsage = "restricted_local_only"
		basis = "restricted local prior derived from Artificial Analysis medians; do not redistribute; verify against measured end-to-end latency"
	}
	s := LatencySuggestions{SchemaVersion: 1, GeneratedAt: now.UTC(), DataUsage: dataUsage, Basis: basis, Suggestions: map[string]LatencySuggestion{}}
	for id, om := range overlay.Models {
		s.Suggestions[id] = LatencySuggestion{Class: "unknown"}
		// The first listed AA slug is the model's primary variant.
		primary := ""
		if ids := om.Sources["artificialanalysis"]; len(ids) > 0 {
			primary = ids[0]
		}
		byEffort := map[string]string{}
		for _, o := range obs {
			if o.ModelID != id {
				continue
			}
			if o.Effort != "" {
				byEffort[o.Effort] = ClassFor(o.TTFTSec, o.TokPerSec)
			}
			if primary != "" && o.SourceID != primary {
				continue
			}
			ttft, tps := o.TTFTSec, o.TokPerSec
			s.Suggestions[id] = LatencySuggestion{Class: ClassFor(ttft, tps), TTFTSec: &ttft, TokPerSec: &tps, SourceID: o.SourceID, Source: o.Source, ObservedAt: o.ObservedAt}
		}
		if len(byEffort) > 0 {
			sug := s.Suggestions[id]
			sug.ByEffort = byEffort
			s.Suggestions[id] = sug
		}
	}
	return s
}
