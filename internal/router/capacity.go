package router

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// PoolEvaluation is one pool's availability at a point in time.
type PoolEvaluation struct {
	PoolID         string       `json:"pool_id"`
	Availability   Availability `json:"availability"`
	Basis          string       `json:"basis"` // exact | estimated | unknown
	ObservedAt     *time.Time   `json:"observed_at"`
	AgeMinutes     *int         `json:"age_minutes"`
	Stale          bool         `json:"stale"`
	ExpiryEligible bool         `json:"expiry_eligible"`
	ExpiryAt       *time.Time   `json:"expiry_at"`
	Reasons        []string     `json:"reasons"`
}

// ProfileAvailability is a profile's availability across its bound pools.
type ProfileAvailability struct {
	ProfileID      string       `json:"profile_id"`
	Availability   Availability `json:"availability"`
	Basis          string       `json:"basis"`       // exact | estimated | unknown | none
	ObservedAt     *time.Time   `json:"observed_at"` // oldest observation among counted pools
	AgeMinutes     *int         `json:"age_minutes"`
	ExpiryEligible bool         `json:"expiry_eligible"`
	ExpiryAt       *time.Time   `json:"expiry_at"`
	Estimated      bool         `json:"estimated"`
	Reasons        []string     `json:"reasons"`
}

// CapacityEvaluation is the result of applying a snapshot to the declared
// topology at one instant. Used is true when at least one pool reached a
// known state, which is what Provenance.CapacityUsed reports.
type CapacityEvaluation struct {
	Pools    []PoolEvaluation               `json:"pools"`
	Profiles map[string]ProfileAvailability `json:"profiles"`
	Used     bool                           `json:"used"`
	Warnings []string                       `json:"warnings"`
}

