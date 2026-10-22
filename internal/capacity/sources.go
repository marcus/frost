package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/marcus/frost/internal/router"
)

// Source is one producer's snapshot file, loaded and validated on its own. A
// producer that is missing, unparseable, or inconsistent with the declared
// topology contributes nothing; the pools it would have reported are simply
// absent from the merged snapshot and therefore unknown. Unknown never means
// exhausted, so one failed producer cannot exclude a profile that another
// producer's pools describe.
type Source struct {
	Path     string
	Hash     string // sha256 hex of the raw bytes; empty when unreadable
	Snapshot router.Capacity
	Missing  bool      // the file does not exist
	Err      error     // read, parse, or topology failure; the source is unusable
	Problems []Problem // topology findings for this file alone, errors and warnings
}

// Usable reports whether the source's observations may be merged.
func (s Source) Usable() bool { return s.Err == nil }

// LoadSources loads every path independently against the declared pools. It
// never returns early: each failure is recorded on its own Source.
func LoadSources(paths []string, pools []router.Pool, now time.Time) []Source {
	out := make([]Source, 0, len(paths))
	for _, path := range paths {
		s := Source{Path: path}
		cap, hash, err := Load(path, now)
		switch {
		case err != nil:
			s.Missing = errors.Is(err, os.ErrNotExist)
			s.Err = err
		default:
			s.Hash, s.Snapshot = hash, cap
			s.Problems = ValidateTopology(cap, pools, now)
			if HasErrors(s.Problems) {
				var msgs []string
				for _, p := range s.Problems {
					if p.Severity == SeverityError {
						msgs = append(msgs, p.Message)
					}
				}
				s.Err = fmt.Errorf("snapshot %s: %s", path, strings.Join(msgs, "; "))
			}
		}
		out = append(out, s)
	}
	return out
}

// Merge combines the usable sources into one snapshot. A pool reported by more
// than one usable source belongs to the earlier source in path order; later
// duplicates are dropped and named in the returned problems, so precedence is
// the operator's list order. The returned hash is the single source's hash
// when exactly one source contributed, and otherwise a digest of the
// contributing hashes in order. It is empty when nothing contributed.
func Merge(sources []Source) (router.Capacity, string, []Problem) {
	merged := router.Capacity{Pools: []router.PoolState{}}
	var problems []Problem
	var hashes, producers []string
	owner := map[string]string{}
	for _, s := range sources {
		if !s.Usable() {
			continue
		}
		hashes = append(hashes, s.Hash)
		if s.Snapshot.Producer != "" {
			producers = append(producers, s.Snapshot.Producer)
		}
		if s.Snapshot.GeneratedAt.After(merged.GeneratedAt) {
			merged.GeneratedAt = s.Snapshot.GeneratedAt
		}
		for _, ps := range s.Snapshot.Pools {
			if first, dup := owner[ps.ID]; dup {
				problems = append(problems, Problem{Severity: SeverityWarning, Message: fmt.Sprintf("pool %s is reported by %s and %s; the earlier producer in the list wins and %s is ignored for it", ps.ID, first, s.Path, s.Path)})
				continue
			}
			owner[ps.ID] = s.Path
			merged.Pools = append(merged.Pools, ps)
		}
	}
	merged.Producer = strings.Join(producers, "+")
	switch len(hashes) {
	case 0:
		return merged, "", problems
	case 1:
		return merged, hashes[0], problems
	}
	sum := sha256.Sum256([]byte(strings.Join(hashes, "\n")))
	return merged, hex.EncodeToString(sum[:]), problems
}

// Absent warns about configured pools that no usable source reported.
func Absent(cap router.Capacity, pools []router.Pool) []Problem {
	present := map[string]bool{}
	for _, s := range cap.Pools {
		present[s.ID] = true
	}
	var ps []Problem
	for _, p := range pools {
		if !present[p.ID] {
			ps = append(ps, Problem{Severity: SeverityWarning, Message: fmt.Sprintf("configured pool %s is absent from the snapshot; it is unknown", p.ID)})
		}
	}
	return ps
}
