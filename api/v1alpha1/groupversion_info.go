/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package v1alpha1 contains API Schema definitions for the eviction-guard.io v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=eviction-guard.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	GroupName = "eviction-guard.io"
	Version   = "v1alpha1"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}

	// SchemeBuilder registers types for this group-version with a scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&EvictionGuardPolicy{},
		&EvictionGuardPolicyList{},
		&EvictionGuardWindow{},
		&EvictionGuardWindowList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
