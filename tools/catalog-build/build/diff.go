package build

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/marcus/frost/internal/router"
	"github.com/marcus/frost/tools/catalog-build/source"
)

// Diff summarizes what a candidate catalog changes relative to the current
// one. generated_at is ignored so an unchanged refresh produces an empty
// diff.
type Diff struct {
	CurrentVersion      string              `json:"current_version,omitempty"`
	CandidateVersion    string              `json:"candidate_version"`
	AddedModels         []string            `json:"added_models"`
	RemovedModels       []string            `json:"removed_models"`
	ChangedFields       map[string][]string `json:"changed_fields"` // model -> fields
	AddedMeasurements   map[string]int      `json:"added_measurements"`
	RemovedMeasurements map[string]int      `json:"removed_measurements"`
	Overridden          map[string][]string `json:"overridden,omitempty"`
	Unmapped            []source.Unmapped   `json:"unmapped"`
	Notes               []source.Note       `json:"notes"`
	Retained            []string            `json:"retained_sources,omitempty"`
	EffortOptions       map[string][]string `json:"effort_options,omitempty"`
}

// Empty reports whether nothing about the models or measurements changed.
func (d Diff) Empty() bool {
	return len(d.AddedModels) == 0 && len(d.RemovedModels) == 0 && len(d.ChangedFields) == 0 && len(d.AddedMeasurements) == 0 && len(d.RemovedMeasurements) == 0
}

