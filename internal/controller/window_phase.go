/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"time"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

// windowStep is the next action the window reconciler should take after evaluating
// live signals (at-risk pods, spare readiness, maxWindow, cooldown clock).
type windowStep int

const (
	// stepStayOpenVulnerable: at-risk pods remain and maxWindow has not forced cool.
	stepStayOpenVulnerable windowStep = iota
	// stepStayOpenWaitingSpare: no at-risk (or force-cool), but safe Ready < baseline.
	stepStayOpenWaitingSpare
	// stepBeginScaleBack: spare ready (or force-cool); WindowUntil unset — scale back then arm cooldown.
	stepBeginScaleBack
	// stepStayCooling: WindowUntil armed and not yet expired.
	stepStayCooling
	// stepClose: cooldown elapsed — mark Closed (retain if ForcedCool).
	stepClose
)

// windowPhaseInput is the pure snapshot used by decideWindowStep.
type windowPhaseInput struct {
	LiveAtRisk  int
	ForceCool   bool
	ForcedCool  bool // status already stamped (or just stamped this reconcile)
	SafeReady   int32
	Baseline    int32
	WindowUntil time.Time // zero if unset
	Now         time.Time
}

// windowPhaseDecision is the pure outcome of decideWindowStep (no API I/O).
type windowPhaseDecision struct {
	Step    windowStep
	Phase   egv1a1.WindowPhase
	Message string
	// AbortCooldown clears Spec.WindowUntil when returning to Open.
	AbortCooldown bool
	// RetainClosed keeps a Closed+ForcedCool tombstone until nodes clear.
	RetainClosed bool
}

// decideWindowStep encodes the Open → Cooling → Closed priority order.
func decideWindowStep(in windowPhaseInput) windowPhaseDecision {
	if in.LiveAtRisk > 0 && !in.ForceCool {
		return windowPhaseDecision{
			Step:          stepStayOpenVulnerable,
			Phase:         egv1a1.WindowPhaseOpen,
			Message:       msgNodesVulnerable,
			AbortCooldown: true,
		}
	}
	if !in.ForceCool && in.SafeReady < in.Baseline {
		return windowPhaseDecision{
			Step:          stepStayOpenWaitingSpare,
			Phase:         egv1a1.WindowPhaseOpen,
			Message:       msgWaitingSpare,
			AbortCooldown: true,
		}
	}
	if in.WindowUntil.IsZero() {
		return windowPhaseDecision{Step: stepBeginScaleBack}
	}
	if in.Now.Before(in.WindowUntil) {
		return windowPhaseDecision{
			Step:    stepStayCooling,
			Phase:   egv1a1.WindowPhaseCooling,
			Message: coolingMessage(in.ForceCool, in.ForcedCool),
		}
	}
	msg := "scaled back to baseline"
	retain := in.ForcedCool
	if retain {
		msg = "scaled back after maxWindow; holding until nodes clear"
	}
	return windowPhaseDecision{
		Step:         stepClose,
		Phase:        egv1a1.WindowPhaseClosed,
		Message:      msg,
		RetainClosed: retain,
	}
}

func coolingMessage(forceCool, forcedCool bool) string {
	if forceCool || forcedCool {
		return msgMaxWindow
	}
	return reasonCooling
}
