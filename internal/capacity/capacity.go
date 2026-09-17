// Package capacity parses and validates the neutral usage snapshot. It knows
// nothing about CodexBar or any producer; it turns a file into
// router.Capacity plus a hash for provenance. Freshness and availability are
// judged in the router so a snapshot can be re-evaluated at another time.
package capacity

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
	"strings"
	"time"

	"github.com/marcus/frost/internal/router"
)

// SchemaVersion is the snapshot schema this package reads.
const SchemaVersion = 1

// MaxBytes caps a snapshot file.
const MaxBytes = 1 << 20

// ClockSkew is how far in the future an observation may sit before it is
// rejected as implausible.
const ClockSkew = 5 * time.Minute

// Problem is one validation finding against the declared topology.
type Problem struct {
	Severity string `json:"severity"` // error | warning
	Message  string `json:"message"`
}

// Severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// HasErrors reports whether any problem is an error.
func HasErrors(ps []Problem) bool {
	for _, p := range ps {
		if p.Severity == SeverityError {
			return true
		}
	}
	return false
}

type fileSnapshot struct {
	SchemaVersion int        `json:"schema_version"`
	GeneratedAt   string     `json:"generated_at"`
	Producer      string     `json:"producer"`
	Pools         []filePool `json:"pools"`
}

type filePool struct {
	ID           string       `json:"id"`
	SourceStatus string       `json:"source_status"`
	Measurement  string       `json:"measurement"`
	ObservedAt   *string      `json:"observed_at"`
	ValidUntil   *string      `json:"valid_until"`
	Windows      []fileWindow `json:"windows"`
}

type fileWindow struct {
	ID               string   `json:"id"`
	RemainingPercent *float64 `json:"remaining_percent"`
	ResetsAt         *string  `json:"resets_at"`
	DurationSeconds  *int     `json:"duration_seconds"`
}

// Load reads a snapshot file. It returns the parsed snapshot and the sha256
// hex of the raw bytes.
func Load(path string, now time.Time) (router.Capacity, string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return router.Capacity{}, "", err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return router.Capacity{}, "", err
	}
	if len(raw) > MaxBytes {
		return router.Capacity{}, "", fmt.Errorf("snapshot %s exceeds %d bytes", path, MaxBytes)
	}
	cap, err := Parse(raw, now)
	if err != nil {
		return router.Capacity{}, "", fmt.Errorf("snapshot %s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	return cap, hex.EncodeToString(sum[:]), nil
}

// Parse decodes and structurally validates snapshot bytes. Topology checks
// against the operator's pools happen in Validate.
func Parse(raw []byte, now time.Time) (router.Capacity, error) {
	var f fileSnapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return router.Capacity{}, err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return router.Capacity{}, errors.New("trailing data after the snapshot object")
	}
	if f.SchemaVersion != SchemaVersion {
		return router.Capacity{}, fmt.Errorf("unsupported schema_version %d (want %d)", f.SchemaVersion, SchemaVersion)
	}
	generated, err := parseTime("generated_at", f.GeneratedAt)
	if err != nil {
		return router.Capacity{}, err
	}
	cap := router.Capacity{GeneratedAt: generated, Producer: f.Producer, Pools: []router.PoolState{}}
	seen := map[string]bool{}
	for i, p := range f.Pools {
		where := fmt.Sprintf("pools[%d]", i)
		if p.ID == "" {
			return router.Capacity{}, errors.New(where + ": id is required")
		}
		where += " (" + p.ID + ")"
		if seen[p.ID] {
			return router.Capacity{}, errors.New(where + ": duplicate pool id")
		}
		seen[p.ID] = true
		if !slices.Contains([]string{router.SourceOK, router.SourceUnavailable, router.SourceError, router.SourceAmbiguousAccount}, p.SourceStatus) {
			return router.Capacity{}, fmt.Errorf("%s: source_status %q must be ok, unavailable, error, or ambiguous_account", where, p.SourceStatus)
		}
		if !slices.Contains([]string{router.MeasurementExact, router.MeasurementEstimated, router.MeasurementUnknown}, p.Measurement) {
			return router.Capacity{}, fmt.Errorf("%s: measurement %q must be exact, estimated, or unknown", where, p.Measurement)
		}
		ps := router.PoolState{ID: p.ID, SourceStatus: p.SourceStatus, Measurement: p.Measurement, Windows: []router.WindowState{}}
		if p.ObservedAt != nil {
			t, err := parseTime(where+".observed_at", *p.ObservedAt)
			if err != nil {
				return router.Capacity{}, err
			}
			if t.After(now.Add(ClockSkew)) {
				return router.Capacity{}, fmt.Errorf("%s: observed_at %s is in the future", where, t.UTC().Format(time.RFC3339))
			}
			ps.ObservedAt = &t
		}
		if p.ValidUntil != nil {
			t, err := parseTime(where+".valid_until", *p.ValidUntil)
			if err != nil {
				return router.Capacity{}, err
			}
			ps.ValidUntil = &t
		}
		wseen := map[string]bool{}
		for j, w := range p.Windows {
			wwhere := fmt.Sprintf("%s.windows[%d]", where, j)
			if w.ID == "" {
				return router.Capacity{}, errors.New(wwhere + ": id is required")
			}
			if wseen[w.ID] {
				return router.Capacity{}, fmt.Errorf("%s: duplicate window id %q", wwhere, w.ID)
			}
			wseen[w.ID] = true
			ws := router.WindowState{ID: w.ID}
			if w.RemainingPercent != nil {
				v := *w.RemainingPercent
				if v < 0 || v > 100 || v != v {
					return router.Capacity{}, fmt.Errorf("%s: remaining_percent %v is outside 0-100", wwhere, v)
				}
				ws.RemainingPercent = &v
			}
			if w.ResetsAt != nil {
				t, err := parseTime(wwhere+".resets_at", *w.ResetsAt)
				if err != nil {
					return router.Capacity{}, err
				}
				ws.ResetsAt = &t
			}
			if w.DurationSeconds != nil {
				if *w.DurationSeconds < 0 {
					return router.Capacity{}, fmt.Errorf("%s: duration_seconds must not be negative", wwhere)
				}
				ws.DurationSeconds = *w.DurationSeconds
			}
			ps.Windows = append(ps.Windows, ws)
		}
		cap.Pools = append(cap.Pools, ps)
	}
	return cap, nil
}

