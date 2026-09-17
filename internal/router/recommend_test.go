package router

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 9, 17, 1, 0, 0, 0, time.UTC)

func fixtureCatalog() Catalog {
	gen := []string{ContractGeneratedText, ContractCodeEdit, ContractStructuredObj}
	return Catalog{Version: "test", Models: map[string]Model{
		"strong": {ID: "strong", Label: "Strong", OutputContracts: gen},
		"mid":    {ID: "mid", Label: "Mid", OutputContracts: gen},
		"cheap":  {ID: "cheap", Label: "Cheap", OutputContracts: gen},
		"judge":  {ID: "judge", Label: "Judge", OutputContracts: []string{ContractTypedDecision}, GenerationMechanism: "undisclosed"},
	}}
}

func fixtureProfiles() []Profile {
	caps := []string{CapRepositoryTools, CapWebResearch}
	return []Profile{
		{ID: "strong-cli", ModelID: "strong", AccessSurface: "cli-a", Enabled: true, Capabilities: caps, EffortMode: EffortConfigurable, EffortOptions: []string{"low", "medium", "high", "xhigh"}, LatencyClass: "slow", Prior: &Prior{QualityRank: 1, CostRank: 1}},
		{ID: "mid-cli", ModelID: "mid", AccessSurface: "cli-a", Enabled: true, Capabilities: caps, EffortMode: EffortConfigurable, EffortOptions: []string{"low", "high"}, LatencyClass: "medium", Prior: &Prior{QualityRank: 2, CostRank: 2}},
		{ID: "cheap-cli", ModelID: "cheap", AccessSurface: "cli-b", Enabled: true, Capabilities: caps, EffortMode: EffortNone, LatencyClass: "fast", Prior: &Prior{QualityRank: 4, CostRank: 3}},
		{ID: "judge-api", ModelID: "judge", AccessSurface: "api", Enabled: true, EffortMode: EffortNone, LatencyClass: "extra_fast", Prior: &Prior{QualityRank: 3, CostRank: 4}},
		{ID: "off-cli", ModelID: "mid", AccessSurface: "cli-a", Enabled: false, EffortMode: EffortNone, Prior: &Prior{QualityRank: 1, CostRank: 9}},
	}
}

func fixturePolicy() Policy {
	p := DefaultPolicy()
	p.PriorMaxQualityRankByLevel = []int{9, 4, 3, 2, 1}
	return p
}

func inputs() Inputs {
	return Inputs{Catalog: fixtureCatalog(), Profiles: fixtureProfiles(), Policy: fixturePolicy(), Now: fixedNow, ProgramVersion: "test"}
}

// peaked builds a Score with all probability on one level.
func peaked(level, levels int) ScoreAnswer {
	p := make([]float64, levels)
	p[level] = 1
	return ScoreAnswer{Mean: float64(level), Probabilities: p, Confidence: 1}
}

func dist(probs ...float64) ScoreAnswer {
	mean := 0.0
	for i, p := range probs {
		mean += float64(i) * p
	}
	return ScoreAnswer{Mean: mean, Probabilities: probs, Confidence: 0.5}
}

func assessment(reasoning ScoreAnswer, contract string) Assessment {
	return Assessment{
		Provider: "test", RequestedModel: "m", ReturnedModel: "m", QuestionsVersion: "v", QuestionsHash: "h",
		Scores: map[string]ScoreAnswer{QReasoning: reasoning, QWorkload: peaked(1, 4), QConsequence: peaked(1, 4)},
		Nouls:  map[string]float64{QMissingContext: 0.02, QNeedsImageInput: 0.01, QNeedsWebRes: 0.05, QNeedsRepoTools: 0.9},
		Choices: map[string]ChoiceAnswer{
			QVerification:   {Choice: "tests_and_review", Probabilities: map[string]float64{"tests_and_review": 1}, Confidence: 1},
			QTaskFamily:     {Choice: "software_change", Probabilities: map[string]float64{"software_change": 0.9, "writing": 0.1}, Confidence: 0.9},
			QOutputContract: {Choice: contract, Probabilities: map[string]float64{contract: 1}, Confidence: 1},
			QResponsiveness: {Choice: ResponsivenessAttended, Probabilities: map[string]float64{ResponsivenessAttended: 1}, Confidence: 1},
		},
	}
}

func task(c Constraints) Task { return Task{Text: "do the thing", Constraints: c} }

