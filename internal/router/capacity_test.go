package router

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Matrix fixtures: the slice 1 profiles bound to pools. Fixed clock is
// fixedNow (2026-09-17T01:00:00Z).

func capPools() []Pool {
	return []Pool{
		{ID: "pool-a", Kind: PoolKindSubscription, MarginalCost: MarginalIncluded, MappingVerified: true, RequiredWindowIDs: []string{"weekly"}, ExpiryPreferenceWindowIDs: []string{"weekly"}},
		{ID: "pool-b", Kind: PoolKindSubscription, MarginalCost: MarginalIncluded, MappingVerified: true, RequiredWindowIDs: []string{"weekly"}, ExpiryPreferenceWindowIDs: []string{"weekly"}},
		{ID: "pool-c", Kind: PoolKindSubscription, MarginalCost: MarginalIncluded, MappingVerified: true, RequiredWindowIDs: []string{"five_hour", "weekly"}, ExpiryPreferenceWindowIDs: []string{"weekly"}},
		{ID: "pool-unverified", Kind: PoolKindSubscription, MarginalCost: MarginalIncluded, MappingVerified: false, RequiredWindowIDs: []string{"weekly"}, ExpiryPreferenceWindowIDs: []string{"weekly"}},
	}
}

// Two adequate profiles tied on cost so the tie group is real: twin-a and
// twin-b share prior q4 c3; strong sits above them.
func capProfiles() []Profile {
	caps := []string{CapRepositoryTools, CapWebResearch}
	return []Profile{
		{ID: "strong-cli", ModelID: "strong", AccessSurface: "cli-a", Enabled: true, Capabilities: caps, EffortMode: EffortNone, LatencyClass: "slow", PoolIDs: []string{"pool-c"}, Prior: &Prior{QualityRank: 1, CostRank: 1}},
		{ID: "twin-a", ModelID: "cheap", AccessSurface: "cli-a", Enabled: true, Capabilities: caps, EffortMode: EffortNone, LatencyClass: "fast", PoolIDs: []string{"pool-a"}, Prior: &Prior{QualityRank: 4, CostRank: 3}},
		{ID: "twin-b", ModelID: "cheap", AccessSurface: "cli-b", Enabled: true, Capabilities: caps, EffortMode: EffortNone, LatencyClass: "fast", PoolIDs: []string{"pool-b"}, Prior: &Prior{QualityRank: 4, CostRank: 3}},
	}
}

func at(d time.Duration) *time.Time { t := fixedNow.Add(d); return &t }
func pct(v float64) *float64        { return &v }

func pool(id string, observed time.Duration, measurement string, windows ...WindowState) PoolState {
	return PoolState{ID: id, SourceStatus: SourceOK, Measurement: measurement, ObservedAt: at(observed), Windows: windows}
}

func win(id string, remaining *float64, resets time.Duration) WindowState {
	return WindowState{ID: id, RemainingPercent: remaining, ResetsAt: at(resets), DurationSeconds: 604800}
}

func capInputs(snap *Capacity) Inputs {
	in := Inputs{Catalog: fixtureCatalog(), Profiles: capProfiles(), Pools: capPools(), Policy: fixturePolicy(), Now: fixedNow, ProgramVersion: "test", Capacity: snap, CapacityHash: "h"}
	in.Policy.Capacity.AllowEstimatedMeasurements = false
	in.Policy.Capacity.EnforceAvailability = true
	return in
}

func snapshot(pools ...PoolState) *Capacity {
	return &Capacity{GeneratedAt: fixedNow, Producer: "test", Pools: pools}
}

func easy() Assessment { return assessment(peaked(0, 5), ContractCodeEdit) }

