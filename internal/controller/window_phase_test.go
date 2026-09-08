/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"testing"
	"time"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

func TestDecideWindowStep(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	until := now.Add(2 * time.Minute)
	past := now.Add(-time.Second)

	tests := []struct {
		name string
		in   windowPhaseInput
		want windowPhaseDecision
	}{
		{
			name: "at-risk keeps Open even when spare ready",
			in: windowPhaseInput{
				LiveAtRisk: 2, ForceCool: false, SafeReady: 3, Baseline: 3, Now: now,
			},
			want: windowPhaseDecision{
				Step: stepStayOpenVulnerable, Phase: egv1a1.WindowPhaseOpen,
				Message: msgNodesVulnerable, AbortCooldown: true,
			},
		},
		{
			name: "at-risk aborts armed cooldown",
			in: windowPhaseInput{
				LiveAtRisk: 1, ForceCool: false, SafeReady: 3, Baseline: 3,
				WindowUntil: until, Now: now,
			},
			want: windowPhaseDecision{
				Step: stepStayOpenVulnerable, Phase: egv1a1.WindowPhaseOpen,
				Message: msgNodesVulnerable, AbortCooldown: true,
			},
		},
		{
			name: "forceCool overrides at-risk and skips spare wait",
			in: windowPhaseInput{
				LiveAtRisk: 2, ForceCool: true, ForcedCool: true,
				SafeReady: 0, Baseline: 3, Now: now,
			},
			want: windowPhaseDecision{Step: stepBeginScaleBack},
		},
		{
			name: "waiting for spare Ready",
			in: windowPhaseInput{
				LiveAtRisk: 0, ForceCool: false, SafeReady: 1, Baseline: 3, Now: now,
			},
			want: windowPhaseDecision{
				Step: stepStayOpenWaitingSpare, Phase: egv1a1.WindowPhaseOpen,
				Message: msgWaitingSpare, AbortCooldown: true,
			},
		},
		{
			name: "spare ready begins scale-back",
			in: windowPhaseInput{
				LiveAtRisk: 0, ForceCool: false, SafeReady: 3, Baseline: 3, Now: now,
			},
			want: windowPhaseDecision{Step: stepBeginScaleBack},
		},
		{
			name: "forceCool begins scale-back without spare",
			in: windowPhaseInput{
				LiveAtRisk: 0, ForceCool: true, ForcedCool: true,
				SafeReady: 0, Baseline: 3, Now: now,
			},
			want: windowPhaseDecision{Step: stepBeginScaleBack},
		},
		{
			name: "stay Cooling until WindowUntil",
			in: windowPhaseInput{
				LiveAtRisk: 0, ForceCool: false, SafeReady: 3, Baseline: 3,
				WindowUntil: until, Now: now,
			},
			want: windowPhaseDecision{
				Step: stepStayCooling, Phase: egv1a1.WindowPhaseCooling, Message: reasonCooling,
			},
		},
		{
			name: "ForcedCool Cooling uses maxWindow message",
			in: windowPhaseInput{
				LiveAtRisk: 0, ForceCool: true, ForcedCool: true, SafeReady: 3, Baseline: 3,
				WindowUntil: until, Now: now,
			},
			want: windowPhaseDecision{
				Step: stepStayCooling, Phase: egv1a1.WindowPhaseCooling, Message: msgMaxWindow,
			},
		},
		{
			name: "cooldown elapsed closes and deletes",
			in: windowPhaseInput{
				LiveAtRisk: 0, ForceCool: false, ForcedCool: false, SafeReady: 3, Baseline: 3,
				WindowUntil: past, Now: now,
			},
			want: windowPhaseDecision{
				Step: stepClose, Phase: egv1a1.WindowPhaseClosed,
				Message: "scaled back to baseline", RetainClosed: false,
			},
		},
		{
			name: "ForcedCool close retains tombstone",
			in: windowPhaseInput{
				LiveAtRisk: 0, ForceCool: true, ForcedCool: true, SafeReady: 3, Baseline: 3,
				WindowUntil: past, Now: now,
			},
			want: windowPhaseDecision{
				Step: stepClose, Phase: egv1a1.WindowPhaseClosed,
				Message: "scaled back after maxWindow; holding until nodes clear", RetainClosed: true,
			},
		},
		{
			name: "exact WindowUntil boundary closes (not Before)",
			in: windowPhaseInput{
				LiveAtRisk: 0, SafeReady: 3, Baseline: 3,
				WindowUntil: now, Now: now,
			},
			want: windowPhaseDecision{
				Step: stepClose, Phase: egv1a1.WindowPhaseClosed,
				Message: "scaled back to baseline",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideWindowStep(tt.in)
			if got != tt.want {
				t.Fatalf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestCoolingMessage(t *testing.T) {
	if got := coolingMessage(false, false); got != reasonCooling {
		t.Fatalf("got %q", got)
	}
	if got := coolingMessage(true, false); got != msgMaxWindow {
		t.Fatalf("forceCool got %q", got)
	}
	if got := coolingMessage(false, true); got != msgMaxWindow {
		t.Fatalf("ForcedCool got %q", got)
	}
}