func TestAdequateModePicksCheapestAdequate(t *testing.T) {
	d := Recommend(task(Constraints{}), assessment(peaked(1, 5), ContractCodeEdit), inputs())
	if d.Status != StatusProvisional || d.Recommendation == nil {
		t.Fatalf("status %s rec %v", d.Status, d.Recommendation)
	}
	if d.Recommendation.ProfileID != "cheap-cli" {
		t.Fatalf("want cheap-cli, got %s", d.Recommendation.ProfileID)
	}
	if d.Recommendation.AdequacyBasis != BasisOperatorPrior {
		t.Fatalf("basis %s", d.Recommendation.AdequacyBasis)
	}
	if !hasWarning(d, "Capacity was not considered") {
		t.Fatalf("expected capacity warning: %v", d.Warnings)
	}
	if got := roles(d); got != "stronger" {
		t.Fatalf("alternatives %s", got)
	}
}

func TestHigherLevelRaisesFloor(t *testing.T) {
	d := Recommend(task(Constraints{}), assessment(peaked(3, 5), ContractCodeEdit), inputs())
	if d.Recommendation.ProfileID != "mid-cli" {
		t.Fatalf("level 3 allows rank<=2: want mid-cli, got %s", d.Recommendation.ProfileID)
	}
	if d.Recommendation.EffortIntent != "high" || d.Recommendation.NativeEffort != nil {
		t.Fatalf("effort intent %q native %v (mapping unverified must hide native)", d.Recommendation.EffortIntent, d.Recommendation.NativeEffort)
	}
	if !excluded(d, "cheap-cli") {
		t.Fatalf("cheap-cli should be excluded below floor: %+v", d.ExcludedCandidates)
	}
}

func TestBimodalDistributionIsNotMedium(t *testing.T) {
	// Half easy, half hard: mean 2 (medium) but the 0.8 quantile is level 4.
	a := assessment(dist(0.5, 0, 0, 0, 0.5), ContractCodeEdit)
	d := Recommend(task(Constraints{}), a, inputs())
	if d.Analysis.Derived.ReasoningLevel != 4 || d.Analysis.Derived.ReasoningMeanLevel != 2 {
		t.Fatalf("level %d mean %d", d.Analysis.Derived.ReasoningLevel, d.Analysis.Derived.ReasoningMeanLevel)
	}
	if d.Recommendation.ProfileID != "strong-cli" {
		t.Fatalf("want strong-cli, got %s", d.Recommendation.ProfileID)
	}
	if !strings.Contains(roles(d), "cheaper") {
		t.Fatalf("expected a cheaper alternative at the mean level: %s", roles(d))
	}
	if !hasReason(d, "upper quantile") {
		t.Fatalf("reasons %v", d.DecisionReasons)
	}
}

func TestPeakedDistributionUsesMean(t *testing.T) {
	d := Recommend(task(Constraints{}), assessment(dist(0, 0.9, 0.1, 0, 0), ContractCodeEdit), inputs())
	if d.Analysis.Derived.ReasoningLevel != 1 {
		t.Fatalf("level %d", d.Analysis.Derived.ReasoningLevel)
	}
}

func TestOutputContractGate(t *testing.T) {
	a := assessment(peaked(0, 5), ContractTypedDecision)
	a.Nouls[QNeedsRepoTools] = 0.05
	d := Recommend(task(Constraints{}), a, inputs())
	if d.Recommendation == nil || d.Recommendation.ProfileID != "judge-api" {
		t.Fatalf("typed decision should select judge-api: %+v", d.Recommendation)
	}
	d = Recommend(task(Constraints{}), assessment(peaked(0, 5), ContractCodeEdit), inputs())
	if !excluded(d, "judge-api") {
		t.Fatalf("judge-api cannot edit code: %+v", d.ExcludedCandidates)
	}
}

func TestCapabilityGate(t *testing.T) {
	a := assessment(peaked(0, 5), ContractStructuredObj)
	a.Nouls[QNeedsImageInput] = 0.95
	d := Recommend(task(Constraints{}), a, inputs())
	if d.Status != StatusNoMatch {
		t.Fatalf("no profile has image_input: status %s", d.Status)
	}
}