func TestCapacityMatrix(t *testing.T) {
	fresh := -4 * time.Minute
	type tc struct {
		name      string
		snap      *Capacity
		tweak     func(*Inputs)
		task      Constraints
		assess    *Assessment
		want      string // winner
		status    string
		used      bool
		warn      string
		excluded  []string
		alt       string
		reason    string
		expiryPre bool
	}
	cases := []tc{
		{name: "no-snapshot-static", snap: nil, want: "twin-a", status: StatusProvisional, warn: "not considered"},
		{name: "fresh-exact-changes-tie", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(58), 6*24*time.Hour)),
			pool("pool-b", fresh, MeasurementExact, win("weekly", pct(58), 19*time.Hour)),
		), want: "twin-b", used: true, reason: "expires sooner", expiryPre: true},
		{name: "stale-zero-not-exhausted", snap: snapshot(
			pool("pool-a", -41*time.Minute, MeasurementExact, win("weekly", pct(0), 19*time.Hour)),
		), want: "twin-a", warn: "stale for pools pool-a", excluded: nil},
		{name: "estimated-zero-no-optin", snap: snapshot(
			pool("pool-a", fresh, MeasurementEstimated, win("weekly", pct(0), 19*time.Hour)),
		), want: "twin-a", warn: "estimated measurements; ignored"},
		{name: "estimated-with-optin", snap: snapshot(
			pool("pool-a", fresh, MeasurementEstimated, win("weekly", pct(0), 19*time.Hour)),
			pool("pool-b", fresh, MeasurementExact, win("weekly", pct(50), 5*24*time.Hour)),
		), tweak: func(in *Inputs) { in.Policy.Capacity.AllowEstimatedMeasurements = true }, want: "twin-b", used: true, excluded: []string{"twin-a"}},
		{name: "primary-full-weekly-empty", snap: snapshot(
			pool("pool-c", fresh, MeasurementExact, win("five_hour", pct(100), 3*time.Hour), win("weekly", pct(0), 3*24*time.Hour)),
		), tweak: func(in *Inputs) { in.Profiles = in.Profiles[:1] }, want: "", status: StatusNoMatch, used: true, excluded: []string{"strong-cli"}},
		{name: "reset-cannot-override-quality", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(80), 2*time.Hour)),
			pool("pool-c", fresh, MeasurementExact, win("five_hour", pct(80), 3*time.Hour), win("weekly", pct(80), 5*24*time.Hour)),
		), assess: ptr(assessment(peaked(2, 5), ContractCodeEdit)), want: "strong-cli", used: true, excluded: []string{"twin-a", "twin-b"}},
		{name: "reset-cannot-override-exhausted-overlap", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(80), 2*time.Hour)),
			pool("pool-b", fresh, MeasurementExact, win("weekly", pct(0), 2*time.Hour)),
		), tweak: func(in *Inputs) { in.Profiles[1].PoolIDs = []string{"pool-a", "pool-b"} }, want: "strong-cli", used: true, excluded: []string{"twin-a", "twin-b"}},
		{name: "unverified-binding-ignored", snap: snapshot(
			pool("pool-unverified", fresh, MeasurementExact, win("weekly", pct(0), 2*time.Hour)),
		), tweak: func(in *Inputs) { in.Profiles[1].PoolIDs = []string{"pool-unverified"} }, want: "twin-a", warn: "unverified binding"},
		{name: "null-primary-required", snap: snapshot(
			pool("pool-c", fresh, MeasurementExact, WindowState{ID: "five_hour"}, win("weekly", pct(80), 3*24*time.Hour)),
		), tweak: func(in *Inputs) { in.Profiles = in.Profiles[:1] }, want: "strong-cli", warn: "Availability of strong-cli is unknown"},
		{name: "claude-source-error", snap: snapshot(
			PoolState{ID: "pool-a", SourceStatus: SourceError, Measurement: MeasurementUnknown, ObservedAt: at(fresh)},
		), want: "twin-a", warn: "does not mean the harness is unavailable"},
		{name: "identical-five-hour-different-weekly", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(50), 6*24*time.Hour)),
			pool("pool-b", fresh, MeasurementExact, win("weekly", pct(50), 6*time.Hour)),
		), want: "twin-b", used: true, expiryPre: true},
		{name: "five-hour-not-designated", snap: snapshot(
			pool("pool-c", fresh, MeasurementExact, win("five_hour", pct(50), 2*time.Hour), win("weekly", pct(50), 6*24*time.Hour)),
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(50), 6*24*time.Hour)),
		), tweak: func(in *Inputs) {
			// Make strong-cli tie with twin-a on cost so expiry alone could decide.
			in.Profiles[0].Prior = &Prior{QualityRank: 1, CostRank: 3}
			in.Profiles = in.Profiles[:2]
		}, want: "strong-cli", used: true, expiryPre: false},
		{name: "missing-required-weekly", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact),
		), want: "twin-a", warn: "Availability of twin-a is unknown"},
		{name: "generated-at-cannot-freshen", snap: &Capacity{GeneratedAt: fixedNow, Pools: []PoolState{
			pool("pool-a", -40*time.Minute, MeasurementExact, win("weekly", pct(0), 19*time.Hour)),
		}}, want: "twin-a", warn: "stale"},
		{name: "post-reset-not-refilled", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(30), -10*time.Minute)),
		), want: "twin-a", warn: "reset boundary passed"},
		{name: "tiny-task-not-upgraded", snap: snapshot(
			pool("pool-c", fresh, MeasurementExact, win("five_hour", pct(90), 3*time.Hour), win("weekly", pct(90), 2*time.Hour)),
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(90), 6*24*time.Hour)),
		), tweak: func(in *Inputs) {
			// pool-c expires tonight and holds both strong-cli and a cheap twin;
			// the expiring pool is favored, and the cheapest profile in it wins.
			in.Profiles = append(in.Profiles, Profile{ID: "twin-c", ModelID: "cheap", AccessSurface: "cli-a", Enabled: true, Capabilities: []string{CapRepositoryTools}, EffortMode: EffortNone, PoolIDs: []string{"pool-c"}, Prior: &Prior{QualityRank: 4, CostRank: 3}})
		}, want: "twin-c", used: true, expiryPre: true},
		{name: "enforce-excludes", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(0), 19*time.Hour)),
			pool("pool-b", fresh, MeasurementExact, win("weekly", pct(60), 5*24*time.Hour)),
		), want: "twin-b", used: true, excluded: []string{"twin-a"}},
		{name: "enforce-off-demotes", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(0), 19*time.Hour)),
			pool("pool-b", fresh, MeasurementExact, win("weekly", pct(60), 5*24*time.Hour)),
		), tweak: func(in *Inputs) { in.Policy.Capacity.EnforceAvailability = false }, want: "twin-b", used: true, reason: "on availability"},
		{name: "per-call-demote-override", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(0), 19*time.Hour)),
			pool("pool-b", fresh, MeasurementExact, win("weekly", pct(0), 5*24*time.Hour)),
			pool("pool-c", fresh, MeasurementExact, win("five_hour", pct(50), 3*time.Hour), win("weekly", pct(50), 5*24*time.Hour)),
		), task: Constraints{Availability: AvailabilityDemote, Policy: ModeQuality}, tweak: func(in *Inputs) {
			in.Capacity.Pools[2].Windows[1].RemainingPercent = pct(0) // strong-cli's pool exhausted too
			in.Capacity.Pools[1].Windows[0].RemainingPercent = pct(50)
		}, want: "strong-cli", used: true, warn: "has exhausted included usage", alt: "available"},
		{name: "demote-reorders-tie-group", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(0), 19*time.Hour)),
			pool("pool-b", fresh, MeasurementExact, win("weekly", pct(60), 5*24*time.Hour)),
		), task: Constraints{Availability: AvailabilityDemote}, want: "twin-b", used: true, reason: "on availability"},
		{name: "per-call-ignore", snap: snapshot(
			pool("pool-a", fresh, MeasurementExact, win("weekly", pct(0), 19*time.Hour)),
		), task: Constraints{Availability: AvailabilityIgnore}, want: "twin-a", warn: "set to ignore"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := capInputs(c.snap)
			if c.tweak != nil {
				c.tweak(&in)
			}
			a := easy()
			if c.assess != nil {
				a = *c.assess
			}
			d := Recommend(task(c.task), a, in)
			if c.status == "" {
				c.status = StatusProvisional
			}
			if d.Status != c.status {
				t.Fatalf("status %s, want %s; reasons %v", d.Status, c.status, d.DecisionReasons)
			}
			got := ""
			if d.Recommendation != nil {
				got = d.Recommendation.ProfileID
			}
			if got != c.want {
				t.Fatalf("winner %q, want %q; reasons %v excluded %+v", got, c.want, d.DecisionReasons, d.ExcludedCandidates)
			}
			if d.Provenance.CapacityUsed != c.used {
				t.Fatalf("capacity used %v, want %v", d.Provenance.CapacityUsed, c.used)
			}
			if c.warn != "" && !hasWarning(d, c.warn) {
				t.Fatalf("missing warning %q in %v", c.warn, d.Warnings)
			}
			for _, id := range c.excluded {
				if !excluded(d, id) {
					t.Fatalf("expected %s excluded: %+v", id, d.ExcludedCandidates)
				}
			}
			if c.alt != "" && !strings.Contains(roles(d), c.alt) {
				t.Fatalf("expected %s alternative: %s", c.alt, roles(d))
			}
			if c.reason != "" && !hasReason(d, c.reason) {
				t.Fatalf("missing reason %q in %v", c.reason, d.DecisionReasons)
			}
			if d.Recommendation != nil && d.Recommendation.ExpiryPreferred != c.expiryPre {
				t.Fatalf("expiry_preferred %v, want %v", d.Recommendation.ExpiryPreferred, c.expiryPre)
			}
		})
	}
}

