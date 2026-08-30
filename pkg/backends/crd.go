/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

// CRDBackend patches an arbitrary integer field on a custom resource
// (default spec.replicas). This is the primary extension point for other
// operators: they keep reconciling their CR; Eviction Guard only writes the count.
type CRDBackend struct{}

func (*CRDBackend) Name() egv1a1.ScaleBackendType { return egv1a1.ScaleBackendCRD }

func (b *CRDBackend) Current(ctx context.Context, c client.Client, t Target) (int32, error) {
	obj, path, err := b.get(ctx, c, t)
	if err != nil {
		return 0, err
	}
	v, ok, err := unstructured.NestedInt64(obj.Object, path...)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	return int32(v), nil
}

func (b *CRDBackend) ScaleUp(ctx context.Context, c client.Client, t Target, desired int32) error {
	return b.patch(ctx, c, t, desired)
}

func (b *CRDBackend) ScaleDown(ctx context.Context, c client.Client, t Target, baseline int32) error {
	return b.patch(ctx, c, t, baseline)
}

func (b *CRDBackend) get(ctx context.Context, c client.Client, t Target) (*unstructured.Unstructured, []string, error) {
	gvk, err := parseAPIVersionKind(t.APIVersion, t.Kind)
	if err != nil {
		return nil, nil, err
	}
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	if err := c.Get(ctx, t.ObjectKey, obj); err != nil {
		return nil, nil, fmt.Errorf("get %s %s: %w", gvk.String(), t.ObjectKey, err)
	}
	path := splitFieldPath(t.FieldPath)
	return obj, path, nil
}

func (b *CRDBackend) patch(ctx context.Context, c client.Client, t Target, desired int32) error {
	obj, path, err := b.get(ctx, c, t)
	if err != nil {
		return err
	}
	cur, ok, _ := unstructured.NestedInt64(obj.Object, path...)
	if ok && int32(cur) == desired {
		return nil
	}
	patch := map[string]interface{}{}
	if err := setNested(patch, int64(desired), path...); err != nil {
		return err
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	if err := c.Patch(ctx, obj, client.RawPatch(types.MergePatchType, raw)); err != nil {
		return fmt.Errorf("patch %s %s: %w", obj.GroupVersionKind().String(), t.ObjectKey, err)
	}
	return nil
}

func parseAPIVersionKind(apiVersion, kind string) (schema.GroupVersionKind, error) {
	if apiVersion == "" || kind == "" {
		return schema.GroupVersionKind{}, fmt.Errorf("scale target requires apiVersion and kind")
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	return gv.WithKind(kind), nil
}

func splitFieldPath(p string) []string {
	if p == "" {
		p = "spec.replicas"
	}
	p = strings.TrimPrefix(p, ".")
	return strings.Split(p, ".")
}

func setNested(obj map[string]interface{}, value interface{}, path ...string) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}
	if len(path) == 1 {
		obj[path[0]] = value
		return nil
	}
	next, ok := obj[path[0]].(map[string]interface{})
	if !ok || next == nil {
		next = map[string]interface{}{}
		obj[path[0]] = next
	}
	return setNested(next, value, path[1:]...)
}

// ParseScaleTarget decodes eviction-guard.io/scale-target.
// Accepted forms:
//   - "namespace/name" (same group as the workload)
//   - "group/version/namespaces/ns/kind/name"
func ParseScaleTarget(raw, defaultNS string) (Target, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Target{}, fmt.Errorf("empty scale-target")
	}
	parts := strings.Split(raw, "/")
	switch len(parts) {
	case 2:
		return Target{ObjectKey: client.ObjectKey{Namespace: parts[0], Name: parts[1]}}, nil
	case 1:
		return Target{ObjectKey: client.ObjectKey{Namespace: defaultNS, Name: parts[0]}}, nil
	case 6:
		// group/version/namespaces/ns/kind/name
		if parts[2] != "namespaces" {
			return Target{}, fmt.Errorf("invalid scale-target %q", raw)
		}
		return Target{
			ObjectKey:  client.ObjectKey{Namespace: parts[3], Name: parts[5]},
			APIVersion: parts[0] + "/" + parts[1],
			Kind:       parts[4],
		}, nil
	default:
		return Target{}, fmt.Errorf("invalid scale-target %q (want ns/name or group/version/namespaces/ns/kind/name)", raw)
	}
}
