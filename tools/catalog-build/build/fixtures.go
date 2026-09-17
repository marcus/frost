package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/marcus/frost/tools/catalog-build/source"
)

// fixtureMeta is the sidecar written next to recorded payloads.
type fixtureMeta struct {
	Source string           `json:"source"`
	Files  []fixtureMetaRow `json:"files"`
}

type fixtureMetaRow struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
	SHA256    string    `json:"sha256"`
}

// RecordPayloads writes payloads verbatim under dir/<source>/ with a
// meta.json sidecar.
func RecordPayloads(dir string, payloads []source.Payload) error {
	if len(payloads) == 0 {
		return errors.New("no payloads to record")
	}
	sdir := filepath.Join(dir, payloads[0].Source)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return err
	}
	meta := fixtureMeta{Source: payloads[0].Source}
	for _, p := range payloads {
		if err := os.WriteFile(filepath.Join(sdir, p.Name), p.Body, 0o644); err != nil {
			return err
		}
		meta.Files = append(meta.Files, fixtureMetaRow{Name: p.Name, URL: p.URL, FetchedAt: p.FetchedAt, SHA256: p.SHA256})
	}
	return WriteJSONAtomic(filepath.Join(sdir, "meta.json"), meta)
}

// LoadPayloads reads a recorded source directory. A missing directory
// returns os.ErrNotExist so the caller can treat the source as failed.
func LoadPayloads(dir, sourceName string) ([]source.Payload, error) {
	sdir := filepath.Join(dir, sourceName)
	raw, err := os.ReadFile(filepath.Join(sdir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta fixtureMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("%s meta.json: %w", sourceName, err)
	}
	var out []source.Payload
	for _, row := range meta.Files {
		body, err := os.ReadFile(filepath.Join(sdir, row.Name))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		actual := hex.EncodeToString(sum[:])
		if actual != row.SHA256 {
			return nil, fmt.Errorf("%s/%s: sha256 mismatch: meta has %q, payload is %q", sourceName, row.Name, row.SHA256, actual)
		}
		out = append(out, source.Payload{Source: sourceName, Name: row.Name, URL: row.URL, FetchedAt: row.FetchedAt, SHA256: actual, Body: body})
	}
	return out, nil
}
