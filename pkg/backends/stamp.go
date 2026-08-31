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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PatchAnnotations merges annotations onto any scaled object (Deployment, HPA, CR).
// Empty values delete the key. Capacity backends never write these.
func PatchAnnotations(ctx context.Context, c client.Client, t Target, anns map[string]string) error {
	if len(anns) == 0 {
		return nil
	}
	gvk, err := parseAPIVersionKind(t.APIVersion, t.Kind)
	if err != nil {
		return err
	}
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	if err := c.Get(ctx, t.ObjectKey, obj); err != nil {
		return fmt.Errorf("get %s %s: %w", gvk.String(), t.ObjectKey, err)
	}
	patchAnns := annotationPatch(obj, anns)
	if len(patchAnns) == 0 {
		return nil
	}
	raw, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{"annotations": patchAnns},
	})
	if err != nil {
		return err
	}
	if err := c.Patch(ctx, obj, client.RawPatch(types.MergePatchType, raw)); err != nil {
		return fmt.Errorf("stamp %s %s: %w", gvk.String(), t.ObjectKey, err)
	}
	return nil
}

func annotationPatch(obj *unstructured.Unstructured, want map[string]string) map[string]interface{} {
	if len(want) == 0 {
		return nil
	}
	existing, _, _ := unstructured.NestedStringMap(obj.Object, "metadata", "annotations")
	out := map[string]interface{}{}
	for k, v := range want {
		if v == "" {
			if _, had := existing[k]; had {
				out[k] = nil
			}
			continue
		}
		if existing[k] != v {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// StampDeletes is a PatchAnnotations map that removes the given keys.
func StampDeletes(keys []string) map[string]string {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = ""
	}
	return out
}
