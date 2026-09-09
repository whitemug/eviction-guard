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

func windowOpenedAt(win *egv1a1.EvictionGuardWindow) time.Time {
	if win.Status.LastScaleTime != nil && !win.Status.LastScaleTime.IsZero() {
		return win.Status.LastScaleTime.Time
	}
	if !win.CreationTimestamp.Time.IsZero() {
		return win.CreationTimestamp.Time
	}
	return time.Time{}
}

func maxWindowExceeded(policy *egv1a1.EvictionGuardPolicy, win *egv1a1.EvictionGuardWindow, now time.Time) bool {
	if policy == nil || win == nil {
		return false
	}
	d := policy.MaxWindowOrDefault()
	if d <= 0 {
		return false
	}
	opened := windowOpenedAt(win)
	if opened.IsZero() {
		return false
	}
	return !now.Before(opened.Add(d))
}

func maxWindowRequeue(policy *egv1a1.EvictionGuardPolicy, win *egv1a1.EvictionGuardWindow, now time.Time) time.Duration {
	if policy == nil || win == nil {
		return 0
	}
	d := policy.MaxWindowOrDefault()
	if d <= 0 {
		return 0
	}
	opened := windowOpenedAt(win)
	if opened.IsZero() {
		return 0
	}
	left := opened.Add(d).Sub(now)
	if left <= 0 {
		return time.Second
	}
	return left
}

func holdMaxWindow(policy *egv1a1.EvictionGuardPolicy, win *egv1a1.EvictionGuardWindow, now time.Time) bool {
	if win == nil {
		return false
	}
	if win.Status.ForcedCool || win.Status.Phase == egv1a1.WindowPhaseClosed {
		return true
	}
	return maxWindowExceeded(policy, win, now)
}
