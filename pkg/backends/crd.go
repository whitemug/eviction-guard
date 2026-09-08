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
)

// fieldBackend patches an arbitrary integer field via unstructured merge-patch.
// Controllers use the package-level Current / ScaleUp / ScaleDown helpers.
type fieldBackend struct{}

func (b *fieldBackend) Current(ctx context.Context, c client.Client, t Target) (int32, error) {
	obj, path, err := b.get(ctx, c, t)
	if err != nil {
		return 0, err
	}
	v, ok, err := unstructured.NestedInt64(obj.Object, path...)
	if err != nil {
		return 0, err
	}
	if !ok {
		// Deployment.spec.replicas and HPA.spec.minReplicas default to 1 when unset.
		return 1, nil
	}
	return int32(v), nil
}

func (b *fieldBackend) ScaleUp(ctx context.Context, c client.Client, t Target, desired int32) error {
	return b.patch(ctx, c, t, desired)
}

func (b *fieldBackend) ScaleDown(ctx context.Context, c client.Client, t Target, baseline int32) error {
	return b.patch(ctx, c, t, baseline)
}

func (b *fieldBackend) PatchIntegers(ctx context.Context, c client.Client, base Target, paths []string, values map[string]int32) error {
	if len(paths) == 0 {
		return nil
	}
	if len(paths) == 1 {
		t := base
		t.FieldPath = paths[0]
		return b.patch(ctx, c, t, values[paths[0]])
	}
	obj, _, err := b.get(ctx, c, base)
	if err != nil {
		return err
	}
	need := false
	patch := map[string]interface{}{}
	for _, path := range paths {
		desired, ok := values[path]
		if !ok {
			continue
		}
		parts := splitFieldPath(path)
		cur, has, _ := unstructured.NestedInt64(obj.Object, parts...)
		if has && int32(cur) == desired {
			continue
		}
		need = true
		if err := setNested(patch, int64(desired), parts...); err != nil {
			return err
		}
	}
	if !need {
		return nil
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	if err := c.Patch(ctx, obj, client.RawPatch(types.MergePatchType, raw)); err != nil {
		return fmt.Errorf("patch %s %s: %w", obj.GroupVersionKind().String(), base.ObjectKey, err)
	}
	return nil
}

func (b *fieldBackend) get(ctx context.Context, c client.Client, t Target) (*unstructured.Unstructured, []string, error) {
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

func (b *fieldBackend) patch(ctx context.Context, c client.Client, t Target, desired int32) error {
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
