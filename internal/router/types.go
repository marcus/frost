// Package router is Frost's shared selection core. It owns the domain types
// and the deterministic Recommend policy. Nothing in this package reads the
// environment, the filesystem, or the network; adapters in other packages
// produce these types and render the Decision.
package router

import "time"

// SchemaVersion is the version of the Decision JSON contract.
const SchemaVersion = 1

// Question IDs the policy understands. A question specification must supply
// answers under these IDs; the config check enforces that before a live call.
const (
	QReasoning       = "reasoning"         // score, 5 levels
	QWorkload        = "workload"          // score, 4 levels
	QConsequence     = "consequence"       // score, 4 levels
	QMissingContext  = "missing_context"   // noul
	QVerification    = "verification"      // choice
	QTaskFamily      = "task_family"       // choice, includes "other"
	QOutputContract  = "output_contract"   // choice, includes "other"
	QResponsiveness  = "responsiveness"    // choice, includes "unstated"
	QNeedsImageInput = "needs_image_input" // noul
	QNeedsWebRes     = "needs_web_research"
	QNeedsRepoTools  = "needs_repository_tools"
)

// Reasoning levels are rubric positions 0-4. The effort ladder and the prior
// adequacy table are indexed by level.
const ReasoningLevels = 5

// Domain outcomes.
const (
	StatusRecommended  = "recommended"
	StatusProvisional  = "provisional"
	StatusNeedsContext = "needs_context"
	StatusNoMatch      = "no_match"
	StatusConflict     = "conflict"
)

// Policy modes.
const (
	ModeAdequate = "adequate" // default: cheapest profile meeting the floor
	ModeQuality  = "quality"
	ModeFast     = "fast"
	ModeRelaxed  = "relaxed" // adequate with an explicitly lowered floor
)

// Output contracts a profile can deliver and a task can require.
const (
	ContractGeneratedText  = "generated_text"
	ContractCodeEdit       = "code_edit"
	ContractStructuredObj  = "structured_object"
	ContractTypedDecision  = "typed_decision"
	ContractOther          = "other"
	FamilyOther            = "other"
	ResponsivenessLive     = "live"
	ResponsivenessAttended = "attended"
	ResponsivenessUnattend = "unattended"
	ResponsivenessUnstated = "unstated"
	VerificationNeedsCtx   = "needs_context"
)

// Capabilities a profile declares and a task can require. The names double
// as the capability Noul question IDs with a needs_ prefix.
const (
	CapImageInput      = "image_input"
	CapWebResearch     = "web_research"
	CapRepositoryTools = "repository_tools"
)

// Effort modes a profile declares.
const (
	EffortFixed        = "fixed"
	EffortConfigurable = "configurable"
	EffortNone         = "none"
)

// EffortLadder orders effort intents from least to most.
var EffortLadder = []string{"low", "medium", "high", "xhigh"}

// Latency classes, fastest first. A class is a labeled prior, never a
// measurement.
var LatencyClasses = []string{"extra_fast", "fast", "medium", "slow"}

// Adequacy bases explain what a floor decision rested on.
const (
	BasisMeasured      = "measured"
	BasisOperatorPrior = "operator_prior"
	BasisUnknown       = "unknown"
)

// Latency target modes and milestones.
const (
	LatencyPrefer  = "prefer"
	LatencyRequire = "require"

	MilestoneFirstUseful = "first_useful_response"
	MilestoneComplete    = "complete_response"
)

// Task is the router's input after the CLI has resolved text, context, and
// explicit constraints. Constraint precedence is handled before this point.
type Task struct {
	Text        string      `json:"text"`
	Context     string      `json:"context,omitempty"`
	Constraints Constraints `json:"constraints"`
}

// Constraints are explicit, code-enforced requirements. They never come from
// probabilistic extraction.
type Constraints struct {
	AllowedProfiles      []string       `json:"allowed_profiles,omitempty"`
	ExcludedProfiles     []string       `json:"excluded_profiles,omitempty"`
	TaskFamily           string         `json:"task_family,omitempty"`
	OutputContract       string         `json:"output_contract,omitempty"`
	Policy               string         `json:"policy,omitempty"`
	RequiredCapabilities []string       `json:"required_capabilities,omitempty"`
	Effort               string         `json:"effort,omitempty"`
	Latency              *LatencyTarget `json:"latency,omitempty"`
}

// LatencyTarget is an explicit response-time constraint.
type LatencyTarget struct {
	TargetMS  int    `json:"target_ms"`
	Milestone string `json:"milestone"` // first_useful_response | complete_response
	Mode      string `json:"mode"`      // prefer | require
}