func TestExplicitConstraints(t *testing.T) {
	d := Recommend(task(Constraints{ExcludedProfiles: []string{"cheap-cli"}}), assessment(peaked(1, 5), ContractCodeEdit), inputs())
	if d.Recommendation.ProfileID != "mid-cli" {
		t.Fatalf("got %s", d.Recommendation.ProfileID)
	}
	d = Recommend(task(Constraints{AllowedProfiles: []string{"strong-cli"}}), assessment(peaked(1, 5), ContractCodeEdit), inputs())
	if d.Recommendation.ProfileID != "strong-cli" {
		t.Fatalf("got %s", d.Recommendation.ProfileID)
	}
	d = Recommend(task(Constraints{Effort: "xhigh"}), assessment(peaked(1, 5), ContractCodeEdit), inputs())
	if d.Recommendation.ProfileID != "strong-cli" || !excluded(d, "mid-cli") || !excluded(d, "cheap-cli") {
		t.Fatalf("effort xhigh: got %s, excluded %+v", d.Recommendation.ProfileID, d.ExcludedCandidates)
	}
	d = Recommend(task(Constraints{AllowedProfiles: []string{"x"}, ExcludedProfiles: []string{"x"}}), assessment(peaked(1, 5), ContractCodeEdit), inputs())
	if d.Status != StatusConflict {
		t.Fatalf("status %s", d.Status)
	}
	d = Recommend(task(Constraints{Policy: "turbo"}), assessment(peaked(1, 5), ContractCodeEdit), inputs())
	if d.Status != StatusConflict {
		t.Fatalf("unknown policy should conflict: %s", d.Status)
	}
}

func TestDisabledNeverSelected(t *testing.T) {
	d := Recommend(task(Constraints{Policy: ModeQuality}), assessment(peaked(0, 5), ContractCodeEdit), inputs())
	if d.Recommendation.ProfileID == "off-cli" || !excluded(d, "off-cli") {
		t.Fatalf("disabled profile selected or not excluded")
	}
}

func TestQualityAndFastModes(t *testing.T) {
	d := Recommend(task(Constraints{Policy: ModeQuality}), assessment(peaked(0, 5), ContractCodeEdit), inputs())
	if d.Recommendation.ProfileID != "strong-cli" {
		t.Fatalf("quality: %s", d.Recommendation.ProfileID)
	}
	d = Recommend(task(Constraints{Policy: ModeFast}), assessment(peaked(2, 5), ContractCodeEdit), inputs())
	// Level 2 admits strong (slow) and mid (medium); fast picks mid over the stronger, slower profile.
	if d.Recommendation.ProfileID != "mid-cli" {
		t.Fatalf("fast: %s", d.Recommendation.ProfileID)
	}
}

func TestLiveResponsivenessSelectsFast(t *testing.T) {
	a := assessment(peaked(2, 5), ContractGeneratedText)
	a.Choices[QResponsiveness] = ChoiceAnswer{Choice: ResponsivenessLive, Probabilities: map[string]float64{ResponsivenessLive: 0.9}, Confidence: 0.9}
	d := Recommend(task(Constraints{}), a, inputs())
	if d.Analysis.Derived.Mode != ModeFast || d.Analysis.Derived.ModeSource != "responsiveness" {
		t.Fatalf("mode %s source %s", d.Analysis.Derived.Mode, d.Analysis.Derived.ModeSource)
	}
	// Explicit policy wins over the reading.
	d = Recommend(task(Constraints{Policy: ModeQuality}), a, inputs())
	if d.Analysis.Derived.Mode != ModeQuality {
		t.Fatalf("explicit policy should win: %s", d.Analysis.Derived.Mode)
	}
}

func TestRequiredLatencyWithOnlyClassEvidence(t *testing.T) {
	c := Constraints{Latency: &LatencyTarget{TargetMS: 2000, Milestone: MilestoneFirstUseful, Mode: LatencyRequire}}
	d := Recommend(task(c), assessment(peaked(2, 5), ContractGeneratedText), inputs())
	if d.Status != StatusProvisional || d.Recommendation.ProfileID != "mid-cli" {
		t.Fatalf("status %s rec %+v", d.Status, d.Recommendation)
	}
	if !hasWarning(d, "cannot be verified") {
		t.Fatalf("warnings %v", d.Warnings)
	}
	c.Latency.Mode = "sometime"
	if d := Recommend(task(c), assessment(peaked(2, 5), ContractGeneratedText), inputs()); d.Status != StatusConflict {
		t.Fatalf("bad latency mode should conflict: %s", d.Status)
	}
}

func TestRelaxedLowersFloor(t *testing.T) {
	d := Recommend(task(Constraints{Policy: ModeRelaxed}), assessment(peaked(2, 5), ContractCodeEdit), inputs())
	if d.Analysis.Derived.ReasoningLevel != 1 || d.Recommendation.ProfileID != "cheap-cli" {
		t.Fatalf("level %d rec %s", d.Analysis.Derived.ReasoningLevel, d.Recommendation.ProfileID)
	}
	if !hasReason(d, "lowered the adequacy floor") {
		t.Fatalf("reasons %v", d.DecisionReasons)
	}
}

