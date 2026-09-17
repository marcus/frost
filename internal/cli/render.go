package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/marcus/frost/internal/router"
)

// out, outln, and outf write to a CLI stream; a failed write to stdout or
// stderr has nowhere useful to be reported, so the error is dropped.
func out(w io.Writer, a ...any)                 { _, _ = fmt.Fprint(w, a...) }
func outln(w io.Writer, a ...any)               { _, _ = fmt.Fprintln(w, a...) }
func outf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

// RenderHuman writes the short recommendation with its limitations visible.
// Every line comes from the decision itself; nothing is invented here.
func RenderHuman(w io.Writer, d router.Decision) {
	switch d.Status {
	case router.StatusNeedsContext:
		outln(w, "Needs context")
	case router.StatusNoMatch:
		outln(w, "No match")
	case router.StatusConflict:
		outln(w, "Conflicting constraints")
	default:
		if d.Recommendation != nil {
			outln(w, headline(*d.Recommendation))
		}
	}
	for _, r := range d.DecisionReasons {
		outln(w, r)
	}
	if d.Recommendation != nil {
		der := d.Analysis.Derived
		outf(w, "Task: reasoning level %d of 4", der.ReasoningLevel)
		if len(der.TaskFamilies) > 0 {
			outf(w, ", %s", strings.Join(der.TaskFamilies, "/"))
		}
		if der.OutputContract != "" {
			outf(w, ", %s", der.OutputContract)
		}
		if der.Mode != router.ModeAdequate {
			outf(w, ", %s mode (%s)", der.Mode, der.ModeSource)
		}
		outln(w, ".")
		if d.Recommendation.Reason != "" {
			outln(w, d.Recommendation.Reason)
		}
		if der.ReviewGuidance != "" {
			outf(w, "Check the result with %s.\n", der.ReviewGuidance)
		}
	}
	for _, a := range d.Alternatives {
		outf(w, "%s: %s", strings.ToUpper(a.Role[:1])+a.Role[1:], headline(a))
		if a.Reason != "" {
			outf(w, " · %s", a.Reason)
		}
		outln(w)
	}
	for _, wn := range d.Warnings {
		outln(w, "Warning: "+wn)
	}
	outln(w, "Basis: "+basis(d))
}

func headline(s router.Selection) string {
	parts := []string{s.ModelLabel, s.AccessSurface}
	switch {
	case s.NativeEffort != nil && *s.NativeEffort != "":
		parts = append(parts, *s.NativeEffort+" effort")
	case s.EffortIntent != "":
		parts = append(parts, s.EffortIntent+" effort intent (native mapping unverified)")
	}
	return strings.Join(parts, " · ")
}

func basis(d router.Decision) string {
	var parts []string
	switch d.Status {
	case router.StatusRecommended:
		parts = append(parts, "measured task-family evidence")
	case router.StatusProvisional:
		if d.Recommendation != nil && d.Recommendation.AdequacyBasis == router.BasisOperatorPrior {
			parts = append(parts, "provisional, from operator priors")
		} else {
			parts = append(parts, "provisional")
		}
	default:
		parts = append(parts, d.Status)
	}
	if d.Provenance.CapacityUsed {
		parts = append(parts, "capacity snapshot considered")
	} else {
		parts = append(parts, "capacity not considered")
	}
	if d.Provenance.AnalyzerModel != "" {
		parts = append(parts, "analyzer "+d.Provenance.AnalyzerModel)
	}
	return strings.Join(parts, "; ") + "."
}
