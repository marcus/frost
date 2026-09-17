package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordedContractsAndReplay(t *testing.T) {
	var catalog Catalog
	if _, err := readJSON("../catalog.json", &catalog); err != nil {
		t.Fatal(err)
	}
	for _, run := range []struct {
		name, questions string
		count           int
	}{{"pilot-v1", "questions-v1.json", 24}, {"pilot-v2", "questions-v2.json", 12}, {"holdout-v2", "questions-v2.json", 6}} {
		t.Run(run.name, func(t *testing.T) {
			var questions map[string]Question
			qb, err := readJSON("../"+run.questions, &questions)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			err = readLines("../results/"+run.name+".jsonl", func(b []byte) error {
				var r Record
				if err := json.Unmarshal(b, &r); err != nil {
					return err
				}
				count++
				if r.QuestionHash != hash(qb) {
					t.Error("question fingerprint mismatch")
				}
				if err := validate(r.Response, questions); err != nil {
					return err
				}
				got := recommend(r.Response, catalog)
				if got.Status != r.Recommendation.Status {
					t.Errorf("%s status mismatch", r.Case.ID)
				}
				if (got.Profile == nil) != (r.Recommendation.Profile == nil) {
					t.Errorf("%s profile presence mismatch", r.Case.ID)
				}
				if got.Profile != nil && r.Recommendation.Profile != nil && got.Profile.ID != r.Recommendation.Profile.ID {
					t.Errorf("%s profile mismatch", r.Case.ID)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if count != run.count {
				t.Errorf("got %d records, want %d", count, run.count)
			}
		})
	}
}

func TestMissingAnswerNeverBecomesZero(t *testing.T) {
	var questions map[string]Question
	if _, err := readJSON("../questions-v2.json", &questions); err != nil {
		t.Fatal(err)
	}
	var record Record
	if err := readLines("../results/pilot-v2.jsonl", func(b []byte) error { return json.Unmarshal(b, &record) }); err != nil {
		t.Fatal(err)
	}
	record.Response.Answers["reasoning"] = Answer{Type: "score"}
	if err := validate(record.Response, questions); err == nil {
		t.Fatal("missing score accepted as zero")
	}
}

func TestNoEligibleProfileHasExplicitOutcome(t *testing.T) {
	var catalog Catalog
	if _, err := readJSON("../catalog.json", &catalog); err != nil {
		t.Fatal(err)
	}
	for i := range catalog.Profiles {
		catalog.Profiles[i].Enabled = false
	}
	var record Record
	if err := readLines("../results/pilot-v2.jsonl", func(b []byte) error { return json.Unmarshal(b, &record) }); err != nil {
		t.Fatal(err)
	}
	got := recommend(record.Response, catalog)
	if got.Status != "no_match" || got.Profile != nil {
		t.Fatalf("unexpected decision: %+v", got)
	}
}

func TestIncompleteDistributionRejected(t *testing.T) {
	var questions map[string]Question
	if _, err := readJSON("../questions-v2.json", &questions); err != nil {
		t.Fatal(err)
	}
	var record Record
	if err := readLines("../results/pilot-v2.jsonl", func(b []byte) error { return json.Unmarshal(b, &record) }); err != nil {
		t.Fatal(err)
	}
	a := record.Response.Answers["reasoning"]
	delete(a.Probabilities, "4")
	record.Response.Answers["reasoning"] = a
	if err := validate(record.Response, questions); err == nil || !strings.Contains(err.Error(), "distribution") {
		t.Fatalf("incomplete distribution not rejected: %v", err)
	}
}