func TestSharedPoolCountedOnce(t *testing.T) {
	// Astra and Luna share a pool; Astra is inadequate at the level. Luna
	// wins and Astra never appears through the shared pool.
	in := capInputs(snapshot(pool("pool-a", -4*time.Minute, MeasurementExact, win("weekly", pct(70), 3*time.Hour))))
	in.Profiles = []Profile{
		{ID: "astra", ModelID: "strong", AccessSurface: "codex", Enabled: true, Capabilities: []string{CapRepositoryTools}, EffortMode: EffortNone, PoolIDs: []string{"pool-a"}, Prior: &Prior{QualityRank: 5, CostRank: 1}},
		{ID: "luna", ModelID: "cheap", AccessSurface: "codex", Enabled: true, Capabilities: []string{CapRepositoryTools}, EffortMode: EffortNone, PoolIDs: []string{"pool-a"}, Prior: &Prior{QualityRank: 8, CostRank: 9}},
	}
	in.Policy.PriorMaxQualityRankByLevel = []int{9, 4, 3, 2, 1}
	a := assessment(peaked(0, 5), ContractCodeEdit)
	d := Recommend(task(Constraints{}), a, in)
	if d.Recommendation.ProfileID != "luna" {
		t.Fatalf("got %s", d.Recommendation.ProfileID)
	}
	a = assessment(peaked(2, 5), ContractCodeEdit)
	d = Recommend(task(Constraints{}), a, in)
	if d.Status != StatusNoMatch || !excluded(d, "astra") {
		t.Fatalf("astra must fail adequacy regardless of pool: %s %+v", d.Status, d.ExcludedCandidates)
	}
}