// Assessment is the vendor-neutral result of analyzing a task. Full
// distributions are retained; means are derived.
type Assessment struct {
	Provider         string                  `json:"provider"`
	RequestedModel   string                  `json:"requested_model"`
	ReturnedModel    string                  `json:"returned_model"`
	QuestionsVersion string                  `json:"questions_version"`
	QuestionsHash    string                  `json:"questions_hash"`
	Scores           map[string]ScoreAnswer  `json:"scores"`
	Nouls            map[string]float64      `json:"nouls"`
	Choices          map[string]ChoiceAnswer `json:"choices"`
	LatencyMS        int64                   `json:"latency_ms"`
	Usage            Usage                   `json:"usage"`
}

// ScoreAnswer is a distribution over ordered rubric levels.
type ScoreAnswer struct {
	Mean          float64   `json:"mean"`
	Probabilities []float64 `json:"probabilities"`
	Confidence    float64   `json:"confidence"`
	Legend        []string  `json:"legend,omitempty"`
}

// ChoiceAnswer is a distribution over named options.
type ChoiceAnswer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

// Usage is the analyzer's token accounting for one request.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Catalog is neutral, source-attributed model evidence.
type Catalog struct {
	Version string           `json:"version"`
	Models  map[string]Model `json:"models"`
}

// Model describes a model's identity, documented capabilities, and
// measurements. It says nothing about the operator's access.
type Model struct {
	ID                  string        `json:"id"`
	Label               string        `json:"label"`
	Aliases             []string      `json:"aliases,omitempty"`
	OutputContracts     []string      `json:"output_contracts"`
	InputModalities     []string      `json:"input_modalities,omitempty"`
	GenerationMechanism string        `json:"generation_mechanism,omitempty"`
	ContextTokens       int           `json:"context_tokens,omitempty"`
	Measurements        []Measurement `json:"measurements,omitempty"`
}

