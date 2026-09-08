/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/recorder"
)

// emitf records an events.k8s.io event (controller-runtime v0.25+ recorder).
// related is unused for our call sites; action mirrors reason for a stable reportingController action.
func emitf(r recorder.EventRecorder, obj runtime.Object, eventtype, reason, msg string, args ...interface{}) {
	if r == nil || obj == nil {
		return
	}
	r.Eventf(obj, nil, eventtype, reason, reason, msg, args...)
}
