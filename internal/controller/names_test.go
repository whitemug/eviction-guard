/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"testing"
)

func TestWindowName(t *testing.T) {
	n := WindowName("spot", "app", "web")
	if n != "spot-app-web" {
		t.Fatalf("got %q", n)
	}
	long := WindowName("very-long-policy-name-that-exceeds", "very-long-namespace-name", "very-long-deployment-name-here")
	if len(long) > 63 {
		t.Fatalf("len=%d name=%s", len(long), long)
	}
}

func TestSortedKeys(t *testing.T) {
	got := sortedKeys(map[string]struct{}{"b": {}, "a": {}, "c": {}})
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("got %v", got)
	}
}
