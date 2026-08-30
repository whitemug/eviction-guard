/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package capacity implements Strategy A: target = baseline + spare, capped by maxBuffer.
package capacity

// TargetReplicas returns the replica count to scale to.
// atRisk is how many replicas of the workload currently sit on vulnerable nodes.
func TargetReplicas(baseline, atRisk, spare, maxBuffer int32) int32 {
	if baseline < 0 {
		baseline = 0
	}
	extra := atRisk
	if spare > extra {
		extra = spare
	}
	if extra < 1 && atRisk > 0 {
		extra = 1
	}
	if maxBuffer > 0 && extra > maxBuffer {
		extra = maxBuffer
	}
	return baseline + extra
}
