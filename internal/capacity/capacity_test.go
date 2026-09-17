package capacity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/frost/internal/router"
)

var now = time.Date(2026, 9, 17, 1, 0, 0, 0, time.UTC)

func fixturePools() []router.Pool {
	return []router.Pool{
		{ID: "codex-main", Kind: "subscription", MarginalCost: "included", MappingVerified: true, RequiredWindowIDs: []string{"secondary"}, ExpiryPreferenceWindowIDs: []string{"secondary"}},
		{ID: "claude-main", Kind: "subscription", MarginalCost: "included", MappingVerified: true, RequiredWindowIDs: []string{"primary", "secondary"}, ExpiryPreferenceWindowIDs: []string{"secondary"}},
		{ID: "opencode-go", Kind: "subscription", MarginalCost: "included", MappingVerified: true, RequiredWindowIDs: []string{"primary", "secondary", "tertiary"}, ExpiryPreferenceWindowIDs: []string{"secondary", "tertiary"}},
		{ID: "claude-fable", Kind: "subscription", MarginalCost: "included", MappingVerified: true, RequiredWindowIDs: []string{"claude-weekly-scoped-fable"}, ExpiryPreferenceWindowIDs: []string{"claude-weekly-scoped-fable"}},
		{ID: "codex-spark", Kind: "subscription", MarginalCost: "included", RequiredWindowIDs: []string{"codex-spark", "codex-spark-weekly"}, ExpiryPreferenceWindowIDs: []string{"codex-spark-weekly"}},
	}
}

func TestFixtureParsesAndValidates(t *testing.T) {
	cap, hash, err := Load("testdata/codexbar-shape.json", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 || len(cap.Pools) != 5 {
		t.Fatalf("hash %s pools %d", hash, len(cap.Pools))
	}
	ps := Validate(cap, fixturePools(), now)
	if HasErrors(ps) {
		t.Fatalf("unexpected errors: %+v", ps)
	}
}

func TestParseRejections(t *testing.T) {
	base := `{"schema_version":1,"generated_at":"2026-09-17T00:59:00Z","producer":"t","pools":[{"id":"codex-main","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:58:00Z","valid_until":null,"windows":[{"id":"secondary","remaining_percent":56,"resets_at":"2026-09-19T13:29:45Z","duration_seconds":604800}]}]}`
	cases := map[string]string{
		"schema":            strings.Replace(base, `"schema_version":1`, `"schema_version":2`, 1),
		"unknown field":     strings.Replace(base, `"producer":"t"`, `"producer":"t","extra":1`, 1),
		"trailing":          base + " {}",
		"bad status":        strings.Replace(base, `"source_status":"ok"`, `"source_status":"fine"`, 1),
		"bad measurement":   strings.Replace(base, `"measurement":"exact"`, `"measurement":"guess"`, 1),
		"future observed":   strings.Replace(base, `"observed_at":"2026-09-17T00:58:00Z"`, `"observed_at":"2026-09-17T02:00:00Z"`, 1),
		"percent range":     strings.Replace(base, `"remaining_percent":56`, `"remaining_percent":101`, 1),
		"negative percent":  strings.Replace(base, `"remaining_percent":56`, `"remaining_percent":-1`, 1),
		"bad time":          strings.Replace(base, `"resets_at":"2026-09-19T13:29:45Z"`, `"resets_at":"soon"`, 1),
		"duplicate pool":    strings.Replace(base, `"pools":[`, `"pools":[{"id":"codex-main","source_status":"ok","measurement":"exact","observed_at":null,"valid_until":null,"windows":[]},`, 1),
		"duplicate window":  strings.Replace(base, `"windows":[`, `"windows":[{"id":"secondary","remaining_percent":1,"resets_at":null,"duration_seconds":null},`, 1),
		"missing generated": strings.Replace(base, `"generated_at":"2026-09-17T00:59:00Z"`, `"generated_at":""`, 1),
	}
	for name, body := range cases {
		if _, err := Parse([]byte(body), now); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// Within clock skew is accepted; null values are accepted.
	ok := strings.Replace(base, `"observed_at":"2026-09-17T00:58:00Z"`, `"observed_at":"2026-09-17T01:04:00Z"`, 1)
	ok = strings.Replace(ok, `"remaining_percent":56`, `"remaining_percent":null`, 1)
	cap, err := Parse([]byte(ok), now)
	if err != nil {
		t.Fatal(err)
	}
	if cap.Pools[0].Windows[0].RemainingPercent != nil {
		t.Fatalf("null must stay unknown")
	}
}

func TestValidateTopology(t *testing.T) {
	cap, err := Parse([]byte(`{"schema_version":1,"generated_at":"2026-09-17T00:59:00Z","pools":[
	  {"id":"codex-spark-unknown","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:58:00Z","valid_until":null,"windows":[]},
	  {"id":"codex-main","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:58:00Z","valid_until":null,"windows":[
	     {"id":"tertiary","remaining_percent":10,"resets_at":null,"duration_seconds":null},
	     {"id":"secondary","remaining_percent":10,"resets_at":"2026-09-30T00:00:00Z","duration_seconds":604800}]},
	  {"id":"claude-main","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:58:00Z","valid_until":null,"windows":[
	     {"id":"primary","remaining_percent":10,"resets_at":"2026-09-17T05:00:00Z","duration_seconds":18000}]}
	]}`), now)
	if err != nil {
		t.Fatal(err)
	}
	ps := Validate(cap, fixturePools(), now)
	want := map[string]string{
		"not declared in the operator config":   SeverityError,
		"cannot add windows":                    SeverityError,
		"more than one window duration":         SeverityWarning,
		`required window "secondary" is absent`: SeverityWarning,
		"configured pool opencode-go is absent": SeverityWarning,
	}
	for sub, sev := range want {
		found := false
		for _, p := range ps {
			if strings.Contains(p.Message, sub) && p.Severity == sev {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %s %q in %+v", sev, sub, ps)
		}
	}
}

func TestLoadMissingAndOversized(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "nope.json"), now); err == nil {
		t.Fatal("expected error")
	}
	big := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(big, make([]byte, MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(big, now); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size error, got %v", err)
	}
}
