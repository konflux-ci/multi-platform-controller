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

package taskrun

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/multi-platform-controller/test/e2e/common"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tekv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	kapi "knative.dev/pkg/apis"
)

// generateVerifyPlatformScript creates a bash script to verify platform execution
// with the expected architecture
func generateVerifyPlatformScript(expectedArch string) string {
	return `#!/bin/bash
set -e

if [ -e "/ssh/error" ]; then
	#no server could be provisioned
	cat /ssh/error
	exit 1
fi
SSH_ARGS="-o StrictHostKeyChecking=no -o ServerAliveInterval=60 -o ServerAliveCountMax=10"
SSH_HOST=$(cat /ssh/host)
mkdir -p ~/.ssh
curl --cacert /ssh/otp-ca -XPOST -d @/ssh/otp $(cat /ssh/otp-server) >~/.ssh/id_rsa
echo "" >> ~/.ssh/id_rsa
chmod 0400 ~/.ssh/id_rsa

OS=$(ssh $SSH_ARGS $SSH_HOST uname)
ARCH=$(ssh $SSH_ARGS $SSH_HOST uname -m)

if [ "$OS" != "Linux" ]; then
	echo "Expected OS to be Linux, got: $OS"
	exit 1
fi
if [ "$ARCH" != "` + expectedArch + `" ]; then
	echo "Expected architecture to be ` + expectedArch + `, got: $ARCH"
	exit 1
fi
echo "Platform verification successful: $OS/$ARCH"
`
}

// generateVerifyWindowsPlatformScript creates a bash script to verify Windows platform execution
// with the expected architecture
func generateVerifyWindowsPlatformScript(expectedArch string) string {
	return `#!/bin/bash
set -e

if [ -e "/ssh/error" ]; then
	#no server could be provisioned
	cat /ssh/error
	exit 1
fi

SSH_ARGS="-o StrictHostKeyChecking=no -o ServerAliveInterval=60 -o ServerAliveCountMax=10"
SSH_HOST=$(cat /ssh/host)
mkdir -p ~/.ssh
curl --cacert /ssh/otp-ca -XPOST -d @/ssh/otp $(cat /ssh/otp-server) >~/.ssh/id_rsa
echo "" >> ~/.ssh/id_rsa
chmod 0400 ~/.ssh/id_rsa

# On Windows, we use PowerShell to get OS and architecture
# Use $env:OS which returns "Windows_NT" on Windows (doesn't require WMI access)
# Use tr -d '\r' to strip Windows carriage returns from the output
OS=$(ssh $SSH_ARGS $SSH_HOST powershell -Command "\$env:OS" | tr -d '\r')
ARCH=$(ssh $SSH_ARGS $SSH_HOST powershell -Command "\$env:PROCESSOR_ARCHITECTURE" | tr -d '\r')

# $env:OS returns "Windows_NT" on Windows systems
if [ "$OS" != "Windows_NT" ]; then
	echo "Expected OS to be Windows_NT, got: $OS"
	exit 1
fi
# Windows reports architecture as "AMD64" for x86_64/amd64
# Accept both the expected architecture and "AMD64" (Windows standard)
if [ "$ARCH" != "` + expectedArch + `" ]; then
	if [ "$ARCH" != "AMD64" ]; then
		echo "Expected architecture to be ` + expectedArch + ` or AMD64, got: $ARCH"
		exit 1
	fi
fi
echo "Platform verification successful: $OS/$ARCH"
`
}

var _ = Describe("TaskRun execution", func() {
	var k8sClient client.Client
	testNamespace := "test-ns"
	testContext := &common.TestContext{}

	// Before running the tests, set up the environment by creating a test namespace
	// and creating a k8s client. The controller should already be deployed.
	BeforeEach(func(ctx context.Context) {
		By("Creating a k8s client")
		// The context provided by the callback is closed when it's completed,
		// so we need to create another context for the client.
		k8sClient = common.GetK8sClientOrDie(context.Background())

		By("Setting controller pod name", func() {
			Eventually(func(g Gomega) {
				podName := common.VerifyControllerPodRunning(g)
				testContext.SetControllerPodName(podName)
			}).Should(Succeed())
		})

		By("Creating a namespace: "+testNamespace, func() {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespace,
				},
			}
			err := k8sClient.Create(ctx, ns)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func(ctx context.Context) {
		common.CollectDebugInfo(ctx, k8sClient, testContext, testNamespace)
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	// runTaskRunPlatformTest is a helper function that runs a TaskRun test for a given platform
	runTaskRunPlatformTest := func(ctx context.Context, platform, expectedArch string, script string) {
		By("creating a TaskRun with PLATFORM parameter")
		platformNormalized := strings.ReplaceAll(platform, "/", "-")
		taskRun := &tekv1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-" + platformNormalized,
				Namespace:    testNamespace,
			},
			Spec: tekv1.TaskRunSpec{
				Params: []tekv1.Param{
					{
						Name:  "PLATFORM",
						Value: *tekv1.NewStructuredValues(platform),
					},
				},
				TaskSpec: &tekv1.TaskSpec{
					Params: []tekv1.ParamSpec{
						{
							Name: "PLATFORM",
							Type: tekv1.ParamTypeString,
						},
					},
					Steps: []tekv1.Step{
						{
							Name:   "verify-platform",
							Image:  "quay.io/konflux-ci/buildah-task:latest",
							Script: script,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "ssh",
									MountPath: "/ssh",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "ssh",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "multi-platform-ssh-$(context.taskRun.name)",
								},
							},
						},
					},
				},
			},
		}

		By("creating the TaskRun")
		Expect(k8sClient.Create(ctx, taskRun)).To(Succeed())

		By("waiting for the TaskRun to complete")
		Eventually(func(g Gomega) {
			key := client.ObjectKeyFromObject(taskRun)
			tr := &tekv1.TaskRun{}
			g.Expect(k8sClient.Get(ctx, key, tr)).To(Succeed())
			g.Expect(tr.Status.CompletionTime).ToNot(BeNil(), "TaskRun should have a completionTime")
		}, 10*time.Minute, 10*time.Second).Should(Succeed())

		By("verifying the TaskRun completed successfully")
		key := client.ObjectKeyFromObject(taskRun)
		tr := &tekv1.TaskRun{}
		Expect(k8sClient.Get(ctx, key, tr)).To(Succeed())
		condition := tr.Status.GetCondition(kapi.ConditionSucceeded)
		Expect(condition).ToNot(BeNil())
		Expect(condition.Status).To(Equal(corev1.ConditionTrue), "TaskRun should succeed")
		Expect(tr.Status.CompletionTime).ToNot(BeNil())
	}

	It("should successfully run a TaskRun on linux/amd64 platform", func(ctx context.Context) {
		runTaskRunPlatformTest(ctx, "linux/amd64", "x86_64", generateVerifyPlatformScript("x86_64"))
	})

	It("should successfully run a TaskRun on linux/arm64 platform", func(ctx context.Context) {
		runTaskRunPlatformTest(ctx, "linux/arm64", "aarch64", generateVerifyPlatformScript("aarch64"))
	})

	It("should successfully run a TaskRun on windows/c4xlarge-amd64 platform", func(ctx context.Context) {
		runTaskRunPlatformTest(ctx, "windows/c4xlarge-amd64", "AMD64", generateVerifyWindowsPlatformScript("AMD64"))
	})
})
