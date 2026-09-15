/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import "time"

// Named requeue intervals (not flags). Keep magic numbers out of reconcile paths.
const (
	// requeueOpen is the default poll while a window stays Open (waiting for spare / nodes).
	requeueOpen = 15 * time.Second
	// requeueDeferred retries policy reconcile while workloads wait for a window slot.
	requeueDeferred = 15 * time.Second
	// requeuePolicyCleanup polls while owned windows finish deleting on policy deletion.
	requeuePolicyCleanup = 2 * time.Second
	// requeueForcedCoolTombstone polls Closed+ForcedCool windows until nodes clear.
	requeueForcedCoolTombstone = 15 * time.Second
)
