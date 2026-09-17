package router

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

// Recommend is deterministic: the same validated assessment, inputs, and
// clock produce the same Decision. It never performs I/O.
func Recommend(task Task, a Assessment, in Inputs) Decision {
	d := Decision{
		SchemaVersion:      SchemaVersion,
		Alternatives:       []Selection{},
		Constraints:        task.Constraints,
		DecisionReasons:    []string{},
		ExcludedCandidates: []Exclusion{},
		Warnings:           []string{},
		Analysis:           Analysis{Questions: a},
		Provenance: Provenance{
			GeneratedAt:      in.Now,
			ProgramVersion:   in.ProgramVersion,
			CatalogVersion:   in.Catalog.Version,
			CatalogHash:      in.CatalogHash,
			PolicyHash:       in.PolicyHash,
			QuestionsVersion: a.QuestionsVersion,
			QuestionsHash:    a.QuestionsHash,
			AnalyzerModel:    a.ReturnedModel,
			CapacityHash:     in.CapacityHash,
			CapacityUsed:     false,
			EvidenceBases:    []string{},
		},
	}
	pol := in.Policy
	c := task.Constraints

	if conflicts := conflicts(c, pol); len(conflicts) > 0 {
		d.Status = StatusConflict
		d.DecisionReasons = conflicts
		return d
	}
	if in.Capacity == nil {
		d.Warnings = append(d.Warnings, "Capacity was not considered: no snapshot supplied.")
	} else {
		d.Warnings = append(d.Warnings, "Capacity snapshot supplied but capacity-aware selection is not implemented yet; it did not affect this decision.")
	}

	der := &d.Analysis.Derived
	der.ReasoningQuantile = pol.UncertainReasoningQuantile

	// Missing context: the analyzer could not identify a job at all.
	if noul, ok := a.Nouls[QMissingContext]; ok && noul >= pol.MissingContextThreshold {
		der.MissingContext = true
		d.Status = StatusNeedsContext
		d.DecisionReasons = append(d.DecisionReasons, fmt.Sprintf("The task does not identify a concrete kind of work (missing-context probability %.2f, threshold %.2f). Describe the intended change and the relevant material.", noul, pol.MissingContextThreshold))
		return d
	}

	// Reasoning level from the full distribution.
	reasoning := a.Scores[QReasoning]
	meanLevel := int(math.Round(reasoning.Mean))
	level := quantileLevel(reasoning.Probabilities, pol.UncertainReasoningQuantile)
	der.ReasoningMeanLevel = meanLevel
	if level != meanLevel {
		d.DecisionReasons = append(d.DecisionReasons, fmt.Sprintf("Reasoning distribution spans levels; the %.0f%% upper quantile (level %d) sets the floor instead of the rounded mean (level %d).", pol.UncertainReasoningQuantile*100, level, meanLevel))
	}

	// Mode: explicit override, then a live responsiveness reading, then config.
	mode, modeSource := resolveMode(c, a, pol)
	der.Mode, der.ModeSource = mode, modeSource
	if mode == ModeRelaxed {
		before := level
		level = max(0, level-pol.RelaxedLevelReduction)
		if level != before {
			der.FloorAdjustedBy = "relaxed"
			d.DecisionReasons = append(d.DecisionReasons, fmt.Sprintf("Relaxed mode lowered the adequacy floor from level %d to level %d.", before, level))
		}
	}

	// Consequence can raise the floor and always shapes review guidance.
	consequence := a.Scores[QConsequence]
	highConsequence := quantileLevelF(consequence.Probabilities, pol.UncertainReasoningQuantile) >= pol.ConsequenceReviewThreshold || consequence.Mean >= pol.ConsequenceReviewThreshold
	if highConsequence && pol.HighConsequenceMinLevel != nil && level < *pol.HighConsequenceMinLevel {
		level = *pol.HighConsequenceMinLevel
		der.FloorAdjustedBy = "high_consequence"
		d.DecisionReasons = append(d.DecisionReasons, fmt.Sprintf("High consequence raised the adequacy floor to the configured level %d.", level))
	}
	der.ReasoningLevel = level

	if v, ok := a.Choices[QVerification]; ok {
		der.Verification = v.Choice
	}
	der.ReviewGuidance = reviewGuidance(der.Verification, highConsequence)

	if w, ok := a.Scores[QWorkload]; ok && w.Mean >= pol.LargeWorkloadThreshold {
		der.LargeWorkload = true
		d.Warnings = append(d.Warnings, "Large workload: whether the whole job fits in any capacity or session is uncertain.")
	}

	// Task families, output contract, capabilities.
	der.TaskFamilies, der.TaskFamilyAmbiguous = taskFamilies(c, a, pol)
	if der.TaskFamilyAmbiguous {
		d.DecisionReasons = append(d.DecisionReasons, fmt.Sprintf("Task family is ambiguous between %s; adequacy is required under both.", strings.Join(der.TaskFamilies, " and ")))
	}
	der.OutputContract = outputContract(c, a)
	if der.OutputContract == "" {
		d.Warnings = append(d.Warnings, "Output contract could not be determined; no contract gate was applied.")
	}
	der.RequiredCaps = requiredCapabilities(c, a, pol)
	der.EffortIntent = effortIntent(pol, level)
	if c.Effort != "" {
		der.EffortIntent = c.Effort
	}

	// Candidate gating: hard constraints and capabilities.
	var candidates []cand
	for _, p := range in.Profiles {
		if reason := gate(p, in.Catalog, c, der); reason != "" {
			d.ExcludedCandidates = append(d.ExcludedCandidates, Exclusion{ProfileID: p.ID, Reason: reason})
			continue
		}
		candidates = append(candidates, cand{p: p, m: in.Catalog.Models[p.ModelID]})
	}
	sortExclusions(d.ExcludedCandidates)
	if len(candidates) == 0 {
		d.Status = StatusNoMatch
		d.DecisionReasons = append(d.DecisionReasons, "No enabled profile satisfies the explicit constraints, output contract, and required capabilities.")
		return d
	}

	// Adequacy.
	var adequate, unknown []cand
	for i := range candidates {
		cd := &candidates[i]
		cd.adequacy, cd.basis, cd.basisDetail = adequacy(*cd, der, pol)
		switch cd.adequacy {
		case adequateYes:
			adequate = append(adequate, *cd)
		case adequateUnknown:
			unknown = append(unknown, *cd)
		default:
			d.ExcludedCandidates = append(d.ExcludedCandidates, Exclusion{ProfileID: cd.p.ID, Reason: "Below the adequacy floor for reasoning level " + fmt.Sprint(level) + " (" + cd.basisDetail + ")."})
		}
	}
	sortExclusions(d.ExcludedCandidates)
	if len(adequate) == 0 {
		d.Status = StatusNoMatch
		d.DecisionReasons = append(d.DecisionReasons, fmt.Sprintf("No candidate meets the adequacy floor at reasoning level %d.", level))
		for _, u := range unknown {
			d.Alternatives = append(d.Alternatives, selection(u, "conditional", der, "Adequacy unknown: "+u.basisDetail))
		}
		return d
	}

	// Latency handling: an explicit require target with only class evidence
	// cannot be verified. Say so, and keep going as a preference.
	if c.Latency != nil && c.Latency.Mode == LatencyRequire {
		d.Warnings = append(d.Warnings, fmt.Sprintf("Required %d ms %s target cannot be verified: only latency classes (priors) are available. Fastest adequate class selected.", c.Latency.TargetMS, c.Latency.Milestone))
	}

	// Objective, then cost and stable ID.
	ordered := order(adequate, mode)
	winner := ordered[0]
	d.Recommendation = ptr(selection(winner, "recommended", der, winnerReason(winner, mode, level)))
	d.Provenance.EvidenceBases = append(d.Provenance.EvidenceBases, winner.basis)

	// Alternatives.
	if level > meanLevel {
		// The cheaper adjacent alternative when uncertainty raised the floor.
		lower := *der
		lower.ReasoningLevel = meanLevel
		var atMean []cand
		for _, cd := range candidates {
			if aq, _, _ := adequacy(cd, &lower, pol); aq == adequateYes && cd.p.ID != winner.p.ID {
				atMean = append(atMean, cd)
			}
		}
		if len(atMean) > 0 {
			cheap := order(atMean, ModeAdequate)[0]
			if cheaperThan(cheap, winner) {
				d.Alternatives = append(d.Alternatives, selection(cheap, "cheaper", der, fmt.Sprintf("Adequate if the task is really level %d (the rounded mean); cheaper by the configured order.", meanLevel)))
			}
		}
	}
	if mode != ModeQuality {
		strong := order(adequate, ModeQuality)[0]
		if strong.p.ID != winner.p.ID && strongerThan(strong, winner) {
			d.Alternatives = append(d.Alternatives, selection(strong, "stronger", der, "Higher quality prior; higher expected resource use."))
		}
	}
	if mode != ModeFast {
		fast := order(adequate, ModeFast)[0]
		if fast.p.ID != winner.p.ID && latencyRank(fast.p.LatencyClass) < latencyRank(winner.p.LatencyClass) {
			d.Alternatives = append(d.Alternatives, selection(fast, "faster", der, "Faster latency class (a prior, not a measurement)."))
		}
	}
	for _, u := range unknown {
		if len(d.Alternatives) >= 4 {
			break
		}
		d.Alternatives = append(d.Alternatives, selection(u, "conditional", der, "Adequacy unknown: "+u.basisDetail))
	}

	// Status.
	d.Status = StatusRecommended
	if winner.basis != BasisMeasured {
		d.Status = StatusProvisional
		d.DecisionReasons = append(d.DecisionReasons, "Provisional: the adequacy floor rests on an operator prior, not measured task-family evidence.")
	}
	if !winner.p.MappingVerified {
		d.Status = StatusProvisional
		d.Warnings = append(d.Warnings, "Native model/effort mapping for "+winner.p.ID+" is unverified; no executable command is offered.")
	}
	if c.Latency != nil && c.Latency.Mode == LatencyRequire {
		d.Status = StatusProvisional
	}
	return d
}