// EvaluatePools is pure. It applies the snapshot to the operator's pools and
// profiles at now, following the plan's per-pool and per-profile rules.
func EvaluatePools(cap *Capacity, pools []Pool, profiles []Profile, pol CapacityPolicy, now time.Time) CapacityEvaluation {
	ev := CapacityEvaluation{Profiles: map[string]ProfileAvailability{}, Warnings: []string{}}
	states := map[string]PoolState{}
	if cap != nil {
		for _, ps := range cap.Pools {
			states[ps.ID] = ps
		}
	}
	byPool := map[string]PoolEvaluation{}
	var stale, estimatedIgnored, sourceErr, unverified []string
	for _, pool := range pools {
		pe := evaluatePool(cap, pool, states, pol, now)
		if pe.Stale {
			stale = append(stale, fmt.Sprintf("%s (observed %d minutes ago, maximum %.0f)", pool.ID, deref(pe.AgeMinutes), pol.MaxSnapshotAgeMinutes))
		}
		if slices.Contains(pe.Reasons, reasonEstimatedIgnored) {
			estimatedIgnored = append(estimatedIgnored, pool.ID)
		}
		if strings.HasPrefix(firstOr(pe.Reasons), "source status ") {
			sourceErr = append(sourceErr, firstOr(pe.Reasons))
		}
		if slices.Contains(pe.Reasons, reasonUnverified) {
			unverified = append(unverified, pool.ID)
		}
		if pe.Availability == Available || pe.Availability == Exhausted {
			ev.Used = true
		}
		byPool[pool.ID] = pe
		ev.Pools = append(ev.Pools, pe)
	}
	sort.Slice(ev.Pools, func(i, j int) bool { return ev.Pools[i].PoolID < ev.Pools[j].PoolID })
	if len(stale) > 0 {
		ev.Warnings = append(ev.Warnings, "Capacity snapshot is stale for pools "+strings.Join(stale, ", ")+".")
	}
	for _, id := range estimatedIgnored {
		ev.Warnings = append(ev.Warnings, "Pool "+id+" reports estimated measurements; ignored because allow_estimated_measurements is off.")
	}
	for _, r := range sourceErr {
		ev.Warnings = append(ev.Warnings, "Pool "+r+"; its windows are unknown. This does not mean the harness is unavailable.")
	}
	for _, id := range unverified {
		ev.Warnings = append(ev.Warnings, "Pool "+id+" has an unverified binding; capacity was ignored for profiles bound to it.")
	}

	for _, p := range profiles {
		pa := ProfileAvailability{ProfileID: p.ID, Availability: Unknown, Basis: "none", Reasons: []string{}}
		if cap == nil {
			pa.Availability = NotConsidered
			pa.Reasons = append(pa.Reasons, "no snapshot")
			ev.Profiles[p.ID] = pa
			continue
		}
		if len(p.PoolIDs) == 0 {
			pa.Reasons = append(pa.Reasons, "no pool binding; capacity cannot apply")
			ev.Profiles[p.ID] = pa
			continue
		}
		allAvailable := true
		anyExhausted := false
		for _, pid := range p.PoolIDs {
			pe, ok := byPool[pid]
			if !ok {
				pa.Reasons = append(pa.Reasons, "pool "+pid+" is not declared")
				allAvailable = false
				continue
			}
			pa.Reasons = append(pa.Reasons, pid+": "+string(pe.Availability)+" ("+strings.Join(pe.Reasons, "; ")+")")
			switch pe.Availability {
			case Exhausted:
				anyExhausted = true
				allAvailable = false
			case Unknown:
				allAvailable = false
			default:
			}
			if pe.Availability != Unknown {
				if pe.Basis == MeasurementEstimated {
					pa.Estimated = true
				}
				if pe.ObservedAt != nil && (pa.ObservedAt == nil || pe.ObservedAt.Before(*pa.ObservedAt)) {
					t := *pe.ObservedAt
					pa.ObservedAt = &t
				}
			}
			if pe.ExpiryEligible && pe.ExpiryAt != nil && (pa.ExpiryAt == nil || pe.ExpiryAt.Before(*pa.ExpiryAt)) {
				t := *pe.ExpiryAt
				pa.ExpiryAt = &t
			}
		}
		switch {
		case anyExhausted:
			pa.Availability = Exhausted
		case allAvailable:
			pa.Availability = Available
		default:
			pa.Availability = Unknown
		}
		if pa.Availability != Unknown {
			pa.Basis = MeasurementExact
			if pa.Estimated {
				pa.Basis = MeasurementEstimated
			}
		} else {
			pa.Basis = MeasurementUnknown
			pa.ExpiryAt = nil
		}
		pa.ExpiryEligible = allAvailable && pa.ExpiryAt != nil
		if !pa.ExpiryEligible {
			pa.ExpiryAt = nil
		}
		if pa.ObservedAt != nil {
			age := int(math.Floor(now.Sub(*pa.ObservedAt).Minutes()))
			pa.AgeMinutes = &age
		}
		ev.Profiles[p.ID] = pa
	}
	return ev
}

const (
	reasonEstimatedIgnored = "estimated measurement, opt-in off"
	reasonUnverified       = "binding unverified"
)

