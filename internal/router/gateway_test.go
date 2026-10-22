package router

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func gatewayProfile(id, alias string, quality, cost int, pools ...string) Profile {
	return Profile{
		ID: id, ModelID: "cheap", AccessSurface: SurfaceGateway, Enabled: true,
		Capabilities: []string{CapRepositoryTools, CapWebResearch}, EffortMode: EffortNone,
		MappingVerified: true, LatencyClass: "fast", PoolIDs: pools,
		Alias: alias, Gateway: &GatewayRef{ID: "litellm", BaseURL: "http://aerie.local:4000/v1"},
		Prior: &Prior{QualityRank: quality, CostRank: cost},
	}
}

func TestGatewaySelectionCarriesExecutableTarget(t *testing.T) {
	in := inputs()
	in.Profiles = append(in.Profiles, gatewayProfile("gw-fast", "fast", 4, 9))
	d := Recommend(Task{Text: "x"}, easy(), in)
	if d.Recommendation == nil || d.Recommendation.ProfileID != "gw-fast" {
		t.Fatalf("gateway profile should win on cost: %+v", d.Recommendation)
	}
	want := &GatewayTarget{ID: "litellm", BaseURL: "http://aerie.local:4000/v1", Alias: "fast"}
	if g := d.Recommendation.Gateway; g == nil || *g != *want {
		t.Fatalf("gateway target %+v", g)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var generic struct {
		Recommendation struct {
			Gateway map[string]string `json:"gateway"`
		} `json:"recommendation"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	g := generic.Recommendation.Gateway
	if g["alias"] != "fast" || g["base_url"] != "http://aerie.local:4000/v1" || g["id"] != "litellm" {
		t.Fatalf("stable JSON field recommendation.gateway: %v", g)
	}
}

func TestNonGatewaySelectionOmitsGateway(t *testing.T) {
	d := Recommend(Task{Text: "x"}, easy(), inputs())
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), `"gateway"`) || strings.Contains(string(raw), "access_surfaces") {
		t.Fatalf("legacy output must not mention gateway fields: %s", raw)
	}
}

func TestAccessSurfaceConstraintLimitsCandidates(t *testing.T) {
	in := inputs()
	// The gateway profile is the dearest, so without the constraint it loses.
	in.Profiles = append(in.Profiles, gatewayProfile("gw-chat", "chat", 4, 1))
	if d := Recommend(Task{Text: "x"}, easy(), in); d.Recommendation.ProfileID == "gw-chat" {
		t.Fatalf("unconstrained pick should be cheaper: %+v", d.Recommendation)
	}
	task := Task{Text: "x", Constraints: Constraints{AccessSurfaces: []string{SurfaceGateway}}}
	d := Recommend(task, easy(), in)
	if d.Recommendation == nil || d.Recommendation.ProfileID != "gw-chat" {
		t.Fatalf("surface constraint: %+v", d.Recommendation)
	}
	excluded := map[string]string{}
	for _, e := range d.ExcludedCandidates {
		excluded[e.ProfileID] = e.Reason
	}
	if !strings.Contains(excluded["cheap-cli"], "Access surface cli-b is not in the allowed surface list") {
		t.Fatalf("exclusion reasons: %v", excluded)
	}
	// No gateway profile at all: an honest no_match, not a CLI fallback.
	task = Task{Text: "x", Constraints: Constraints{AccessSurfaces: []string{SurfaceGateway}}}
	if d := Recommend(task, easy(), inputs()); d.Status != StatusNoMatch || d.Recommendation != nil {
		t.Fatalf("status %s rec %+v", d.Status, d.Recommendation)
	}
}

// A monthly credit expires at the end of its billing cycle with no rollover.
// With the default 24 hour horizon it earns no preference three days out; a
// per-pool horizon of a week does, and only for that pool.
func TestPoolExpiryHorizonOverride(t *testing.T) {
	credit := Pool{ID: "anthropic-credit", Kind: PoolKindPrepaid, MarginalCost: MarginalIncluded, MappingVerified: true,
		RequiredWindowIDs: []string{"monthly"}, ExpiryPreferenceWindowIDs: []string{"monthly"}, NonRolloverWindowIDs: []string{"monthly"}}
	week := Pool{ID: "weekly", Kind: PoolKindSubscription, MarginalCost: MarginalIncluded, MappingVerified: true,
		RequiredWindowIDs: []string{"w"}, ExpiryPreferenceWindowIDs: []string{"w"}}
	profiles := []Profile{
		gatewayProfile("gw-credit", "claude", 4, 3, "anthropic-credit"),
		{ID: "twin", ModelID: "cheap", AccessSurface: "cli-a", Enabled: true, Capabilities: []string{CapRepositoryTools, CapWebResearch}, EffortMode: EffortNone, PoolIDs: []string{"weekly"}, Prior: &Prior{QualityRank: 4, CostRank: 3}},
	}
	snap := snapshot(
		pool("anthropic-credit", -time.Minute, MeasurementExact, WindowState{ID: "monthly", RemainingPercent: pct(70), ResetsAt: at(3 * 24 * time.Hour), DurationSeconds: 2592000}),
		pool("weekly", -time.Minute, MeasurementExact, WindowState{ID: "w", RemainingPercent: pct(70), ResetsAt: at(3 * 24 * time.Hour), DurationSeconds: 604800}),
	)
	run := func(creditHorizon float64) Decision {
		c := credit
		c.ExpiryHorizonHours = creditHorizon
		in := capInputs(snap)
		in.Pools = []Pool{c, week}
		in.Profiles = profiles
		return Recommend(Task{Text: "x"}, easy(), in)
	}
	// Default horizon: neither is inside 24 hours, so the stable ID decides.
	d := run(0)
	if d.Recommendation.ProfileID != "gw-credit" || d.Recommendation.ExpiryPreferred {
		t.Fatalf("no preference expected, ties break on ID: %+v", d.Recommendation)
	}
	// Flip the IDs so the stable-ID tiebreak would pick the other profile; the
	// override must then be what selects the credit.
	profiles[0].ID = "z-credit"
	d = run(0)
	if d.Recommendation.ProfileID != "twin" {
		t.Fatalf("baseline: %+v", d.Recommendation)
	}
	d = run(168)
	if d.Recommendation.ProfileID != "z-credit" || !d.Recommendation.ExpiryPreferred {
		t.Fatalf("a 168h horizon should prefer the expiring credit: %+v", d.Recommendation)
	}
	if !strings.Contains(strings.Join(d.DecisionReasons, " "), "expires sooner") {
		t.Fatalf("reasons: %v", d.DecisionReasons)
	}
}

// Local fleet pools use the same snapshot shape: online, memory headroom, and
// queue slots are windows with a remaining percentage and no reset.
func TestLocalPoolExhaustionExcludesAndUnknownDoesNot(t *testing.T) {
	local := Pool{ID: "host-5090", Kind: PoolKindLocal, MarginalCost: MarginalIncluded, MappingVerified: true,
		RequiredWindowIDs: []string{"online", "vram_free", "queue_slots"}, ExpiryPreferenceWindowIDs: nil}
	profiles := []Profile{gatewayProfile("gw-local", "code", 4, 9, "host-5090"), gatewayProfile("gw-cloud", "chat", 4, 3)}
	local100 := func(id string, v float64) WindowState { return WindowState{ID: id, RemainingPercent: pct(v)} }
	mk := func(snap *Capacity) Decision {
		in := capInputs(snap)
		in.Pools = []Pool{local}
		in.Profiles = profiles
		return Recommend(Task{Text: "x"}, easy(), in)
	}
	up := snapshot(pool("host-5090", -time.Minute, MeasurementExact, local100("online", 100), local100("vram_free", 40), local100("queue_slots", 90)))
	if d := mk(up); d.Recommendation.ProfileID != "gw-local" || d.Recommendation.Availability != "available" || d.Recommendation.ExpiryPreferred {
		t.Fatalf("online host: %+v", d.Recommendation)
	}
	down := snapshot(pool("host-5090", -time.Minute, MeasurementExact, local100("online", 0), local100("vram_free", 40), local100("queue_slots", 90)))
	if d := mk(down); d.Recommendation.ProfileID != "gw-cloud" {
		t.Fatalf("host reported offline must be excluded: %+v", d.Recommendation)
	}
	failed := snapshot(PoolState{ID: "host-5090", SourceStatus: SourceError, Measurement: MeasurementUnknown, ObservedAt: at(-time.Minute)})
	if d := mk(failed); d.Recommendation.ProfileID != "gw-local" || d.Recommendation.Availability != "unknown" {
		t.Fatalf("a failed producer is unknown, never exhausted: %+v", d.Recommendation)
	}
	if d := mk(nil); d.Recommendation.ProfileID != "gw-local" {
		t.Fatalf("no snapshot: %+v", d.Recommendation)
	}
}