type adequacyState int

const (
	adequateUnknown adequacyState = iota
	adequateYes
	adequateNo
)

type cand struct {
	p           Profile
	m           Model
	adequacy    adequacyState
	basis       string
	basisDetail string
}

func ptr[T any](v T) *T { return &v }

func conflicts(c Constraints, pol Policy) []string {
	var out []string
	for _, id := range c.AllowedProfiles {
		if slices.Contains(c.ExcludedProfiles, id) {
			out = append(out, "Profile "+id+" is both allowed and excluded.")
		}
	}
	if c.Policy != "" && !slices.Contains([]string{ModeAdequate, ModeQuality, ModeFast, ModeRelaxed}, c.Policy) {
		out = append(out, "Unknown policy mode "+c.Policy+".")
	}
	if c.Effort != "" && !slices.Contains(EffortLadder, c.Effort) {
		out = append(out, "Unknown effort "+c.Effort+".")
	}
	if c.Latency != nil {
		if c.Latency.TargetMS <= 0 {
			out = append(out, "Latency target must be positive.")
		}
		if c.Latency.Mode != LatencyPrefer && c.Latency.Mode != LatencyRequire {
			out = append(out, "Latency mode must be prefer or require.")
		}
		if c.Latency.Milestone != MilestoneFirstUseful && c.Latency.Milestone != MilestoneComplete {
			out = append(out, "Latency milestone must be first_useful_response or complete_response.")
		}
		if c.Policy == ModeQuality && c.Latency.Mode == LatencyRequire {
			out = append(out, "A quality policy and a required latency target conflict; choose one or use a latency preference.")
		}
	}
	_ = pol
	return out
}

