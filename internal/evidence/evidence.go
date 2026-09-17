// Package evidence defines the run-record contract. Recording is explicit:
// normal route results do not persist task text; --record does.
package evidence

import (
	"encoding/json"
	"time"

	"github.com/marcus/frost/internal/router"
)

// SchemaVersion is the version of the Record contract.
const SchemaVersion = 1

// Record is one analyzed task with everything needed to replay the policy
// without resending the text to a provider.
type Record struct {
	SchemaVersion  int               `json:"schema_version"`
	RecordedAt     time.Time         `json:"recorded_at"`
	ProgramVersion string            `json:"program_version"`
	CaseID         string            `json:"case_id,omitempty"`
	Task           router.Task       `json:"task"`
	Assessment     router.Assessment `json:"assessment"`
	RawResponse    json.RawMessage   `json:"raw_response,omitempty"`
	Decision       *router.Decision  `json:"decision,omitempty"`
	CatalogHash    string            `json:"catalog_hash,omitempty"`
	PolicyHash     string            `json:"policy_hash,omitempty"`
	CapacityHash   string            `json:"capacity_hash,omitempty"`
}

// Store persists records. One JSONL adapter suffices for now.
type Store interface {
	Append(Record) error
	Each(fn func(Record) error) error
}
