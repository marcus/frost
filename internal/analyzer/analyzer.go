// Package analyzer defines the task-analysis seam and the question
// specification the router's policy depends on. Concrete providers live in
// subpackages; the router never sees provider types.
package analyzer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"

	"github.com/marcus/frost/internal/router"
)

// Analyzer turns a task into a vendor-neutral assessment.
type Analyzer interface {
	Analyze(ctx context.Context, task router.Task) (router.Assessment, error)
}

// Question is one typed question. Instructions and criteria may be a string,
// object, or array, so they stay raw.
type Question struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

// Spec is a versioned question specification. Hash covers the raw bytes so a
// recorded assessment can be matched to the exact wording that produced it.
type Spec struct {
	SchemaVersion int                 `json:"schema_version"`
	Version       string              `json:"version"`
	Questions     map[string]Question `json:"questions"`
	Hash          string              `json:"-"`
	Raw           []byte              `json:"-"`
}

// LoadSpec reads and parses a specification file.
func LoadSpec(path string) (*Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseSpec(b)
}

// ParseSpec parses specification bytes. It rejects unknown fields and
// trailing data.
func ParseSpec(b []byte) (*Spec, error) {
	var s Spec
	d := json.NewDecoder(bytesReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return nil, fmt.Errorf("question spec: %w", err)
	}
	if d.More() {
		return nil, errors.New("question spec: trailing data")
	}
	if s.SchemaVersion != 1 {
		return nil, fmt.Errorf("question spec: unsupported schema_version %d", s.SchemaVersion)
	}
	if s.Version == "" {
		return nil, errors.New("question spec: version is required")
	}
	if len(s.Questions) == 0 {
		return nil, errors.New("question spec: no questions")
	}
	for id, q := range s.Questions {
		switch q.Type {
		case "score", "choice", "noul":
		default:
			return nil, fmt.Errorf("question spec: %s has unknown type %q", id, q.Type)
		}
		if len(q.Instructions) == 0 {
			return nil, fmt.Errorf("question spec: %s has no instructions", id)
		}
		if q.Type != "noul" && len(q.Criteria) == 0 {
			return nil, fmt.Errorf("question spec: %s has no criteria", id)
		}
	}
	sum := sha256.Sum256(b)
	s.Hash = hex.EncodeToString(sum[:])
	s.Raw = b
	return &s, nil
}

// ScoreLevels returns the ordered level descriptions of a score question as
// strings for legend matching. Structured levels are kept as compact JSON.
func (s *Spec) ScoreLevels(id string) ([]string, error) {
	q, ok := s.Questions[id]
	if !ok || q.Type != "score" {
		return nil, fmt.Errorf("question %s is not a score", id)
	}
	var levels []json.RawMessage
	if err := json.Unmarshal(q.Criteria, &levels); err != nil {
		return nil, fmt.Errorf("question %s: score criteria must be an array: %w", id, err)
	}
	out := make([]string, len(levels))
	for i, l := range levels {
		var str string
		if err := json.Unmarshal(l, &str); err == nil {
			out[i] = str
		} else {
			out[i] = string(l)
		}
	}
	return out, nil
}

// ChoiceOptions returns the sorted option names of a choice question, or nil
// if the question is absent or not a choice.
func (s *Spec) ChoiceOptions(id string) []string {
	q, ok := s.Questions[id]
	if !ok || q.Type != "choice" {
		return nil
	}
	var opts map[string]json.RawMessage
	if err := json.Unmarshal(q.Criteria, &opts); err != nil {
		return nil
	}
	out := make([]string, 0, len(opts))
	for k := range opts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Validate checks that the spec supplies every question the router policy
// reads, with the expected types, level counts, and options.
func (s *Spec) Validate() error {
	var errs []error
	requireScore := func(id string, levels int) {
		q, ok := s.Questions[id]
		if !ok {
			errs = append(errs, fmt.Errorf("missing score question %s", id))
			return
		}
		if q.Type != "score" {
			errs = append(errs, fmt.Errorf("question %s must be a score, got %s", id, q.Type))
			return
		}
		got, err := s.ScoreLevels(id)
		if err != nil {
			errs = append(errs, err)
			return
		}
		if len(got) != levels {
			errs = append(errs, fmt.Errorf("question %s must have %d levels, got %d", id, levels, len(got)))
		}
	}
	requireNoul := func(id string) {
		q, ok := s.Questions[id]
		if !ok {
			errs = append(errs, fmt.Errorf("missing noul question %s", id))
			return
		}
		if q.Type != "noul" {
			errs = append(errs, fmt.Errorf("question %s must be a noul, got %s", id, q.Type))
		}
	}
	requireChoice := func(id string, mustHave []string, exact bool) {
		q, ok := s.Questions[id]
		if !ok {
			errs = append(errs, fmt.Errorf("missing choice question %s", id))
			return
		}
		if q.Type != "choice" {
			errs = append(errs, fmt.Errorf("question %s must be a choice, got %s", id, q.Type))
			return
		}
		opts := s.ChoiceOptions(id)
		if opts == nil {
			errs = append(errs, fmt.Errorf("question %s: choice criteria must be an object", id))
			return
		}
		if len(opts) < 2 {
			errs = append(errs, fmt.Errorf("question %s needs at least two options", id))
		}
		for _, m := range mustHave {
			if !slices.Contains(opts, m) {
				errs = append(errs, fmt.Errorf("question %s must offer option %q", id, m))
			}
		}
		if exact {
			want := slices.Clone(mustHave)
			sort.Strings(want)
			if !slices.Equal(opts, want) {
				errs = append(errs, fmt.Errorf("question %s options must be exactly %v, got %v", id, want, opts))
			}
		}
	}

	requireScore(router.QReasoning, router.ReasoningLevels)
	requireScore(router.QWorkload, 4)
	requireScore(router.QConsequence, 4)
	requireNoul(router.QMissingContext)
	requireChoice(router.QVerification, nil, false)
	requireChoice(router.QTaskFamily, []string{router.FamilyOther}, false)
	requireChoice(router.QOutputContract, []string{
		router.ContractGeneratedText, router.ContractCodeEdit, router.ContractStructuredObj,
		router.ContractTypedDecision, router.ContractOther,
	}, true)
	requireChoice(router.QResponsiveness, []string{
		router.ResponsivenessLive, router.ResponsivenessAttended, router.ResponsivenessUnattend, router.ResponsivenessUnstated,
	}, false)
	requireNoul(router.QNeedsImageInput)
	requireNoul(router.QNeedsWebRes)
	requireNoul(router.QNeedsRepoTools)
	return errors.Join(errs...)
}

// ProviderError is a non-success response from an analysis provider. Detail
// carries the response body only for 422 validation failures, which describe
// the question specification rather than the task.
type ProviderError struct {
	Status    int
	Retryable bool
	Detail    string
}

func (e *ProviderError) Error() string {
	msg := fmt.Sprintf("provider HTTP %d", e.Status)
	if e.Retryable {
		msg += " (retryable)"
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}