// quantileLevel returns the smallest level whose cumulative probability
// reaches q. A peaked distribution returns its peak.
func quantileLevel(probs []float64, q float64) int {
	cum := 0.0
	for i, p := range probs {
		cum += p
		if cum >= q-1e-9 {
			return i
		}
	}
	return max(0, len(probs)-1)
}

func quantileLevelF(probs []float64, q float64) float64 {
	if len(probs) == 0 {
		return 0
	}
	return float64(quantileLevel(probs, q))
}

func resolveMode(c Constraints, a Assessment, pol Policy) (string, string) {
	if c.Policy != "" {
		return c.Policy, "explicit"
	}
	if c.Latency != nil {
		return ModeFast, "latency_target"
	}
	if r, ok := a.Choices[QResponsiveness]; ok && r.Choice == ResponsivenessLive && r.Confidence >= 0.5 {
		return ModeFast, "responsiveness"
	}
	if pol.Mode == "" {
		return ModeAdequate, "default"
	}
	return pol.Mode, "config"
}

func reviewGuidance(verification string, highConsequence bool) string {
	if highConsequence {
		return "independent review and targeted verification"
	}
	switch verification {
	case "exact":
		return "exact comparison or deterministic test"
	case "tests_and_review":
		return "tests plus review"
	case "subjective_review":
		return "subjective review"
	}
	return "review"
}

