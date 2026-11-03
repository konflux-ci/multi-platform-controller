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
	"strings"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	tekv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kapi "knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mpctaskrun "github.com/konflux-ci/multi-platform-controller/pkg/reconciler/taskrun"
)

const (
	diagnosticsTestNamespace = "test-windows-c4xlarge-amd64"
	diagnosticsTestName      = "test-windows-c4xlarge-amd64wvlww"
	allocationTimeoutMessage = "timed out waiting for instance address"
)

// observedTaskRun returns the TaskRun the controller would reconcile for the
// Windows platform spec, without any status.
func observedTaskRun(annotations map[string]string) *tekv1.TaskRun {
	return &tekv1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   diagnosticsTestNamespace,
			Name:        diagnosticsTestName,
			Annotations: annotations,
		},
	}
}

// withSucceededCondition attaches a Succeeded condition to tr and returns it.
func withSucceededCondition(tr *tekv1.TaskRun, status corev1.ConditionStatus, reason, message string) *tekv1.TaskRun {
	tr.Status.Conditions = duckv1.Conditions{{
		Type:    kapi.ConditionSucceeded,
		Status:  status,
		Reason:  reason,
		Message: message,
	}}
	return tr
}

// clientWithErrorSecret returns a client seeded with the error secret the
// controller writes when host allocation fails.
func clientWithErrorSecret(tr *tekv1.TaskRun, message string) client.Client {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).Should(Succeed())

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: tr.Namespace,
			Name:      mpctaskrun.SecretPrefix + tr.Name,
		},
		Data: map[string][]byte{errorSecretKey: []byte(message)},
	}).Build()
}

// clientWithoutSecrets returns a client with an empty scheme-backed store.
func clientWithoutSecrets() client.Client {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).Should(Succeed())

	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

var _ = Describe("TaskRun failure diagnostics", func() {
	When("no TaskRun was observed", func() {
		It("should report that nothing was observed", func() {
			Expect(DescribeTaskRunState(nil)).Should(ContainSubstring("no object was observed"))
		})

		It("should not look for an error secret", func(ctx SpecContext) {
			Expect(DescribeFailureDiagnostics(ctx, nil, nil)).Should(ContainSubstring("no object was observed"))
		})
	})

	When("the TaskRun has no Succeeded condition yet", func() {
		It("should report its identity and the missing condition", func() {
			diagnostics := DescribeTaskRunState(observedTaskRun(nil))

			Expect(diagnostics).Should(ContainSubstring(diagnosticsTestNamespace + "/" + diagnosticsTestName))
			Expect(diagnostics).Should(ContainSubstring("no Succeeded condition yet"))
		})
	})

	When("the controller failed the allocation", func() {
		var failedTaskRun *tekv1.TaskRun

		BeforeEach(func() {
			failedTaskRun = withSucceededCondition(
				observedTaskRun(map[string]string{
					mpctaskrun.CloudInstanceId:               "i-0bf081f8b45efd606",
					mpctaskrun.AllocationStartTimeAnnotation: "1793643701",
				}),
				corev1.ConditionFalse,
				"Failed",
				allocationTimeoutMessage,
			)
			failedTaskRun.Status.PodName = diagnosticsTestName + "-pod"
		})

		It("should report the condition, the pod and the allocation annotations", func() {
			diagnostics := DescribeTaskRunState(failedTaskRun)

			Expect(diagnostics).Should(ContainSubstring("pod=" + diagnosticsTestName + "-pod"))
			Expect(diagnostics).Should(ContainSubstring("cloud-instance-id=i-0bf081f8b45efd606"))
			Expect(diagnostics).Should(ContainSubstring("allocation-start-time=1793643701"))
			Expect(diagnostics).Should(ContainSubstring(`Succeeded=False reason="Failed" message="` + allocationTimeoutMessage + `"`))
		})

		It("should append the controller's error secret message", func(ctx SpecContext) {
			diagnostics := DescribeFailureDiagnostics(ctx, clientWithErrorSecret(failedTaskRun, "Error allocating host: "+allocationTimeoutMessage), failedTaskRun)

			Expect(diagnostics).Should(ContainSubstring("ssh-secret-error="))
			Expect(diagnostics).Should(ContainSubstring(allocationTimeoutMessage))
		})

		It("should keep the TaskRun state when the controller wrote no secret", func(ctx SpecContext) {
			Expect(DescribeFailureDiagnostics(ctx, clientWithoutSecrets(), failedTaskRun)).Should(Equal(DescribeTaskRunState(failedTaskRun)))
		})

		It("should truncate a verbose error secret message", func(ctx SpecContext) {
			verbose := strings.Repeat("x", maxSecretMessageLength+64)

			diagnostics := DescribeFailureDiagnostics(ctx, clientWithErrorSecret(failedTaskRun, verbose), failedTaskRun)

			Expect(diagnostics).Should(ContainSubstring("…"))
			Expect(diagnostics).ShouldNot(ContainSubstring(verbose))
		})

		It("should not split a multi-byte character when truncating", func() {
			clipped := truncate("x"+strings.Repeat("é", 400), maxSecretMessageLength)

			Expect(utf8.ValidString(clipped)).Should(BeTrue())
			Expect(clipped).Should(HaveSuffix("…"))
			Expect(len(strings.TrimSuffix(clipped, "…"))).Should(BeNumerically("<=", maxSecretMessageLength))
		})
	})

	DescribeTable("terminal state detection",
		func(tr *tekv1.TaskRun, expected bool) {
			Expect(IsTaskRunTerminal(tr)).Should(Equal(expected))
		},
		Entry("no TaskRun was observed", nil, false),
		Entry("there is no completion time and no condition", observedTaskRun(nil), false),
		Entry("the Succeeded condition is still unknown",
			withSucceededCondition(observedTaskRun(nil), corev1.ConditionUnknown, "Pending", ""), false),
		Entry("the Succeeded condition reports a failure",
			withSucceededCondition(observedTaskRun(nil), corev1.ConditionFalse, "Failed", allocationTimeoutMessage), true),
		Entry("the Succeeded condition reports success",
			withSucceededCondition(observedTaskRun(nil), corev1.ConditionTrue, "Succeeded", ""), true),
		Entry("Tekton recorded a completion time", &tekv1.TaskRun{
			Status: tekv1.TaskRunStatus{
				TaskRunStatusFields: tekv1.TaskRunStatusFields{CompletionTime: &metav1.Time{Time: time.Now()}},
			},
		}, true),
	)
})
