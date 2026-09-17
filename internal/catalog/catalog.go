// Package catalog parses the neutral, source-attributed model catalog into
// router types. It knows the file format and nothing about which public
// sources produced it.
package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/marcus/frost/internal/router"
)

// SchemaVersion is the catalog file schema this package reads.
const SchemaVersion = 1

// file is the on-disk shape. Models are a list so the file stays diffable;
// the router receives them keyed by ID.
type file struct {
	SchemaVersion int            `json:"schema_version"`
	Version       string         `json:"version"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Models        []router.Model `json:"models"`
}

var validContracts = []string{
	router.ContractGeneratedText,
	router.ContractCodeEdit,
	router.ContractStructuredObj,
	router.ContractTypedDecision,
}

var validModalities = []string{"text", "image", "audio"}

// Load reads and validates a catalog file. The second result is the SHA-256
// hex digest of the raw bytes, for provenance.
func Load(path string) (router.Catalog, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return router.Catalog{}, "", fmt.Errorf("read catalog: %w", err)
	}
	cat, err := Parse(raw)
	if err != nil {
		return router.Catalog{}, "", fmt.Errorf("catalog %s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	return cat, hex.EncodeToString(sum[:]), nil
}

// Parse decodes and validates catalog bytes. Unknown fields are errors so a
// typo cannot silently drop evidence.
func Parse(raw []byte) (router.Catalog, error) {
	var f file
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return router.Catalog{}, fmt.Errorf("decode: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return router.Catalog{}, errors.New("trailing data after catalog object")
	}
	if f.SchemaVersion != SchemaVersion {
		return router.Catalog{}, fmt.Errorf("unsupported schema_version %d (want %d)", f.SchemaVersion, SchemaVersion)
	}
	if f.Version == "" {
		return router.Catalog{}, errors.New("version is required")
	}
	cat := router.Catalog{Version: f.Version, Models: make(map[string]router.Model, len(f.Models))}
	names := map[string]string{} // ID or alias -> owning model ID
	for i, m := range f.Models {
		where := fmt.Sprintf("models[%d]", i)
		if m.ID == "" {
			return router.Catalog{}, fmt.Errorf("%s: id is required", where)
		}
		where += " (" + m.ID + ")"
		if owner, dup := names[m.ID]; dup {
			return router.Catalog{}, fmt.Errorf("%s: id collides with %q", where, owner)
		}
		names[m.ID] = m.ID
		for _, a := range m.Aliases {
			if a == "" {
				return router.Catalog{}, fmt.Errorf("%s: empty alias", where)
			}
			if owner, dup := names[a]; dup {
				return router.Catalog{}, fmt.Errorf("%s: alias %q collides with %q", where, a, owner)
			}
			names[a] = m.ID
		}
		if len(m.OutputContracts) == 0 {
			return router.Catalog{}, fmt.Errorf("%s: output_contracts must not be empty", where)
		}
		for _, c := range m.OutputContracts {
			if !slices.Contains(validContracts, c) {
				return router.Catalog{}, fmt.Errorf("%s: unknown output contract %q", where, c)
			}
		}
		for _, mod := range m.InputModalities {
			if !slices.Contains(validModalities, mod) {
				return router.Catalog{}, fmt.Errorf("%s: unknown input modality %q", where, mod)
			}
		}
		for j, ms := range m.Measurements {
			if ms.Source == "" || ms.Metric == "" || ms.MetricVersion == "" {
				return router.Catalog{}, fmt.Errorf("%s: measurements[%d] needs source, metric, and metric_version", where, j)
			}
		}
		cat.Models[m.ID] = m
	}
	return cat, nil
}

// Resolve maps an ID or alias to the canonical model ID.
func Resolve(cat router.Catalog, name string) (string, bool) {
	if _, ok := cat.Models[name]; ok {
		return name, true
	}
	ids := make([]string, 0, len(cat.Models))
	for id := range cat.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if slices.Contains(cat.Models[id].Aliases, name) {
			return id, true
		}
	}
	return "", false
}