// Measurement is one source-attributed observation. Unknown fields stay
// empty; a rule that needs them treats the measurement as non-matching.
type Measurement struct {
	Source         string    `json:"source"`
	Metric         string    `json:"metric"`
	MetricVersion  string    `json:"metric_version"`
	Value          float64   `json:"value"`
	Unit           string    `json:"unit,omitempty"`
	HigherIsBetter bool      `json:"higher_is_better"`
	TaskFamily     string    `json:"task_family,omitempty"`
	Effort         string    `json:"effort,omitempty"`
	Harness        string    `json:"harness,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	ObservedAt     time.Time `json:"observed_at,omitempty"`
}

// Profile combines a model with actual operator access.
type Profile struct {
	ID              string   `json:"id"`
	Label           string   `json:"label,omitempty"`
	ModelID         string   `json:"model"`
	AccessSurface   string   `json:"access_surface"`
	Enabled         bool     `json:"enabled"`
	OutputContracts []string `json:"output_contracts,omitempty"` // overrides the model's when set
	Capabilities    []string `json:"capabilities,omitempty"`
	EffortMode      string   `json:"effort_mode"` // fixed | configurable | none
	NativeEffort    string   `json:"native_effort,omitempty"`
	EffortOptions   []string `json:"effort_options,omitempty"`
	MappingVerified bool     `json:"mapping_verified"`
	LatencyClass    string   `json:"latency_class,omitempty"` // prior
	PoolIDs         []string `json:"pool_ids,omitempty"`
	Prior           *Prior   `json:"prior,omitempty"`
}

// Prior is an operator's labeled, ordinal impression. Lower QualityRank is
// better; higher CostRank is cheaper. It is never measured performance.
type Prior struct {
	QualityRank int `json:"quality_rank"`
	CostRank    int `json:"cost_rank"`
}

// AdequacyRule declares a measured floor for a task family and reasoning band.
type AdequacyRule struct {
	ID              string  `json:"id"`
	TaskFamily      string  `json:"task_family"`
	ReasoningLevels []int   `json:"reasoning_levels"`
	Metric          string  `json:"metric"`
	MetricVersion   string  `json:"metric_version"`
	Minimum         float64 `json:"minimum"`
	ProfileMatch    string  `json:"profile_match"` // exact | any
	OnMissing       string  `json:"on_missing"`    // provisional_only | exclude | unknown
}

// Policy is the operator's resolved selection policy.
type Policy struct {
	Mode                       string         `json:"mode"`
	UncertainReasoningQuantile float64        `json:"uncertain_reasoning_quantile"`
	TaskFamilyMargin           float64        `json:"task_family_margin"`
	MissingContextThreshold    float64        `json:"missing_context_threshold"`
	CapabilityThreshold        float64        `json:"capability_threshold"`
	ConsequenceReviewThreshold float64        `json:"consequence_review_threshold"`
	HighConsequenceMinLevel    *int           `json:"high_consequence_min_level,omitempty"`
	LargeWorkloadThreshold     float64        `json:"large_workload_threshold"`
	EffortByLevel              []string       `json:"effort_by_level"`
	PriorMaxQualityRankByLevel []int          `json:"prior_max_quality_rank_by_level,omitempty"`
	RelaxedLevelReduction      int            `json:"relaxed_level_reduction"`
	AdequacyRules              []AdequacyRule `json:"adequacy_rules,omitempty"`
}

// DefaultPolicy returns the proposal defaults from the plan. They are not
// empirical optima.
func DefaultPolicy() Policy {
	return Policy{
		Mode:                       ModeAdequate,
		UncertainReasoningQuantile: 0.8,
		TaskFamilyMargin:           0.15,
		MissingContextThreshold:    0.65,
		CapabilityThreshold:        0.7,
		ConsequenceReviewThreshold: 1.5,
		LargeWorkloadThreshold:     2.5,
		EffortByLevel:              []string{"low", "medium", "high", "high", "xhigh"},
		RelaxedLevelReduction:      1,
	}
}

// Capacity is the neutral usage snapshot. Slice 2 fills it in; Recommend
// accepts nil and says capacity was not considered.
type Capacity struct {
	GeneratedAt time.Time `json:"generated_at"`
	Producer    string    `json:"producer,omitempty"`
}

// Decision is the complete result of one recommendation.
type Decision struct {
	SchemaVersion      int         `json:"schema_version"`
	Status             string      `json:"status"`
	Recommendation     *Selection  `json:"recommendation,omitempty"`
	Alternatives       []Selection `json:"alternatives"`
	Analysis           Analysis    `json:"analysis"`
	Constraints        Constraints `json:"constraints"`
	DecisionReasons    []string    `json:"decision_reasons"`
	ExcludedCandidates []Exclusion `json:"excluded_candidates"`
	Warnings           []string    `json:"warnings"`
	Provenance         Provenance  `json:"provenance"`
}

// Selection is a selected or alternative profile with its applicable effort.
type Selection struct {
	Role                string  `json:"role"` // recommended | cheaper | stronger | faster | conditional
	ProfileID           string  `json:"profile_id"`
	ModelLabel          string  `json:"model_label"`
	AccessSurface       string  `json:"access_surface"`
	OutputContract      string  `json:"output_contract"`
	GenerationMechanism string  `json:"generation_mechanism,omitempty"`
	EffortIntent        string  `json:"effort_intent,omitempty"`
	NativeEffort        *string `json:"native_effort"`
	MappingVerified     bool    `json:"mapping_verified"`
	LatencyClass        string  `json:"latency_class,omitempty"`
	AdequacyBasis       string  `json:"adequacy_basis"`
	Availability        string  `json:"availability"`
	Reason              string  `json:"reason,omitempty"`
}

// Exclusion records a candidate that was removed and why.
type Exclusion struct {
	ProfileID string `json:"profile_id"`
	Reason    string `json:"reason"`
}

// Analysis is the assessment plus the policy's derived reading of it.
type Analysis struct {
	Derived   Derived    `json:"derived"`
	Questions Assessment `json:"questions"`
}

// Derived is what the policy concluded from the distributions.
type Derived struct {
	ReasoningLevel      int      `json:"reasoning_level"`
	ReasoningMeanLevel  int      `json:"reasoning_mean_level"`
	ReasoningQuantile   float64  `json:"reasoning_quantile"`
	FloorAdjustedBy     string   `json:"floor_adjusted_by,omitempty"`
	TaskFamilies        []string `json:"task_families"`
	TaskFamilyAmbiguous bool     `json:"task_family_ambiguous"`
	OutputContract      string   `json:"output_contract"`
	RequiredCaps        []string `json:"required_capabilities"`
	EffortIntent        string   `json:"effort_intent"`
	Mode                string   `json:"mode"`
	ModeSource          string   `json:"mode_source"`
	Verification        string   `json:"verification"`
	ReviewGuidance      string   `json:"review_guidance"`
	LargeWorkload       bool     `json:"large_workload"`
	MissingContext      bool     `json:"missing_context"`
}

// Provenance identifies every input that shaped the decision.
type Provenance struct {
	GeneratedAt      time.Time `json:"generated_at"`
	ProgramVersion   string    `json:"program_version"`
	CatalogVersion   string    `json:"catalog_version"`
	CatalogHash      string    `json:"catalog_hash,omitempty"`
	PolicyHash       string    `json:"policy_hash,omitempty"`
	QuestionsVersion string    `json:"questions_version"`
	QuestionsHash    string    `json:"questions_hash"`
	AnalyzerModel    string    `json:"analyzer_model"`
	CapacityHash     string    `json:"capacity_hash,omitempty"`
	CapacityUsed     bool      `json:"capacity_used"`
	EvidenceBases    []string  `json:"evidence_bases"`
}

// Inputs bundles everything Recommend needs besides the task and assessment.
type Inputs struct {
	Catalog  Catalog
	Profiles []Profile
	Capacity *Capacity
	Policy   Policy
	Now      time.Time
	// Provenance fields the caller knows and the core does not.
	ProgramVersion string
	CatalogHash    string
	PolicyHash     string
	CapacityHash   string
}
