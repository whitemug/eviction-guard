/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package backends

import (
	"fmt"
	"sort"
	"strings"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

var (
	scaleUpOrder = map[egv1a1.ScaleBackendType]int{
		egv1a1.ScaleBackendDeployment: 0,
		egv1a1.ScaleBackendHPAMin:     1,
		egv1a1.ScaleBackendCRD:        2,
	}
	scaleDownOrder = map[egv1a1.ScaleBackendType]int{
		egv1a1.ScaleBackendHPAMin:     0,
		egv1a1.ScaleBackendDeployment: 1,
		egv1a1.ScaleBackendCRD:        2,
	}
)

// ParseList decodes eviction-guard.io/scale-backend (comma-separated).
func ParseList(raw string) ([]egv1a1.ScaleBackendType, error) {
	var out []egv1a1.ScaleBackendType
	seen := map[egv1a1.ScaleBackendType]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		t := egv1a1.ScaleBackendType(p)
		if _, err := Get(t); err != nil {
			return nil, fmt.Errorf("unknown scale backend %q", p)
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty scale-backend list")
	}
	return out, nil
}

// SortForScaleUp orders backends so pods are scheduled before the HPA floor
// is raised, then the CR field is updated.
func SortForScaleUp(in []egv1a1.ScaleBackendType) []egv1a1.ScaleBackendType {
	out := append([]egv1a1.ScaleBackendType(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		return orderOf(scaleUpOrder, out[i]) < orderOf(scaleUpOrder, out[j])
	})
	return out
}

// SortForScaleDown lowers the HPA floor before Deployment replicas so HPA
// cannot immediately fight the scale-back.
func SortForScaleDown(in []egv1a1.ScaleBackendType) []egv1a1.ScaleBackendType {
	out := append([]egv1a1.ScaleBackendType(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		return orderOf(scaleDownOrder, out[i]) < orderOf(scaleDownOrder, out[j])
	})
	return out
}

// SortActionsForScaleDown orders window actions for restore.
func SortActionsForScaleDown(in []egv1a1.ScaleAction) []egv1a1.ScaleAction {
	out := append([]egv1a1.ScaleAction(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		return orderOf(scaleDownOrder, out[i].Backend) < orderOf(scaleDownOrder, out[j].Backend)
	})
	return out
}

func orderOf(m map[egv1a1.ScaleBackendType]int, t egv1a1.ScaleBackendType) int {
	if n, ok := m[t]; ok {
		return n
	}
	return 10
}