func taskFamilies(c Constraints, a Assessment, pol Policy) ([]string, bool) {
	if c.TaskFamily != "" {
		return []string{c.TaskFamily}, false
	}
	ch, ok := a.Choices[QTaskFamily]
	if !ok || ch.Choice == "" || ch.Choice == FamilyOther {
		return []string{FamilyOther}, false
	}
	type kv struct {
		k string
		v float64
	}
	var ranked []kv
	for k, v := range ch.Probabilities {
		ranked = append(ranked, kv{k, v})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].v == ranked[j].v {
			return ranked[i].k < ranked[j].k
		}
		return ranked[i].v > ranked[j].v
	})
	if len(ranked) >= 2 && ranked[1].k != FamilyOther && ranked[0].v-ranked[1].v <= pol.TaskFamilyMargin {
		fams := []string{ranked[0].k, ranked[1].k}
		sort.Strings(fams)
		return fams, true
	}
	return []string{ch.Choice}, false
}

func outputContract(c Constraints, a Assessment) string {
	if c.OutputContract != "" {
		return c.OutputContract
	}
	if ch, ok := a.Choices[QOutputContract]; ok && ch.Choice != "" && ch.Choice != ContractOther {
		return ch.Choice
	}
	return ""
}

func requiredCapabilities(c Constraints, a Assessment, pol Policy) []string {
	set := map[string]bool{}
	for _, r := range c.RequiredCapabilities {
		set[r] = true
	}
	for q, cap := range map[string]string{QNeedsImageInput: CapImageInput, QNeedsWebRes: CapWebResearch, QNeedsRepoTools: CapRepositoryTools} {
		if v, ok := a.Nouls[q]; ok && v >= pol.CapabilityThreshold {
			set[cap] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func effortIntent(pol Policy, level int) string {
	if level >= 0 && level < len(pol.EffortByLevel) {
		return pol.EffortByLevel[level]
	}
	return ""
}

// gate returns a non-empty reason when the profile cannot be a candidate.
func gate(p Profile, cat Catalog, c Constraints, der *Derived) string {
	if !p.Enabled {
		return "Profile is disabled."
	}
	if len(c.AllowedProfiles) > 0 && !slices.Contains(c.AllowedProfiles, p.ID) {
		return "Not in the allowed profile list."
	}
	if slices.Contains(c.ExcludedProfiles, p.ID) {
		return "Explicitly excluded."
	}
	m, ok := cat.Models[p.ModelID]
	if !ok {
		return "Model " + p.ModelID + " is not in the catalog."
	}
	contracts := p.OutputContracts
	if len(contracts) == 0 {
		contracts = m.OutputContracts
	}
	if der.OutputContract != "" && !slices.Contains(contracts, der.OutputContract) {
		return "Cannot deliver output contract " + der.OutputContract + "."
	}
	for _, cap := range der.RequiredCaps {
		if !slices.Contains(p.Capabilities, cap) {
			return "Lacks required capability " + cap + "."
		}
	}
	if c.Effort != "" {
		switch p.EffortMode {
		case EffortFixed:
			if p.NativeEffort != c.Effort {
				return "Fixed native effort " + p.NativeEffort + " does not match the requested " + c.Effort + "."
			}
		case EffortConfigurable:
			if len(p.EffortOptions) > 0 && !slices.Contains(p.EffortOptions, c.Effort) {
				return "Effort " + c.Effort + " is not a supported native option."
			}
		default:
			return "Profile has no effort control; requested effort " + c.Effort + " cannot apply."
		}
	}
	return ""
}

// adequacy decides whether a candidate meets the floor. Measured rules win;
// operator priors are the labeled fallback; otherwise adequacy is unknown.
func adequacy(cd cand, der *Derived, pol Policy) (adequacyState, string, string) {
	level := der.ReasoningLevel
	decided := false
	measuredAll := true
	var details []string
	for _, fam := range der.TaskFamilies {
		for _, r := range pol.AdequacyRules {
			if r.TaskFamily != fam || !slices.Contains(r.ReasoningLevels, level) {
				continue
			}
			decided = true
			m, found := findMeasurement(cd, r)
			if !found {
				measuredAll = false
				switch r.OnMissing {
				case "exclude":
					return adequateNo, BasisUnknown, "rule " + r.ID + " has no matching measurement and excludes on missing"
				case "unknown":
					return adequateUnknown, BasisUnknown, "rule " + r.ID + " has no matching measurement"
				default: // provisional_only: fall through to prior
					details = append(details, "rule "+r.ID+" unmeasured")
				}
				continue
			}
			pass := m.Value >= r.Minimum
			if !m.HigherIsBetter {
				pass = m.Value <= r.Minimum
			}
			if !pass {
				return adequateNo, BasisMeasured, fmt.Sprintf("rule %s: %s %.3g vs minimum %.3g", r.ID, r.Metric, m.Value, r.Minimum)
			}
			details = append(details, fmt.Sprintf("rule %s: %s %.3g meets %.3g", r.ID, r.Metric, m.Value, r.Minimum))
		}
	}
	if decided && measuredAll {
		return adequateYes, BasisMeasured, strings.Join(details, "; ")
	}
	if cd.p.Prior != nil && len(pol.PriorMaxQualityRankByLevel) > level {
		maxRank := pol.PriorMaxQualityRankByLevel[level]
		detail := fmt.Sprintf("operator prior quality rank %d vs maximum %d for level %d", cd.p.Prior.QualityRank, maxRank, level)
		if len(details) > 0 {
			detail = strings.Join(details, "; ") + "; " + detail
		}
		if cd.p.Prior.QualityRank <= maxRank {
			return adequateYes, BasisOperatorPrior, detail
		}
		return adequateNo, BasisOperatorPrior, detail
	}
	return adequateUnknown, BasisUnknown, "no matching measured rule and no operator prior"
}

func findMeasurement(cd cand, r AdequacyRule) (Measurement, bool) {
	for _, m := range cd.m.Measurements {
		if m.Metric != r.Metric || m.MetricVersion != r.MetricVersion {
			continue
		}
		if m.TaskFamily != "" && m.TaskFamily != r.TaskFamily {
			continue
		}
		if r.ProfileMatch != "any" {
			if cd.p.EffortMode == EffortFixed && m.Effort != "" && m.Effort != cd.p.NativeEffort {
				continue
			}
			if m.Harness != "" && m.Harness != cd.p.AccessSurface {
				continue
			}
		}
		return m, true
	}
	return Measurement{}, false
}

func latencyRank(class string) int {
	i := slices.Index(LatencyClasses, class)
	if i < 0 {
		return len(LatencyClasses) // unknown last
	}
	return i
}

func qualityRank(cd cand) int {
	if cd.p.Prior == nil {
		return math.MaxInt32
	}
	return cd.p.Prior.QualityRank
}

func costRank(cd cand) int {
	if cd.p.Prior == nil {
		return -1 // unknown cost sorts after every known cost
	}
	return cd.p.Prior.CostRank
}

func cheaperThan(a, b cand) bool  { return costRank(a) > costRank(b) }
func strongerThan(a, b cand) bool { return qualityRank(a) < qualityRank(b) }

// order sorts by the objective, then cost (cheaper first), then stable ID.
func order(cs []cand, mode string) []cand {
	out := slices.Clone(cs)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch mode {
		case ModeQuality:
			if qualityRank(a) != qualityRank(b) {
				return qualityRank(a) < qualityRank(b)
			}
		case ModeFast:
			if latencyRank(a.p.LatencyClass) != latencyRank(b.p.LatencyClass) {
				return latencyRank(a.p.LatencyClass) < latencyRank(b.p.LatencyClass)
			}
		}
		if costRank(a) != costRank(b) {
			return costRank(a) > costRank(b)
		}
		return a.p.ID < b.p.ID
	})
	return out
}

func winnerReason(w cand, mode string, level int) string {
	switch mode {
	case ModeQuality:
		return fmt.Sprintf("Strongest quality prior among profiles adequate at reasoning level %d.", level)
	case ModeFast:
		return fmt.Sprintf("Fastest latency class among profiles adequate at reasoning level %d, then cheapest.", level)
	case ModeRelaxed:
		return fmt.Sprintf("Cheapest profile adequate at the relaxed reasoning level %d.", level)
	}
	return fmt.Sprintf("Cheapest profile adequate at reasoning level %d by the configured cost order.", level)
}

func selection(cd cand, role string, der *Derived, reason string) Selection {
	s := Selection{
		Role:                role,
		ProfileID:           cd.p.ID,
		ModelLabel:          labelFor(cd),
		AccessSurface:       cd.p.AccessSurface,
		OutputContract:      der.OutputContract,
		GenerationMechanism: cd.m.GenerationMechanism,
		MappingVerified:     cd.p.MappingVerified,
		LatencyClass:        cd.p.LatencyClass,
		AdequacyBasis:       cd.basis,
		Availability:        "configured",
		Reason:              reason,
	}
	if s.OutputContract == "" {
		s.OutputContract = "unspecified"
	}
	switch cd.p.EffortMode {
	case EffortFixed:
		s.EffortIntent = der.EffortIntent
		s.NativeEffort = ptr(cd.p.NativeEffort)
	case EffortConfigurable:
		s.EffortIntent = der.EffortIntent
		s.NativeEffort = ptr(nearestEffort(der.EffortIntent, cd.p.EffortOptions))
	default:
		s.EffortIntent = ""
		s.NativeEffort = nil
	}
	if !cd.p.MappingVerified {
		s.NativeEffort = nil
	}
	return s
}

func labelFor(cd cand) string {
	if cd.p.Label != "" {
		return cd.p.Label
	}
	if cd.m.Label != "" {
		return cd.m.Label
	}
	return cd.p.ModelID
}

// nearestEffort picks the supported option closest to the intent on the
// ladder, preferring the higher neighbour on a tie.
func nearestEffort(intent string, options []string) string {
	if len(options) == 0 || slices.Contains(options, intent) {
		return intent
	}
	want := slices.Index(EffortLadder, intent)
	best, bestDist := "", math.MaxInt32
	for _, o := range options {
		i := slices.Index(EffortLadder, o)
		if i < 0 {
			continue
		}
		dist := i - want
		if dist < 0 {
			dist = -dist*2 + 1 // prefer stepping up over stepping down
		}
		if dist < bestDist {
			best, bestDist = o, dist
		}
	}
	if best == "" {
		return options[0]
	}
	return best
}

func sortExclusions(ex []Exclusion) {
	sort.SliceStable(ex, func(i, j int) bool { return ex[i].ProfileID < ex[j].ProfileID })
}