// Compute builds the diff. current may be nil.
func Compute(current *router.Catalog, a Assembly) Diff {
	d := Diff{CandidateVersion: a.Catalog.Version, ChangedFields: map[string][]string{}, AddedMeasurements: map[string]int{}, RemovedMeasurements: map[string]int{}, Overridden: a.Overridden, Unmapped: a.Unmapped, Notes: a.Notes, Retained: a.Retained, EffortOptions: a.EffortOptions, AddedModels: []string{}, RemovedModels: []string{}}
	if d.Unmapped == nil {
		d.Unmapped = []source.Unmapped{}
	}
	if d.Notes == nil {
		d.Notes = []source.Note{}
	}
	cand := map[string]router.Model{}
	for _, m := range a.Catalog.Models {
		cand[m.ID] = m
	}
	if current == nil {
		for id := range cand {
			d.AddedModels = append(d.AddedModels, id)
		}
		sort.Strings(d.AddedModels)
		return d
	}
	d.CurrentVersion = current.Version
	for id, cm := range cand {
		pm, ok := current.Models[id]
		if !ok {
			d.AddedModels = append(d.AddedModels, id)
			continue
		}
		var fields []string
		if cm.Label != pm.Label {
			fields = append(fields, "label")
		}
		if !reflect.DeepEqual(nz(cm.Aliases), nz(pm.Aliases)) {
			fields = append(fields, "aliases")
		}
		if !reflect.DeepEqual(nz(cm.OutputContracts), nz(pm.OutputContracts)) {
			fields = append(fields, "output_contracts")
		}
		if !reflect.DeepEqual(nz(cm.InputModalities), nz(pm.InputModalities)) {
			fields = append(fields, "input_modalities")
		}
		if cm.GenerationMechanism != pm.GenerationMechanism {
			fields = append(fields, "generation_mechanism")
		}
		if cm.ContextTokens != pm.ContextTokens {
			fields = append(fields, "context_tokens")
		}
		if len(fields) > 0 {
			d.ChangedFields[id] = fields
		}
		have := map[string]router.Measurement{}
		for _, ms := range pm.Measurements {
			have[measurementKey(ms)] = ms
		}
		want := map[string]router.Measurement{}
		for _, ms := range cm.Measurements {
			want[measurementKey(ms)] = ms
		}
		for k, ms := range want {
			old, ok := have[k]
			if !ok || old.Value != ms.Value || !old.ObservedAt.Equal(ms.ObservedAt) {
				d.AddedMeasurements[id]++
				if ok {
					d.RemovedMeasurements[id]++
				}
			}
		}
		for k := range have {
			if _, ok := want[k]; !ok {
				d.RemovedMeasurements[id]++
			}
		}
	}
	for id := range current.Models {
		if _, ok := cand[id]; !ok {
			d.RemovedModels = append(d.RemovedModels, id)
		}
	}
	sort.Strings(d.AddedModels)
	sort.Strings(d.RemovedModels)
	return d
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Render writes the human diff.
func Render(w io.Writer, d Diff) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	if d.CurrentVersion != "" {
		p("catalog %s -> %s\n", d.CurrentVersion, d.CandidateVersion)
	} else {
		p("new catalog %s\n", d.CandidateVersion)
	}
	if d.Empty() {
		p("no model or measurement changes\n")
	}
	for _, id := range d.AddedModels {
		p("+ model %s\n", id)
	}
	for _, id := range d.RemovedModels {
		p("- model %s\n", id)
	}
	for _, id := range sortedKeys(d.ChangedFields) {
		p("~ %s: %s\n", id, strings.Join(d.ChangedFields[id], ", "))
	}
	for _, id := range sortedKeysInt(d.AddedMeasurements) {
		p("+ %s: %d measurement(s)\n", id, d.AddedMeasurements[id])
	}
	for _, id := range sortedKeysInt(d.RemovedMeasurements) {
		p("- %s: %d measurement(s)\n", id, d.RemovedMeasurements[id])
	}
	for _, id := range sortedKeys(d.Overridden) {
		p("! %s overridden: %s\n", id, strings.Join(d.Overridden[id], ", "))
	}
	for _, s := range d.Retained {
		p("! %s unavailable; previous data retained\n", s)
	}
	counts := map[string]int{}
	for _, u := range d.Unmapped {
		counts[u.Source]++
	}
	for _, src := range sortedKeysInt(counts) {
		p("? %s: %d unmapped records (review with propose-aliases; full list in --json)\n", src, counts[src])
	}
	for _, n := range d.Notes {
		p("note %s/%s: %s\n", n.Source, n.Kind, n.Message)
	}
	for _, id := range sortedKeys(d.EffortOptions) {
		p("effort options %s: %s\n", id, strings.Join(d.EffortOptions[id], ", "))
	}
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysInt(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ProposeAliases ranks overlay models for each unmapped source record by
// normalized token overlap between labels. Suggestions are never applied.
type Proposal struct {
	Source      string   `json:"source"`
	SourceID    string   `json:"source_id"`
	Label       string   `json:"label"`
	Suggestions []string `json:"suggestions"`
}

// ProposeAliases produces review candidates.
func ProposeAliases(unmapped []source.Unmapped, overlay source.Overlay) []Proposal {
	type scored struct {
		id    string
		score float64
	}
	var out []Proposal
	for _, u := range unmapped {
		text := tokens(u.Label + " " + u.SourceID)
		var ranked []scored
		for id, om := range overlay.Models {
			cand := tokens(om.Label + " " + id)
			overlap := 0
			for t := range text {
				if cand[t] {
					overlap++
				}
			}
			if overlap == 0 {
				continue
			}
			ranked = append(ranked, scored{id, float64(overlap) / float64(len(cand)+len(text)-overlap)})
		}
		sort.Slice(ranked, func(i, j int) bool {
			if ranked[i].score == ranked[j].score {
				return ranked[i].id < ranked[j].id
			}
			return ranked[i].score > ranked[j].score
		})
		p := Proposal{Source: u.Source, SourceID: u.SourceID, Label: u.Label, Suggestions: []string{}}
		for i, r := range ranked {
			if i == 3 {
				break
			}
			p.Suggestions = append(p.Suggestions, r.id)
		}
		out = append(out, p)
	}
	return out
}

func tokens(s string) map[string]bool {
	s = strings.ToLower(s)
	repl := strings.NewReplacer("/", " ", "-", " ", "_", " ", ".", " ", "(", " ", ")", " ", ",", " ")
	out := map[string]bool{}
	for _, t := range strings.Fields(repl.Replace(s)) {
		if len(t) > 1 {
			out[t] = true
		}
	}
	return out
}