func TestLargeWorkloadReserveCaveat(t *testing.T) {
	in := capInputs(snapshot(pool("pool-a", -4*time.Minute, MeasurementExact, win("weekly", pct(70), 3*time.Hour))))
	a := easy()
	a.Scores[QWorkload] = peaked(3, 4)
	d := Recommend(task(Constraints{}), a, in)
	if !hasWarning(d, "not a fit guarantee") {
		t.Fatalf("warnings %v", d.Warnings)
	}
}

func TestCapacityDeterministicAndSelectionFields(t *testing.T) {
	in := capInputs(snapshot(
		pool("pool-a", -4*time.Minute, MeasurementExact, win("weekly", pct(58), 6*24*time.Hour)),
		pool("pool-b", -4*time.Minute, MeasurementExact, win("weekly", pct(58), 19*time.Hour)),
	))
	x, _ := json.Marshal(Recommend(task(Constraints{}), easy(), in))
	y, _ := json.Marshal(Recommend(task(Constraints{}), easy(), in))
	if string(x) != string(y) {
		t.Fatal("non-deterministic")
	}
	d := Recommend(task(Constraints{}), easy(), in)
	r := d.Recommendation
	if r.Availability != string(Available) || r.AvailabilityBasis != MeasurementExact || r.CapacityObservedAt == nil || !r.ExpiryPreferred {
		t.Fatalf("selection %+v", r)
	}
	if d.Provenance.CapacityAgeMin == nil || *d.Provenance.CapacityAgeMin != 4 || d.Provenance.CapacityBasis != MeasurementExact || d.Provenance.CapacityHash != "h" {
		t.Fatalf("provenance %+v", d.Provenance)
	}
}

func TestEvaluatePoolsUnknownWithoutBinding(t *testing.T) {
	in := capInputs(snapshot(pool("pool-a", -4*time.Minute, MeasurementExact, win("weekly", pct(70), 3*time.Hour))))
	in.Profiles[1].PoolIDs = nil
	ev := EvaluatePools(in.Capacity, in.Pools, in.Profiles, in.Policy.Capacity, fixedNow)
	if pa := ev.Profiles["twin-a"]; pa.Availability != Unknown || !strings.Contains(pa.Reasons[0], "no pool binding") {
		t.Fatalf("%+v", pa)
	}
	if pa := ev.Profiles["twin-b"]; pa.Availability != Unknown {
		t.Fatalf("pool-b absent from snapshot must be unknown: %+v", pa)
	}
	if !ev.Used {
		t.Fatal("pool-a is known, so the snapshot was used")
	}
}