func evaluatePool(cap *Capacity, pool Pool, states map[string]PoolState, pol CapacityPolicy, now time.Time) PoolEvaluation {
	pe := PoolEvaluation{PoolID: pool.ID, Availability: Unknown, Basis: MeasurementUnknown, Reasons: []string{}}
	if cap == nil {
		pe.Reasons = append(pe.Reasons, "no snapshot")
		return pe
	}
	ps, ok := states[pool.ID]
	if !ok {
		pe.Reasons = append(pe.Reasons, "pool absent from snapshot")
		return pe
	}
	if ps.ObservedAt != nil {
		t := *ps.ObservedAt
		pe.ObservedAt = &t
		age := int(math.Floor(now.Sub(t).Minutes()))
		pe.AgeMinutes = &age
	}
	if ps.SourceStatus != SourceOK {
		pe.Reasons = append(pe.Reasons, "source status "+ps.SourceStatus)
		return pe
	}
	if !pool.MappingVerified {
		pe.Reasons = append(pe.Reasons, reasonUnverified)
		return pe
	}
	if ps.ObservedAt == nil {
		pe.Reasons = append(pe.Reasons, "observation time unknown")
		return pe
	}
	limit := ps.ObservedAt.Add(time.Duration(pol.MaxSnapshotAgeMinutes * float64(time.Minute)))
	if ps.ValidUntil != nil && ps.ValidUntil.Before(limit) {
		limit = *ps.ValidUntil
	}
	if now.After(limit) {
		pe.Stale = true
		pe.Reasons = append(pe.Reasons, fmt.Sprintf("stale, observed %d minutes ago", deref(pe.AgeMinutes)))
		return pe
	}
	switch ps.Measurement {
	case MeasurementExact:
		pe.Basis = MeasurementExact
	case MeasurementEstimated:
		if !pol.AllowEstimatedMeasurements {
			pe.Reasons = append(pe.Reasons, reasonEstimatedIgnored)
			return pe
		}
		pe.Basis = MeasurementEstimated
	default:
		pe.Reasons = append(pe.Reasons, "measurement unknown")
		return pe
	}
	windows := map[string]WindowState{}
	for _, w := range ps.Windows {
		windows[w.ID] = w
	}
	exhausted, unknown := false, false
	for _, wid := range pool.RequiredWindowIDs {
		w, ok := windows[wid]
		switch {
		case !ok:
			unknown = true
			pe.Reasons = append(pe.Reasons, "required window "+wid+" absent")
		case w.RemainingPercent == nil:
			unknown = true
			pe.Reasons = append(pe.Reasons, "window "+wid+" unknown")
		case w.ResetsAt != nil && !w.ResetsAt.After(now):
			unknown = true
			pe.Reasons = append(pe.Reasons, "window "+wid+" reset boundary passed since observation")
		case *w.RemainingPercent == 0:
			exhausted = true
			pe.Reasons = append(pe.Reasons, "window "+wid+" exhausted")
		case *w.RemainingPercent < pol.ReservePercent:
			exhausted = true
			pe.Reasons = append(pe.Reasons, fmt.Sprintf("window %s below reserve (%.0f%% remaining)", wid, *w.RemainingPercent))
		default:
			pe.Reasons = append(pe.Reasons, fmt.Sprintf("window %s %.0f%% remaining", wid, *w.RemainingPercent))
		}
	}
	switch {
	case exhausted:
		pe.Availability = Exhausted
	case unknown:
		pe.Availability = Unknown
	default:
		pe.Availability = Available
	}
	if pe.Availability != Available || pool.MarginalCost != MarginalIncluded || !pol.PreferExpiringIncludedUsage {
		return pe
	}
	horizon := now.Add(time.Duration(pol.ExpiryHorizonHours * float64(time.Hour)))
	for _, wid := range pool.ExpiryPreferenceWindowIDs {
		w, ok := windows[wid]
		if !ok || w.RemainingPercent == nil || *w.RemainingPercent < pol.ReservePercent || w.ResetsAt == nil {
			continue
		}
		if w.ResetsAt.Before(now) || w.ResetsAt.After(horizon) {
			continue
		}
		if pe.ExpiryAt == nil || w.ResetsAt.Before(*pe.ExpiryAt) {
			t := *w.ResetsAt
			pe.ExpiryAt = &t
		}
	}
	if pe.ExpiryAt != nil {
		pe.ExpiryEligible = true
		pe.Reasons = append(pe.Reasons, fmt.Sprintf("included allowance resets in %s", humanDuration(pe.ExpiryAt.Sub(now))))
	}
	return pe
}

func deref(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func firstOr(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

func humanDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%d hours", int(math.Round(d.Hours())))
	}
	return fmt.Sprintf("%d days", int(math.Round(d.Hours()/24)))
}
