package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/frost/internal/capacity"
	"github.com/marcus/frost/internal/catalog"
	"github.com/marcus/frost/internal/config"
	"github.com/marcus/frost/internal/router"
)

// The shipped examples are the documentation of the producer contracts, so
// they must keep validating against the shipped config.
func TestShippedExamplesStayValid(t *testing.T) {
	generated, err := os.ReadFile(filepath.Join("..", "..", "config", "generated-profiles.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfgPath, dir := workConfig(t, `generated_profiles_file = "profiles.generated.json"`, "", map[string]string{"profiles.generated.json": string(generated)})
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cat, _, err := catalog.Load(filepath.Join(dir, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	problems := cfg.Validate(cat, nil, nil)
	for _, p := range problems {
		if p.Source == config.SourceGenerated {
			t.Errorf("the generated example must validate cleanly, got %s: %s", p.Severity, p.Message)
		}
	}
	if config.HasErrors(problems) {
		t.Fatalf("%+v", problems)
	}
	var local *router.Profile
	for i := range cfg.Profiles {
		if cfg.Profiles[i].ID == "rook-local-code" {
			local = &cfg.Profiles[i]
		}
	}
	if local == nil || local.Gateway == nil || local.Gateway.BaseURL == "" || local.Source != router.SourceGenerated {
		t.Fatalf("example profile not loaded: %+v", local)
	}

	// The local-fleet snapshot example fits the pool the generated example declares.
	at := time.Date(2026, 10, 21, 20, 1, 0, 0, time.UTC)
	snap, _, err := capacity.Load(filepath.Join("..", "..", "examples", "capacity", "local-fleet.example.json"), at)
	if err != nil {
		t.Fatal(err)
	}
	if ps := capacity.Validate(snap, cfg.Pools, at); capacity.HasErrors(ps) {
		t.Fatalf("%+v", ps)
	}
	ev := router.EvaluatePools(&snap, cfg.Pools, cfg.Profiles, cfg.Policy.Capacity, at)
	if pa := ev.Profiles["rook-local-code"]; pa.Availability != router.Available {
		t.Fatalf("%+v", pa)
	}
}

func TestCreditSnapshotScript(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	script := filepath.Join("..", "..", "examples", "capacity", "credit-snapshot.sh")
	out := filepath.Join(t.TempDir(), "credit.json")
	resets := time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339)
	cmd := exec.Command(script, "--granted", "200", "--spent", "50", "--resets-at", resets, "--out", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	now := time.Now().UTC()
	snap, _, err := capacity.Load(out, now)
	if err != nil {
		t.Fatal(err)
	}
	pools := []router.Pool{{ID: "anthropic-credit", Kind: router.PoolKindPrepaid, MarginalCost: router.MarginalIncluded, MappingVerified: true,
		RequiredWindowIDs: []string{"monthly"}, ExpiryPreferenceWindowIDs: []string{"monthly"}, NonRolloverWindowIDs: []string{"monthly"}, ExpiryHorizonHours: 168}}
	if ps := capacity.Validate(snap, pools, now); capacity.HasErrors(ps) || len(ps) > 0 {
		t.Fatalf("%+v", ps)
	}
	w := snap.Pools[0].Windows[0]
	if w.RemainingPercent == nil || *w.RemainingPercent != 75 {
		t.Fatalf("remaining %v", w.RemainingPercent)
	}
	ev := router.EvaluatePools(&snap, pools, []router.Profile{{ID: "p", PoolIDs: []string{"anthropic-credit"}}}, router.DefaultPolicy().Capacity, now)
	if !ev.Profiles["p"].ExpiryEligible {
		t.Fatalf("a credit resetting in 72h with a 168h horizon must earn the preference: %+v", ev.Profiles["p"])
	}
	// Overspend clamps at zero (exhausted), never negative.
	cmd = exec.Command(script, "--granted", "100", "--spent", "130", "--resets-at", resets)
	b, err := cmd.Output()
	if err != nil || !strings.Contains(string(b), `"remaining_percent": 0`) {
		t.Fatalf("%v\n%s", err, b)
	}
	if err := exec.Command(script, "--granted", "0", "--spent", "1", "--resets-at", resets).Run(); err == nil {
		t.Fatal("a zero grant must fail rather than divide by zero")
	}
}
