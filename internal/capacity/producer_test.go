package capacity

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/frost/internal/router"
)

// TestCodexBarConverter runs the example jq converter on a recorded,
// scrubbed CodexBar payload and checks that Frost accepts the result under
// the example pools. It skips when jq is not installed.
func TestCodexBarConverter(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	dir := filepath.Join("..", "..", "examples", "capacity")
	cmd := exec.Command("jq", "--slurpfile", "bindings", filepath.Join(dir, "bindings.json"), "-f", filepath.Join(dir, "codexbar-to-frost.jq"), filepath.Join(dir, "testdata", "codexbar-usage.sample.json"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("jq: %v\n%s", err, out)
	}
	now := time.Now().UTC()
	cap, err := Parse(out, now)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	ps := Validate(cap, fixturePools(), now)
	if HasErrors(ps) {
		t.Fatalf("validate: %+v", ps)
	}
	byID := map[string]router.PoolState{}
	for _, p := range cap.Pools {
		byID[p.ID] = p
	}
	if p := byID["codex-main"]; p.Measurement != router.MeasurementExact || len(p.Windows) != 1 || p.Windows[0].RemainingPercent == nil || *p.Windows[0].RemainingPercent != 56 {
		t.Fatalf("codex-main %+v", p)
	}
	if p := byID["claude-main"]; p.Measurement != router.MeasurementExact || len(p.Windows) != 2 {
		t.Fatalf("claude-main percentOnly must map to exact: %+v", p)
	}
	if p := byID["opencode-go"]; p.Measurement != router.MeasurementEstimated || len(p.Windows) != 3 {
		t.Fatalf("opencode-go %+v", p)
	}
	if p := byID["claude-fable"]; len(p.Windows) != 1 || p.Windows[0].ID != "claude-weekly-scoped-fable" {
		t.Fatalf("claude-fable %+v", p)
	}
	for _, p := range cap.Pools {
		if p.ObservedAt == nil || !p.ObservedAt.Equal(time.Date(2026, 9, 17, 0, 59, 0, 0, time.UTC)) {
			t.Fatalf("observed_at must come from the source: %+v", p)
		}
	}
	_ = os.Getenv
}