func TestMissingContext(t *testing.T) {
	a := assessment(peaked(2, 5), ContractCodeEdit)
	a.Nouls[QMissingContext] = 0.9
	d := Recommend(task(Constraints{}), a, inputs())
	if d.Status != StatusNeedsContext || d.Recommendation != nil {
		t.Fatalf("status %s", d.Status)
	}
}

func TestHighConsequenceSmallEdit(t *testing.T) {
	a := assessment(peaked(0, 5), ContractCodeEdit)
	a.Scores[QConsequence] = peaked(3, 4)
	a.Scores[QWorkload] = peaked(0, 4)
	in := inputs()
	in.Policy.HighConsequenceMinLevel = ptr(2)
	d := Recommend(task(Constraints{}), a, in)
	if d.Analysis.Derived.ReasoningLevel != 2 || d.Analysis.Derived.FloorAdjustedBy != "high_consequence" {
		t.Fatalf("derived %+v", d.Analysis.Derived)
	}
	if d.Analysis.Derived.ReviewGuidance != "independent review and targeted verification" {
		t.Fatalf("guidance %q", d.Analysis.Derived.ReviewGuidance)
	}
	if d.Recommendation.ProfileID != "mid-cli" {
		t.Fatalf("rec %s", d.Recommendation.ProfileID)
	}
}

func TestAmbiguousTaskFamilyUsesStricterRule(t *testing.T) {
	a := assessment(peaked(1, 5), ContractCodeEdit)
	a.Choices[QTaskFamily] = ChoiceAnswer{Choice: "software_change", Probabilities: map[string]float64{"software_change": 0.5, "writing": 0.42, "other": 0.08}, Confidence: 0.2}
	in := inputs()
	in.Catalog.Models["cheap"] = withMeasurement(in.Catalog.Models["cheap"], Measurement{Source: "s", Metric: "prose.quality", MetricVersion: "1", Value: 0.5, HigherIsBetter: true, TaskFamily: "writing"})
	in.Policy.AdequacyRules = []AdequacyRule{{ID: "w", TaskFamily: "writing", ReasoningLevels: []int{1}, Metric: "prose.quality", MetricVersion: "1", Minimum: 0.7, ProfileMatch: "any", OnMissing: "provisional_only"}}
	d := Recommend(task(Constraints{}), a, in)
	if !d.Analysis.Derived.TaskFamilyAmbiguous || len(d.Analysis.Derived.TaskFamilies) != 2 {
		t.Fatalf("derived %+v", d.Analysis.Derived)
	}
	if !excluded(d, "cheap-cli") {
		t.Fatalf("cheap-cli fails the writing rule and must be excluded: %+v", d.ExcludedCandidates)
	}
	if d.Recommendation.ProfileID != "mid-cli" {
		t.Fatalf("rec %s", d.Recommendation.ProfileID)
	}
}

func TestMeasuredRuleOutcomes(t *testing.T) {
	base := func() Inputs {
		in := inputs()
		in.Catalog.Models["cheap"] = withMeasurement(in.Catalog.Models["cheap"], Measurement{Source: "s", Metric: "repo.resolve", MetricVersion: "1", Value: 0.8, HigherIsBetter: true})
		in.Policy.AdequacyRules = []AdequacyRule{{ID: "r", TaskFamily: "software_change", ReasoningLevels: []int{3}, Metric: "repo.resolve", MetricVersion: "1", Minimum: 0.7, ProfileMatch: "any", OnMissing: "provisional_only"}}
		return in
	}
	a := assessment(peaked(3, 5), ContractCodeEdit)
	// Measured pass beats the prior: cheap is adequate at level 3 through evidence.
	d := Recommend(task(Constraints{}), a, base())
	if d.Recommendation.ProfileID != "cheap-cli" || d.Recommendation.AdequacyBasis != BasisMeasured {
		t.Fatalf("rec %+v", d.Recommendation)
	}
	// Unverified mapping still keeps it provisional.
	if d.Status != StatusProvisional {
		t.Fatalf("status %s", d.Status)
	}
	// Measured fail excludes even with a good prior.
	in := base()
	in.Catalog.Models["strong"] = withMeasurement(in.Catalog.Models["strong"], Measurement{Source: "s", Metric: "repo.resolve", MetricVersion: "1", Value: 0.1, HigherIsBetter: true})
	d = Recommend(task(Constraints{Policy: ModeQuality}), a, in)
	if !excluded(d, "strong-cli") {
		t.Fatalf("measured failure must exclude: %+v", d.ExcludedCandidates)
	}
	// on_missing=exclude removes unmeasured profiles; on_missing=unknown makes them conditional.
	in = base()
	in.Policy.AdequacyRules[0].OnMissing = "exclude"
	d = Recommend(task(Constraints{}), a, in)
	if !excluded(d, "mid-cli") || d.Recommendation.ProfileID != "cheap-cli" {
		t.Fatalf("exclude: %+v %+v", d.Recommendation, d.ExcludedCandidates)
	}
	in = base()
	in.Policy.AdequacyRules[0].OnMissing = "unknown"
	d = Recommend(task(Constraints{}), a, in)
	if !strings.Contains(roles(d), "conditional") {
		t.Fatalf("unknown adequacy should surface conditional alternatives: %s", roles(d))
	}
	// A measurement at the wrong effort does not satisfy an exact-match rule.
	in = base()
	in.Policy.AdequacyRules[0].ProfileMatch = "exact"
	in.Catalog.Models["mid"] = withMeasurement(in.Catalog.Models["mid"], Measurement{Source: "s", Metric: "repo.resolve", MetricVersion: "1", Value: 0.9, HigherIsBetter: true, Harness: "other-harness"})
	d = Recommend(task(Constraints{Policy: ModeQuality}), a, in)
	for _, alt := range append(d.Alternatives, *d.Recommendation) {
		if alt.ProfileID == "mid-cli" && alt.AdequacyBasis == BasisMeasured {
			t.Fatalf("harness-mismatched measurement must not count as measured")
		}
	}
}

