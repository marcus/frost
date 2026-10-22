package capacity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/frost/internal/router"
)

func multiPools() []router.Pool {
	return []router.Pool{
		{ID: "claude-main", Kind: "subscription", MarginalCost: "included", MappingVerified: true, RequiredWindowIDs: []string{"primary", "secondary"}, ExpiryPreferenceWindowIDs: []string{"secondary"}},
		{ID: "host-5090", Kind: "local", MarginalCost: "included", MappingVerified: true, RequiredWindowIDs: []string{"online", "vram_free", "queue_slots"}},
		{ID: "anthropic-credit", Kind: "prepaid", MarginalCost: "included", MappingVerified: true, RequiredWindowIDs: []string{"monthly"}, ExpiryPreferenceWindowIDs: []string{"monthly"}, NonRolloverWindowIDs: []string{"monthly"}, ExpiryHorizonHours: 168},
	}
}

func writeSnapshot(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const claudeSnap = `{"schema_version":1,"generated_at":"2026-09-17T00:59:30Z","producer":"codexbar-example","pools":[
 {"id":"claude-main","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:59:00Z","valid_until":"2026-09-17T01:14:00Z","windows":[
  {"id":"primary","remaining_percent":39,"resets_at":"2026-09-17T05:00:00Z","duration_seconds":18000},
  {"id":"secondary","remaining_percent":54,"resets_at":"2026-09-19T13:00:00Z","duration_seconds":604800}]}]}`

// The local-fleet contract: a host is a pool; online, VRAM headroom, and queue
// slots are windows reported as a remaining percentage without a reset. The
// credit pool is a prepaid pool with one monthly window that resets.
const rookSnap = `{"schema_version":1,"generated_at":"2026-09-17T00:59:50Z","producer":"rook","pools":[
 {"id":"host-5090","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:59:50Z","valid_until":"2026-09-17T01:01:50Z","windows":[
  {"id":"online","remaining_percent":100,"resets_at":null},
  {"id":"vram_free","remaining_percent":38.5,"resets_at":null},
  {"id":"queue_slots","remaining_percent":75,"resets_at":null}]},
 {"id":"anthropic-credit","source_status":"ok","measurement":"exact","observed_at":"2026-09-17T00:59:50Z","valid_until":"2026-09-17T01:14:50Z","windows":[
  {"id":"monthly","remaining_percent":64,"resets_at":"2026-09-20T00:00:00Z","duration_seconds":2592000}]}]}`

func TestSourcesLoadIndependently(t *testing.T) {
	dir := t.TempDir()
	a := writeSnapshot(t, dir, "codexbar.json", claudeSnap)
	b := writeSnapshot(t, dir, "rook.json", rookSnap)
	bad := writeSnapshot(t, dir, "undeclared.json", `{"schema_version":1,"generated_at":"2026-09-17T00:59:00Z","pools":[{"id":"nope","source_status":"ok","measurement":"exact","observed_at":null,"valid_until":null,"windows":[]}]}`)
	garbage := writeSnapshot(t, dir, "garbage.json", `{not json`)
	missing := filepath.Join(dir, "missing.json")

	srcs := LoadSources([]string{a, b, bad, garbage, missing}, multiPools(), now)
	if !srcs[0].Usable() || !srcs[1].Usable() {
		t.Fatalf("healthy producers must load: %v %v", srcs[0].Err, srcs[1].Err)
	}
	if srcs[2].Usable() || !HasErrors(srcs[2].Problems) || !strings.Contains(srcs[2].Err.Error(), "cannot add pools") {
		t.Fatalf("undeclared pool: %+v", srcs[2])
	}
	if srcs[3].Usable() || srcs[3].Missing {
		t.Fatalf("garbage must be invalid, not missing: %+v", srcs[3])
	}
	if srcs[4].Usable() || !srcs[4].Missing {
		t.Fatalf("missing file: %+v", srcs[4])
	}
	// Topology validation of one file does not complain about pools another
	// producer owns.
	for _, p := range srcs[0].Problems {
		if strings.Contains(p.Message, "absent") {
			t.Fatalf("per-file validation must not warn about other producers' pools: %+v", p)
		}
	}

	merged, hash, problems := Merge(srcs)
	if len(problems) != 0 || hash == "" || len(merged.Pools) != 3 || merged.Producer != "codexbar-example+rook" {
		t.Fatalf("merge: %+v hash %q problems %+v", merged, hash, problems)
	}
	if !merged.GeneratedAt.Equal(time.Date(2026, 9, 17, 0, 59, 50, 0, time.UTC)) {
		t.Fatalf("generated_at is the latest source: %v", merged.GeneratedAt)
	}
	if absent := Absent(merged, multiPools()); len(absent) != 0 {
		t.Fatalf("all pools reported: %+v", absent)
	}
}

func TestMergeHashAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	a := writeSnapshot(t, dir, "a.json", claudeSnap)
	b := writeSnapshot(t, dir, "b.json", rookSnap)
	one := LoadSources([]string{a}, multiPools(), now)
	_, h1, _ := Merge(one)
	if h1 != one[0].Hash {
		t.Fatalf("a single producer keeps its own hash so legacy provenance is unchanged: %s vs %s", h1, one[0].Hash)
	}
	both := LoadSources([]string{a, b}, multiPools(), now)
	_, h2, _ := Merge(both)
	_, h3, _ := Merge(LoadSources([]string{b, a}, multiPools(), now))
	if h2 == h1 || h2 == h3 || h2 == both[1].Hash {
		t.Fatalf("a combined hash must differ from each input and depend on order: %s %s %s", h1, h2, h3)
	}

	// Two producers claiming one pool: the earlier in the list wins.
	dup := writeSnapshot(t, dir, "dup.json", strings.Replace(claudeSnap, `"remaining_percent":39`, `"remaining_percent":1`, 1))
	merged, _, problems := Merge(LoadSources([]string{a, dup}, multiPools(), now))
	if len(merged.Pools) != 1 || *merged.Pools[0].Windows[0].RemainingPercent != 39 {
		t.Fatalf("first producer must win: %+v", merged.Pools)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "claude-main is reported by") {
		t.Fatalf("problems %+v", problems)
	}

	// Nothing usable: no hash, so the caller reports capacity as not considered.
	if m, h, _ := Merge(LoadSources([]string{filepath.Join(dir, "none.json")}, multiPools(), now)); h != "" || len(m.Pools) != 0 {
		t.Fatalf("empty merge: %+v %q", m, h)
	}
}

