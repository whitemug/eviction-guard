/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"sort"

	"github.com/whitemug/eviction-guard/pkg/naming"
)

// WindowName is a DNS-1123 name unique per (policy, workload).
func WindowName(policy, namespace, name string) string {
	return naming.WindowName(policy, namespace, name)
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
