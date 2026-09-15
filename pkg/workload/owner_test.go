/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package workload_test

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/whitemug/eviction-guard/pkg/workload"
)

func TestOwnerDeployment(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: "d1"}}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-rs", Namespace: "app", UID: "r1",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "d1", Controller: ptr.To(true),
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-a", Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-rs", UID: "r1", Controller: ptr.To(true),
			}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep, rs, pod).Build()
	got, err := workload.OwnerDeployment(context.Background(), c, pod)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Name != "web" {
		t.Fatalf("got=%v, want web", got)
	}

	orphan := pod.DeepCopy()
	orphan.OwnerReferences = nil
	got, err = workload.OwnerDeployment(context.Background(), c, orphan)
	if err != nil || got != nil {
		t.Fatalf("orphan got=%v err=%v, want nil", got, err)
	}
}