// A stale, failed, or missing producer degrades only its own pools. Evaluated
// at one instant, rook's pools stay available while the stale codexbar pool is
// unknown: unknown is never exhausted.
func TestStaleProducerDegradesOnlyItsOwnPools(t *testing.T) {
	dir := t.TempDir()
	a := writeSnapshot(t, dir, "codexbar.json", claudeSnap)
	b := writeSnapshot(t, dir, "rook.json", rookSnap)
	pools := multiPools()
	profiles := []router.Profile{
		{ID: "claude-cli", PoolIDs: []string{"claude-main"}},
		{ID: "gw-local", PoolIDs: []string{"host-5090"}},
		{ID: "gw-credit", PoolIDs: []string{"anthropic-credit"}},
	}
	pol := router.DefaultPolicy().Capacity
	eval := func(at time.Time, paths ...string) map[string]router.Availability {
		merged, _, _ := Merge(LoadSources(paths, pools, at))
		ev := router.EvaluatePools(&merged, pools, profiles, pol, at)
		out := map[string]router.Availability{}
		for id, pa := range ev.Profiles {
			out[id] = pa.Availability
		}
		return out
	}

	got := eval(now, a, b)
	if got["claude-cli"] != router.Available || got["gw-local"] != router.Available || got["gw-credit"] != router.Available {
		t.Fatalf("fresh: %v", got)
	}

	// 13 minutes on: codexbar (15 minute valid_until) is still fresh, rook
	// (2 minute valid_until) is not. Only rook's pools degrade.
	later := now.Add(13 * time.Minute)
	got = eval(later, a, b)
	if got["claude-cli"] != router.Available || got["gw-local"] != router.Unknown {
		t.Fatalf("rook's short valid_until must age only rook's host pool: %v", got)
	}
	if got["gw-credit"] != router.Available {
		t.Fatalf("the credit pool carries its own, longer valid_until: %v", got)
	}

	// A missing producer file removes only its pools.
	got = eval(now, a, filepath.Join(dir, "missing.json"))
	if got["claude-cli"] != router.Available || got["gw-local"] != router.Unknown || got["gw-credit"] != router.Unknown {
		t.Fatalf("missing producer: %v", got)
	}

	// A producer reporting failure marks its pool unknown; the other is untouched.
	failing := writeSnapshot(t, dir, "failing.json", strings.Replace(rookSnap, `"id":"host-5090","source_status":"ok"`, `"id":"host-5090","source_status":"error"`, 1))
	got = eval(now, a, failing)
	if got["gw-local"] != router.Unknown || got["claude-cli"] != router.Available || got["gw-credit"] != router.Available {
		t.Fatalf("failed producer: %v", got)
	}
}

func TestCreditPoolEarnsExpiryPreferenceOnItsOwnHorizon(t *testing.T) {
	dir := t.TempDir()
	b := writeSnapshot(t, dir, "rook.json", rookSnap)
	merged, _, _ := Merge(LoadSources([]string{b}, multiPools(), now))
	pol := router.DefaultPolicy().Capacity
	profiles := []router.Profile{{ID: "gw-credit", PoolIDs: []string{"anthropic-credit"}}}
	ev := router.EvaluatePools(&merged, multiPools(), profiles, pol, now)
	pa := ev.Profiles["gw-credit"]
	// The credit resets in about 71 hours: outside the policy's 24 hours,
	// inside the pool's 168.
	if !pa.ExpiryEligible || pa.ExpiryAt == nil {
		t.Fatalf("credit with a 168h horizon should be expiry eligible: %+v", pa)
	}
	pools := multiPools()
	pools[2].ExpiryHorizonHours = 0
	ev = router.EvaluatePools(&merged, pools, profiles, pol, now)
	if ev.Profiles["gw-credit"].ExpiryEligible {
		t.Fatalf("without the override the policy horizon applies: %+v", ev.Profiles["gw-credit"])
	}
}
