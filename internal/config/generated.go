package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/marcus/frost/internal/router"
)

// GeneratedSchemaVersion is the producer-written profiles file schema.
const GeneratedSchemaVersion = 1

// MaxGeneratedBytes caps the producer-written profiles file.
const MaxGeneratedBytes = 1 << 20

// fileGenerated is the producer-written JSON file. It carries the same
// profile, pool, and gateway shapes as the operator's TOML, so a producer
// emits exactly what an operator could write by hand.
type fileGenerated struct {
	SchemaVersion int           `json:"schema_version"`
	GeneratedAt   string        `json:"generated_at"`
	Producer      string        `json:"producer"`
	Gateways      []fileGateway `json:"gateways"`
	Pools         []filePool    `json:"pools"`
	Profiles      []fileProfile `json:"profiles"`
}

// mergeGenerated loads the producer-written file and appends what survives
// structural checks beneath the operator's own declarations. It never fails
// the config: a producer bug must not stop routing over the profiles the
// operator wrote. Every refusal is a Problem that Validate reports, and
// Validate drops generated entries that fail semantic checks the same way.
//
// Precedence and collisions, by ID within each kind (profile, pool, gateway):
//   - an operator declaration always wins; a generated entry with the same ID
//     is ignored (this is how an operator overrides or disables one), noted
//     at info severity;
//   - IDs repeated inside the generated file are ambiguous, so every entry
//     with that ID is ignored;
//   - an entry with no ID is ignored.
func (c *Config) mergeGenerated(path string) {
	g := &Generated{Path: path}
	c.Generated = g
	add := func(sev, format string, args ...any) {
		c.loadProblems = append(c.loadProblems, Problem{Severity: sev, Message: fmt.Sprintf(format, args...), Source: SourceGenerated})
	}
	ignoreFile := func(format string, args ...any) {
		g.Hash = ""
		add(SeverityWarning, "generated profiles %s ignored entirely: %s", path, fmt.Sprintf(format, args...))
	}
	raw, err := readGenerated(path)
	if errors.Is(err, os.ErrNotExist) {
		g.Missing = true
		add(SeverityWarning, "generated_profiles_file %s does not exist; no generated profiles loaded", path)
		return
	}
	if err != nil {
		ignoreFile("%v", err)
		return
	}
	sum := sha256.Sum256(raw)
	g.Hash = hex.EncodeToString(sum[:])

	var f fileGenerated
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		ignoreFile("%v", err)
		return
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		ignoreFile("trailing data after the object")
		return
	}
	if f.SchemaVersion != GeneratedSchemaVersion {
		ignoreFile("unsupported schema_version %d (want %d)", f.SchemaVersion, GeneratedSchemaVersion)
		return
	}
	if f.GeneratedAt != "" {
		if _, err := time.Parse(time.RFC3339, f.GeneratedAt); err != nil {
			ignoreFile("generated_at: %v", err)
			return
		}
	}
	g.Producer, g.GeneratedAt = f.Producer, f.GeneratedAt

	// Gateways first, then pools, then profiles: later kinds reference earlier.
	c.generatedGateway = map[string]bool{}
	for _, i := range acceptable("gateway", f.Gateways, func(g fileGateway) string { return g.ID }, func(id string) bool {
		return slices.ContainsFunc(c.Gateways, func(g Gateway) bool { return g.ID == id })
	}, add) {
		c.Gateways = append(c.Gateways, Gateway{ID: f.Gateways[i].ID, BaseURL: f.Gateways[i].BaseURL})
		c.generatedGateway[f.Gateways[i].ID] = true
		g.Gateways++
	}
	c.generatedPools = map[string]bool{}
	for _, i := range acceptable("pool", f.Pools, func(p filePool) string { return p.ID }, func(id string) bool {
		return slices.ContainsFunc(c.Pools, func(p Pool) bool { return p.ID == id })
	}, add) {
		c.Pools = append(c.Pools, toPool(f.Pools[i]))
		c.generatedPools[f.Pools[i].ID] = true
		g.Pools++
	}
	for _, i := range acceptable("profile", f.Profiles, func(p fileProfile) string { return p.ID }, func(id string) bool {
		return slices.ContainsFunc(c.Profiles, func(p router.Profile) bool { return p.ID == id })
	}, add) {
		p := toProfile(f.Profiles[i])
		p.Source = router.SourceGenerated
		c.Profiles = append(c.Profiles, p)
		g.Profiles++
	}
}

func readGenerated(path string) ([]byte, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, MaxGeneratedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxGeneratedBytes {
		return nil, fmt.Errorf("exceeds %d bytes", MaxGeneratedBytes)
	}
	return raw, nil
}

// acceptable returns the indexes of generated entries that may join the
// config, reporting each refusal through add. declared reports whether the
// operator's config (as merged so far) already holds an entry with the id.
func acceptable[T any](kind string, items []T, idOf func(T) string, declared func(string) bool, add func(sev, format string, args ...any)) []int {
	count := map[string]int{}
	for _, it := range items {
		count[idOf(it)]++
	}
	reported := map[string]bool{}
	var keep []int
	for i, it := range items {
		id := idOf(it)
		switch {
		case id == "":
			add(SeverityWarning, "generated %s #%d ignored: id is required", kind, i+1)
		case count[id] > 1:
			if !reported[id] {
				reported[id] = true
				add(SeverityWarning, "generated %s %q ignored: the id appears %d times in the generated file, so it is ambiguous", kind, id, count[id])
			}
		case declared(id):
			add(SeverityInfo, "generated %s %q ignored: the operator's config declares one with that id, and operator declarations take precedence", kind, id)
		default:
			keep = append(keep, i)
		}
	}
	return keep
}
