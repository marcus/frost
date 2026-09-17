package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/frost/internal/router"
)

func TestProductionSpecValidates(t *testing.T) {
	spec, err := LoadSpec(filepath.Join("..", "..", "config", "questions-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.Version != "frost-questions-v3" || len(spec.Hash) != 64 {
		t.Errorf("version=%q hash=%q", spec.Version, spec.Hash)
	}
	opts := spec.ChoiceOptions(router.QTaskFamily)
	for _, want := range []string{"software_change", "writing", "structured_decision", "other"} {
		found := false
		for _, o := range opts {
			if o == want {
				found = true
			}
		}
		if !found {
			t.Errorf("task_family lacks %s: %v", want, opts)
		}
	}
	if spec.ChoiceOptions(router.QReasoning) != nil {
		t.Error("ChoiceOptions on a score must be nil")
	}
	levels, err := spec.ScoreLevels(router.QReasoning)
	if err != nil || len(levels) != router.ReasoningLevels {
		t.Errorf("reasoning levels: %v %v", levels, err)
	}
}

func TestParseSpecRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"unknown field":  `{"schema_version":1,"version":"v","questions":{"a":{"type":"noul","instructions":"x"}},"extra":1}`,
		"bad schema":     `{"schema_version":2,"version":"v","questions":{"a":{"type":"noul","instructions":"x"}}}`,
		"no version":     `{"schema_version":1,"questions":{"a":{"type":"noul","instructions":"x"}}}`,
		"no questions":   `{"schema_version":1,"version":"v","questions":{}}`,
		"unknown type":   `{"schema_version":1,"version":"v","questions":{"a":{"type":"rank","instructions":"x"}}}`,
		"score no crit":  `{"schema_version":1,"version":"v","questions":{"a":{"type":"score","instructions":"x"}}}`,
		"trailing bytes": `{"schema_version":1,"version":"v","questions":{"a":{"type":"noul","instructions":"x"}}} {}`,
	}
	for name, in := range cases {
		if _, err := ParseSpec([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestValidateReportsEveryGap(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "questions-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Break several requirements at once and check each is named.
	s := string(b)
	s = strings.Replace(s, `"needs_repository_tools": {`, `"needs_repo": {`, 1)
	s = strings.Replace(s, `"typed_decision": {`, `"decision": {`, 1)
	spec, err := ParseSpec([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	err = spec.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{"needs_repository_tools", "output_contract"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestProviderErrorMessage(t *testing.T) {
	e := &ProviderError{Status: 422, Detail: `{"detail":"questions.x.criteria missing"}`}
	if !strings.Contains(e.Error(), "422") || !strings.Contains(e.Error(), "criteria missing") {
		t.Error(e.Error())
	}
	r := &ProviderError{Status: 529, Retryable: true}
	if !strings.Contains(r.Error(), "retryable") || strings.Contains(r.Error(), ":") {
		t.Error(r.Error())
	}
}