func TestNoPriorNoRuleIsConditional(t *testing.T) {
	in := inputs()
	in.Profiles = append(in.Profiles, Profile{ID: "mystery", ModelID: "mid", AccessSurface: "cli-z", Enabled: true, Capabilities: []string{CapRepositoryTools}, EffortMode: EffortNone})
	d := Recommend(task(Constraints{}), assessment(peaked(1, 5), ContractCodeEdit), in)
	if d.Recommendation.ProfileID == "mystery" {
		t.Fatalf("unknown adequacy cannot win")
	}
	if !strings.Contains(roles(d), "conditional") {
		t.Fatalf("expected conditional alternative: %s", roles(d))
	}
}

func TestStableTies(t *testing.T) {
	in := inputs()
	in.Profiles = append(in.Profiles, Profile{ID: "a-twin", ModelID: "cheap", AccessSurface: "cli-b", Enabled: true, Capabilities: []string{CapRepositoryTools}, EffortMode: EffortNone, Prior: &Prior{QualityRank: 4, CostRank: 3}})
	d := Recommend(task(Constraints{}), assessment(peaked(0, 5), ContractCodeEdit), in)
	if d.Recommendation.ProfileID != "a-twin" {
		t.Fatalf("tie must resolve by stable ID: %s", d.Recommendation.ProfileID)
	}
}

func TestDeterministic(t *testing.T) {
	a := assessment(dist(0.2, 0.3, 0.3, 0.1, 0.1), ContractCodeEdit)
	x, _ := json.Marshal(Recommend(task(Constraints{}), a, inputs()))
	y, _ := json.Marshal(Recommend(task(Constraints{}), a, inputs()))
	if string(x) != string(y) {
		t.Fatalf("non-deterministic output")
	}
}

func TestNearestEffort(t *testing.T) {
	if got := nearestEffort("medium", []string{"low", "high"}); got != "high" {
		t.Fatalf("prefer stepping up: %s", got)
	}
	if got := nearestEffort("xhigh", []string{"low", "high"}); got != "high" {
		t.Fatalf("%s", got)
	}
	if got := nearestEffort("high", []string{"high"}); got != "high" {
		t.Fatalf("%s", got)
	}
}

func withMeasurement(m Model, ms Measurement) Model {
	m.Measurements = append(append([]Measurement{}, m.Measurements...), ms)
	return m
}

func roles(d Decision) string {
	var r []string
	for _, a := range d.Alternatives {
		r = append(r, a.Role)
	}
	return strings.Join(r, ",")
}

func excluded(d Decision, id string) bool {
	for _, e := range d.ExcludedCandidates {
		if e.ProfileID == id {
			return true
		}
	}
	return false
}

func hasWarning(d Decision, s string) bool { return containsSub(d.Warnings, s) }
func hasReason(d Decision, s string) bool  { return containsSub(d.DecisionReasons, s) }

func containsSub(list []string, s string) bool {
	for _, w := range list {
		if strings.Contains(w, s) {
			return true
		}
	}
	return false
}
