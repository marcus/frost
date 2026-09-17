package catalog

import (
	"strings"
	"testing"

	"github.com/marcus/frost/internal/router"
)

const good = `{
  "schema_version": 1,
  "version": "test-1",
  "generated_at": "2026-09-16T00:00:00Z",
  "models": [
    {"id": "sol", "label": "Sol", "aliases": ["sol-latest"], "output_contracts": ["generated_text", "code_edit"], "input_modalities": ["text"],
     "measurements": [{"source": "example", "metric": "m", "metric_version": "1", "value": 0.5, "higher_is_better": true}]},
    {"id": "jev-1.13.0", "label": "Jev", "output_contracts": ["typed_decision"], "generation_mechanism": "undisclosed"}
  ]
}`

func TestParseGood(t *testing.T) {
	cat, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if cat.Version != "test-1" || len(cat.Models) != 2 {
		t.Fatalf("unexpected catalog %+v", cat)
	}
	if cat.Models["sol"].Measurements[0].Value != 0.5 {
		t.Fatal("measurement lost")
	}
	if id, ok := Resolve(cat, "sol-latest"); !ok || id != "sol" {
		t.Fatalf("alias resolve = %q, %v", id, ok)
	}
	if _, ok := Resolve(cat, "nope"); ok {
		t.Fatal("unknown name resolved")
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"unknown field":   {strings.Replace(good, `"version"`, `"provenance": "x", "version"`, 1), "unknown field"},
		"schema":          {strings.Replace(good, `"schema_version": 1`, `"schema_version": 2`, 1), "schema_version"},
		"no version":      {strings.Replace(good, `"version": "test-1"`, `"version": ""`, 1), "version is required"},
		"dup id":          {strings.Replace(good, `"id": "jev-1.13.0"`, `"id": "sol"`, 1), "collides"},
		"alias collision": {strings.Replace(good, `"id": "jev-1.13.0"`, `"id": "sol-latest"`, 1), "collides"},
		"empty contracts": {strings.Replace(good, `["typed_decision"]`, `[]`, 1), "must not be empty"},
		"bad contract":    {strings.Replace(good, `"typed_decision"`, `"poem"`, 1), "unknown output contract"},
		"bad modality":    {strings.Replace(good, `["text"]`, `["smell"]`, 1), "unknown input modality"},
		"bad measurement": {strings.Replace(good, `"metric_version": "1"`, `"metric_version": ""`, 1), "metric_version"},
		"trailing":        {good + " {}", "trailing"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestLoadExample(t *testing.T) {
	cat, hash, err := Load("../../config/catalog.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 {
		t.Fatalf("hash %q", hash)
	}
	if _, ok := cat.Models["sol"]; !ok {
		t.Fatal("example catalog lacks sol")
	}
	if !contains(cat.Models["jev-1.13.0"].OutputContracts, router.ContractTypedDecision) {
		t.Fatal("jev must be typed_decision")
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