// Validate checks a parsed snapshot against the declared pools. Errors are
// observations the topology does not admit; warnings are gaps.
func Validate(cap router.Capacity, pools []router.Pool, now time.Time) []Problem {
	var ps []Problem
	errf := func(format string, args ...any) {
		ps = append(ps, Problem{SeverityError, fmt.Sprintf(format, args...)})
	}
	warnf := func(format string, args ...any) {
		ps = append(ps, Problem{SeverityWarning, fmt.Sprintf(format, args...)})
	}
	declared := map[string]router.Pool{}
	for _, p := range pools {
		declared[p.ID] = p
	}
	present := map[string]bool{}
	for _, s := range cap.Pools {
		pool, ok := declared[s.ID]
		if !ok {
			errf("pool %q is not declared in the operator config; a snapshot cannot add pools", s.ID)
			continue
		}
		present[s.ID] = true
		allowed := append(append([]string{}, pool.RequiredWindowIDs...), pool.ExpiryPreferenceWindowIDs...)
		have := map[string]router.WindowState{}
		for _, w := range s.Windows {
			have[w.ID] = w
			if !slices.Contains(allowed, w.ID) {
				errf("pool %s: window %q is not declared for that pool; a snapshot cannot add windows", s.ID, w.ID)
				continue
			}
			if w.ResetsAt != nil && w.DurationSeconds > 0 {
				base := now
				if s.ObservedAt != nil {
					base = *s.ObservedAt
				}
				if w.ResetsAt.After(base.Add(time.Duration(w.DurationSeconds)*time.Second + ClockSkew)) {
					warnf("pool %s: window %s resets at %s, more than one window duration after the observation", s.ID, w.ID, w.ResetsAt.UTC().Format(time.RFC3339))
				}
			}
		}
		if s.SourceStatus == router.SourceOK {
			for _, id := range pool.RequiredWindowIDs {
				if _, ok := have[id]; !ok {
					warnf("pool %s: required window %q is absent; the pool is unknown", s.ID, id)
				}
			}
		}
	}
	for _, p := range pools {
		if !present[p.ID] {
			warnf("configured pool %s is absent from the snapshot; it is unknown", p.ID)
		}
	}
	return ps
}

func parseTime(field, v string) (time.Time, error) {
	if strings.TrimSpace(v) == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", field, err)
	}
	return t, nil
}
