package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/legacy/golden.json")

// TestLegacyDecisionsGolden pins the routing core's behavior for the
// pre-gateway configuration shape. The fixtures in testdata/legacy are a
// frozen copy of the September 2026 example plus a snapshot with an
// expiry-eligible pool and an exhausted one. Every case routes through the
// full CLI with a pinned clock and stub analyzer, so any change to selection,
// capacity handling, or the JSON contract for such a config shows up as a
// byte difference here.
func TestLegacyDecisionsGolden(t *testing.T) {
	dir := filepath.Join("testdata", "legacy")
	cfg := filepath.Join(dir, "config.toml")
	snapshot := filepath.Join(dir, "capacity.json")

	type routeCase struct {
		name  string
		level int
		fam   string
		con   string
		args  []string
	}
	var cases []routeCase
	for level := 0; level <= 4; level++ {
		for _, policy := range []string{"adequate", "quality", "fast", "relaxed"} {
			for _, cap := range []struct {
				name string
				args []string
			}{
				{"nocap", nil},
				{"cap", []string{"--capacity", snapshot}},
				{"cap-demote", []string{"--capacity", snapshot, "--availability", "demote"}},
				{"cap-ignore", []string{"--capacity", snapshot, "--availability", "ignore"}},
			} {
				args := append([]string{"--policy", policy}, cap.args...)
				cases = append(cases, routeCase{
					name:  fmt.Sprintf("software-l%d-%s-%s", level, policy, cap.name),
					level: level, fam: "software_change", con: "code_edit", args: args,
				})
			}
		}
	}
	cases = append(cases,
		routeCase{name: "writing-l2-cap", level: 2, fam: "writing", con: "generated_text", args: []string{"--capacity", snapshot}},
		routeCase{name: "research-l3-allow", level: 3, fam: "research_synthesis", con: "generated_text", args: []string{"--allow", "sol,astra,opus-5", "--capacity", snapshot}},
		routeCase{name: "effort-high-exclude", level: 2, fam: "software_change", con: "code_edit", args: []string{"--effort", "high", "--exclude", "fable", "--capacity", snapshot}},
		routeCase{name: "latency-require", level: 1, fam: "software_change", con: "code_edit", args: []string{"--latency-ms", "5000", "--latency-mode", "require"}},
	)

	got := map[string]json.RawMessage{}
	for _, c := range cases {
		h := newHarness()
		h.stub.level, h.stub.family, h.stub.contract = c.level, c.fam, c.con
		args := append([]string{"route", "--config", cfg, "--json"}, c.args...)
		args = append(args, "Golden task: "+c.name)
		code := h.run(args...)
		entry, err := json.Marshal(map[string]any{"exit": code, "decision": withoutQuestions(t, h.stdout.Bytes())})
		if err != nil {
			t.Fatal(err)
		}
		got[c.name] = entry
	}
	// Human rendering and profile listing are part of the contract too.
	h := newHarness()
	h.run("route", "--config", cfg, "--capacity", snapshot, "Golden human output")
	got["human"], _ = json.Marshal(h.stdout.String())
	h.run("profiles", "list", "--config", cfg, "--json")
	got["profiles"] = json.RawMessage(bytes.Clone(h.stdout.Bytes()))

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", " ")
	if err := enc.Encode(got); err != nil {
		t.Fatal(err)
	}
	goldenPath := filepath.Join(dir, "golden.json")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if !bytes.Equal(want, buf.Bytes()) {
		var wantMap map[string]json.RawMessage
		_ = json.Unmarshal(want, &wantMap)
		for name, g := range got {
			var a, b bytes.Buffer
			_ = json.Compact(&a, wantMap[name])
			_ = json.Compact(&b, g)
			if !bytes.Equal(a.Bytes(), b.Bytes()) {
				t.Errorf("decision %q changed for a legacy config", name)
			}
		}
		t.Fatalf("legacy golden mismatch; if intentional, rerun with -update")
	}
}

// withoutQuestions drops the echoed analyzer answers: they are the stub's
// input, large, and not part of what the router decided.
func withoutQuestions(t *testing.T, raw []byte) json.RawMessage {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if a, ok := d["analysis"].(map[string]any); ok {
		delete(a, "questions")
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
