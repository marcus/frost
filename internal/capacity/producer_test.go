package capacity

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
}

func TestCapacityRefreshCollection(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	dir, err := filepath.Abs(filepath.Join("..", "..", "examples", "capacity"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "testdata", "codexbar-usage.sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	var codex []map[string]any
	for _, row := range rows {
		if row["provider"] == "codex" {
			codex = append(codex, row)
		}
	}
	valid, err := json.Marshal(codex)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, payload string
		wantSuccess   bool
	}{
		{"valid", string(valid), true},
		{"malformed", "not json with private account data", false},
		{"wrong shape", `{}`, false},
		{"empty", `[]`, false},
		{"wrong source", `[{"provider":"codex","source":"web","usage":{}}]`, false},
		{"provider error", `[{"provider":"codex","error":"private account error"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			write := func(name string, data []byte, mode os.FileMode) string {
				t.Helper()
				path := filepath.Join(work, name)
				if err := os.WriteFile(path, data, mode); err != nil {
					t.Fatal(err)
				}
				return path
			}
			write("codexbar", []byte("#!/bin/bash\nprintf '%s\\n' \"$*\" >> \"$FROST_TEST_ARGS\"\ncat \"$FROST_TEST_PAYLOAD\"\n"), 0o755)
			// Do not invoke the operator's installed frost after publication.
			write("frost", []byte("#!/bin/bash\nexit 0\n"), 0o755)
			payload := write("payload.json", []byte(tc.payload), 0o600)
			previous := []byte("last-good-snapshot\n")
			out := write("capacity.json", previous, 0o600)
			argsPath := filepath.Join(work, "args")
			cmd := exec.Command("bash", filepath.Join(dir, "refresh.sh"), "--bindings", filepath.Join(dir, "bindings.json"), "--providers", "codex", "--out", out)
			cmd.Env = append(os.Environ(), "PATH="+work+string(os.PathListSeparator)+os.Getenv("PATH"), "FROST_TEST_ARGS="+argsPath, "FROST_TEST_PAYLOAD="+payload)
			output, runErr := cmd.CombinedOutput()
			if (runErr == nil) != tc.wantSuccess {
				t.Fatalf("success=%v, wanted %v: %v\n%s", runErr == nil, tc.wantSuccess, runErr, output)
			}
			argv, err := os.ReadFile(argsPath)
			if err != nil || string(argv) != "usage --json --provider codex --source oauth\n" {
				t.Fatalf("expected one deduplicated oauth request: %q, %v", argv, err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.wantSuccess {
				if !bytes.Equal(got, previous) {
					t.Fatalf("last-good snapshot changed: %s", got)
				}
				if strings.Contains(string(output), "private account") {
					t.Fatalf("raw provider data leaked: %s", output)
				}
				return
			}
			cap, err := Parse(got, time.Now().UTC())
			if err != nil || cap.Pools[0].SourceStatus != "ok" {
				t.Fatalf("published snapshot: %+v, %v", cap, err)
			}
		})
	}
}
