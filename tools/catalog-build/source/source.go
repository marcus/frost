// Package source defines the connector interface for public model-data
// sources and the neutral contribution each one returns. A connector knows
// one site's URL and JSON shape; nothing else in the producer does.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/marcus/frost/internal/router"
)

// Payload is one raw document fetched from a source, kept verbatim so
// normalization is reproducible offline.
type Payload struct {
	Source    string    `json:"source"`
	Name      string    `json:"name"` // file name within the source's fixture directory
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
	SHA256    string    `json:"sha256"`
	Body      []byte    `json:"-"`
}

// Credentials carries per-source secrets read from the environment by the
// caller. A missing credential makes an optional source skip itself.
type Credentials struct {
	ArtificialAnalysisKey string
}

// Overlay is the reviewed identity table: the only way a source record
// becomes a Frost model.
type Overlay struct {
	SchemaVersion int                     `json:"schema_version"`
	Models        map[string]OverlayModel `json:"models"`
	Ignore        map[string][]string     `json:"ignore,omitempty"` // source -> substrings of records to skip
}

// OverlayModel names one Frost model and its IDs in each source. Defaults
// apply when no source supplies the fact (a model absent from every source
// still needs output contracts to be valid).
type OverlayModel struct {
	Label               string              `json:"label"`
	Sources             map[string][]string `json:"sources"`
	Aliases             []string            `json:"aliases,omitempty"`
	OutputContracts     []string            `json:"output_contracts,omitempty"`
	InputModalities     []string            `json:"input_modalities,omitempty"`
	GenerationMechanism string              `json:"generation_mechanism,omitempty"`
	Notes               string              `json:"notes,omitempty"`
}

// Lookup returns the Frost model ID that owns a source ID.
func (o Overlay) Lookup(source, sourceID string) (string, bool) {
	ids := make([]string, 0, len(o.Models))
	for id := range o.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, s := range o.Models[id].Sources[source] {
			if s == sourceID {
				return id, true
			}
		}
	}
	return "", false
}

// Ignored reports whether a source record should be skipped silently.
func (o Overlay) Ignored(source, text string) bool {
	for _, sub := range o.Ignore[source] {
		if strings.Contains(text, sub) {
			return true
		}
	}
	return false
}

// ModelFacts are identity and capability facts a source states about a
// model. Empty values mean the source did not say.
type ModelFacts struct {
	Label           string
	InputModalities []string
	ContextTokens   int
	OutputContracts []string
	EffortOptions   []string // a hint for operator config, surfaced in the diff
	ReleaseDate     string
}

// Measured is one measurement attributed to a Frost model.
type Measured struct {
	ModelID     string
	Measurement router.Measurement
}

// LatencyObservation is a public median used only to suggest a coarse
// latency class. It never enters the catalog.
type LatencyObservation struct {
	ModelID    string
	SourceID   string
	Effort     string
	TTFTSec    float64
	TokPerSec  float64
	ObservedAt time.Time
	Source     string
}

// Unmapped is a source record with no overlay entry.
type Unmapped struct {
	Source   string `json:"source"`
	SourceID string `json:"source_id"`
	Label    string `json:"label"`
}

// Note is a non-fatal finding surfaced in the diff.
type Note struct {
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Contribution is everything one source adds after normalization.
type Contribution struct {
	Source       string
	Facts        map[string]ModelFacts
	Measurements []Measured
	Latency      []LatencyObservation
	Unmapped     []Unmapped
	Notes        []Note
}

// Source is one connector.
type Source interface {
	Name() string
	// Fetch downloads the source's documents. An optional source with no
	// credential returns ErrSkipped.
	Fetch(ctx context.Context, client *http.Client, creds Credentials) ([]Payload, error)
	// Normalize maps recorded payloads to a contribution without network.
	Normalize(payloads []Payload, overlay Overlay) (Contribution, error)
	// Restricted reports whether the source's data may only be written to a
	// non-distributable catalog.
	Restricted() bool
}

// ErrSkipped means an optional source had nothing to contribute (no key).
var ErrSkipped = errors.New("source skipped")

// All returns every connector in fetch order.
func All() []Source {
	return []Source{ModelsDev{}, SWEBench{}, ArtificialAnalysis{}}
}

// ByName finds a connector.
func ByName(name string) (Source, bool) {
	for _, s := range All() {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

// maxBody bounds any single document.
const maxBody = 32 << 20

func fetchJSON(ctx context.Context, client *http.Client, source, name, url string, headers map[string]string) (Payload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Payload{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "frost-catalog-build")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Payload{}, fmt.Errorf("%s: %w", source, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return Payload{}, fmt.Errorf("%s: read: %w", source, err)
	}
	if len(body) > maxBody {
		return Payload{}, fmt.Errorf("%s: document exceeds %d bytes", source, maxBody)
	}
	if resp.StatusCode != http.StatusOK {
		return Payload{}, fmt.Errorf("%s: HTTP %d from %s", source, resp.StatusCode, url)
	}
	sum := sha256.Sum256(body)
	return Payload{Source: source, Name: name, URL: url, FetchedAt: time.Now().UTC(), SHA256: hex.EncodeToString(sum[:]), Body: body}, nil
}

func payloadNamed(payloads []Payload, name string) (Payload, bool) {
	for _, p := range payloads {
		if p.Name == name {
			return p, true
		}
	}
	return Payload{}, false
}

func parseDate(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
