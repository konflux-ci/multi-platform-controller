/*
Copyright 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package common

import (
	"fmt"
	"strings"

	tekv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	kapi "knative.dev/pkg/apis"

	"github.com/konflux-ci/multi-platform-controller/pkg/reconciler/taskrun"
)

// IsTaskRunTerminal reports whether the TaskRun reached a terminal state.
//
// A TaskRun is terminal once Tekton records a completionTime. It is also
// terminal when the Succeeded condition is no longer Unknown: the controller
// fails an allocation by writing an error secret that the verification step
// consumes before exiting non-zero, so waiting on completionTime alone can hide
// a failure that is already decided.
func IsTaskRunTerminal(tr *tekv1.TaskRun) bool {
	if tr == nil {
		return false
	}
	if tr.Status.CompletionTime != nil {
		return true
	}
	condition := tr.Status.GetCondition(kapi.ConditionSucceeded)
	return condition != nil && condition.Status != corev1.ConditionUnknown
}

// DescribeTaskRunState renders the controller-visible state of a TaskRun on a
// single line. Use it as the optional description of a failed Eventually or
// Expect so that a timeout reports why the TaskRun did not complete (for
// example "timed out waiting for instance address") instead of only reporting
// that completionTime is nil.
//
// The controller's fuller explanation is logged when it creates the error
// secret (pkg/reconciler/taskrun/taskrun.go) and is collected with the rest of
// the e2e debug logs, so the test never reads that secret itself.
func DescribeTaskRunState(tr *tekv1.TaskRun) string {
	if tr == nil {
		return "TaskRun state unavailable: no object was observed"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "TaskRun %s/%s", tr.Namespace, tr.Name)

	if tr.Status.PodName != "" {
		fmt.Fprintf(&b, " pod=%s", tr.Status.PodName)
	}
	if instanceID := tr.Annotations[taskrun.CloudInstanceId]; instanceID != "" {
		fmt.Fprintf(&b, " cloud-instance-id=%s", instanceID)
	}
	if allocationStart := tr.Annotations[taskrun.AllocationStartTimeAnnotation]; allocationStart != "" {
		fmt.Fprintf(&b, " allocation-start-time=%s", allocationStart)
	}

	condition := tr.Status.GetCondition(kapi.ConditionSucceeded)
	if condition == nil {
		b.WriteString(": no Succeeded condition yet")
		return b.String()
	}

	fmt.Fprintf(&b, ": Succeeded=%s reason=%q message=%q", condition.Status, condition.Reason, condition.Message)
	return b.String()
}
