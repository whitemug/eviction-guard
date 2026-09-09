/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package capacity_test

import (
	"testing"

	"github.com/whitemug/eviction-guard/pkg/capacity"
)

func TestTargetReplicas(t *testing.T) {
	tests := []struct {
		name                                     string
		baseline, atRisk, spare, maxBuffer, want int32
	}{
		{"one pod at risk", 3, 1, 1, 4, 4},
		{"spare larger than atRisk", 3, 1, 2, 4, 5},
		{"maxBuffer caps", 3, 10, 1, 2, 5},
		{"zero at-risk still uses spare if caller passes it", 3, 0, 1, 4, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := capacity.TargetReplicas(tc.baseline, tc.atRisk, tc.spare, tc.maxBuffer)
			if got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}
