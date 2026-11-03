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
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	tekv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	kapi "knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/konflux-ci/multi-platform-controller/pkg/reconciler/taskrun"
)

const (
	// errorSecretKey is the key the controller uses to explain an allocation
	// failure inside the TaskRun's SSH secret. The verification step prints it
	// and exits non-zero, which is how the failure reaches the TaskRun status.
	errorSecretKey = "error"

	// maxSecretMessageLength keeps a failure message readable when the
	// controller's explanation is verbose.
	maxSecretMessageLength = 512
)

// IsTaskRunTerminal reports whether the TaskRun reached a terminal state.
//
// A TaskRun is terminal once Tekton records a completionTime. It is also
// terminal when the Succeeded condition is no longer Unknown: the controller
// reports an allocation failure by writing an error secret that the
// verification step consumes before exiting non-zero, so waiting on
// completionTime alone can hide a failure that is already decided.
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

// DescribeFailureDiagnostics returns DescribeTaskRunState followed by the
// controller's own explanation of an allocation failure, when it wrote one to
// the multi-platform-ssh-<taskrun> secret. The secret exists even when the pod
// never started, which is exactly the case the E2E suite has to explain.
func DescribeFailureDiagnostics(ctx context.Context, c client.Client, tr *tekv1.TaskRun) string {
	state := DescribeTaskRunState(tr)
	if tr == nil || c == nil {
		return state
	}

	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: tr.Namespace, Name: taskrun.SecretPrefix + tr.Name}
	if err := c.Get(ctx, key, secret); err != nil {
		return state
	}

	message := strings.TrimSpace(string(secret.Data[errorSecretKey]))
	if message == "" {
		return state
	}
	return fmt.Sprintf("%s; ssh-secret-error=%q", state, truncate(message, maxSecretMessageLength))
}

// truncate caps the body of s at limit bytes and marks the cut with an
// ellipsis, so the result can exceed limit by the length of that ellipsis.
// The cut never splits a UTF-8 sequence, so the result stays valid text.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}

	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
