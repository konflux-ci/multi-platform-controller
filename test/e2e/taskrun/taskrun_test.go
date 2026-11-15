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
SSH_HOST=$(cat /ssh/host)
SSH_KEY="$HOME/.ssh/id_rsa"
echo "ssh key path: $SSH_KEY"
SSH_ARGS="-o StrictHostKeyChecking=no -o ServerAliveInterval=60 -o ServerAliveCountMax=10 -i $SSH_KEY -v"
mkdir -p "$(dirname "$SSH_KEY")"
curl --cacert /ssh/otp-ca -XPOST -d @/ssh/otp $(cat /ssh/otp-server) > "$SSH_KEY"
echo "" >> "$SSH_KEY"
chmod 0400 "$SSH_KEY"

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

SSH_HOST=$(cat /ssh/host)
SSH_KEY="$HOME/.ssh/id_rsa"
echo "ssh key path: $SSH_KEY"
SSH_ARGS="-o StrictHostKeyChecking=no -o ServerAliveInterval=60 -o ServerAliveCountMax=10 -i $SSH_KEY -v"
mkdir -p "$(dirname "$SSH_KEY")"
curl --cacert /ssh/otp-ca -XPOST -d @/ssh/otp $(cat /ssh/otp-server) > "$SSH_KEY"
echo "" >> "$SSH_KEY"
chmod 0400 "$SSH_KEY"

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

# A provisioned Windows host must be able to run containers: MPC opens
# port 22 only after the Docker setup completes.
DOCKER_RC=0
DOCKER_INFO=$(ssh $SSH_ARGS $SSH_HOST powershell -Command "docker info --format '{{.ServerVersion}}'" 2>&1) || DOCKER_RC=$?
DOCKER_INFO=$(printf '%s' "$DOCKER_INFO" | tr -d '\r')
if [ "$DOCKER_RC" -ne 0 ] || [ -z "$DOCKER_INFO" ]; then
	echo "Docker daemon is not usable: $DOCKER_INFO"
	exit 1
fi

DOCKER_RC=0
DOCKER_RUN=$(ssh $SSH_ARGS $SSH_HOST powershell -Command "docker run --rm mcr.microsoft.com/windows/nanoserver:ltsc2025 cmd /c echo docker-ok" 2>&1) || DOCKER_RC=$?
DOCKER_RUN=$(printf '%s' "$DOCKER_RUN" | tr -d '\r')
if [ "$DOCKER_RC" -ne 0 ] || ! printf '%s\n' "$DOCKER_RUN" | grep -q "docker-ok"; then
	echo "Docker could not run a container: $DOCKER_RUN"
	exit 1
fi
echo "Docker verification successful: server $DOCKER_INFO"

echo "Platform verification successful: $OS/$ARCH"
`
}

// taskRunWaitTimeout bounds how long a test waits for a TaskRun to reach a
// terminal state. It must exceed the longest dynamic.<platform>.allocation-timeout
// configured in deploy/overlays/dev-template/host-config.yaml, otherwise the test
// can give up before the controller reports why the allocation failed.
const taskRunWaitTimeout = 25 * time.Minute

var _ = Describe("TaskRun execution", func() {
	var k8sClient client.Client
	testNamespace := ""
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
		testNamespace = "test-" + strings.ReplaceAll(platform, "/", "-")

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
							Name: "verify-platform",
							// renovate: datasource=docker depName=task-runner packageName=quay.io/konflux-ci/task-runner
							Image:  "quay.io/konflux-ci/task-runner:2.1.0",
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
		var lastSeen *tekv1.TaskRun
		Eventually(func(g Gomega) {
			key := client.ObjectKeyFromObject(taskRun)
			tr := &tekv1.TaskRun{}
			g.Expect(k8sClient.Get(ctx, key, tr)).Should(Succeed())
			lastSeen = tr
			g.Expect(common.IsTaskRunTerminal(tr)).Should(BeTrue(), "TaskRun should reach a terminal state")
		}, taskRunWaitTimeout, 10*time.Second).Should(Succeed(), func() string {
			return common.DescribeTaskRunState(lastSeen)
		})

		By("verifying the TaskRun completed successfully")
		key := client.ObjectKeyFromObject(taskRun)
		tr := &tekv1.TaskRun{}
		Expect(k8sClient.Get(ctx, key, tr)).Should(Succeed())
		condition := tr.Status.GetCondition(kapi.ConditionSucceeded)
		Expect(condition).ShouldNot(BeNil())
		Expect(condition.Status).Should(Equal(corev1.ConditionTrue), "TaskRun should succeed: %s", common.DescribeTaskRunState(tr))
		Expect(tr.Status.CompletionTime).ShouldNot(BeNil())
	}

	It("should successfully run a TaskRun on linux/amd64 platform", func(ctx context.Context) {
		runTaskRunPlatformTest(ctx, "linux/amd64", "x86_64", generateVerifyPlatformScript("x86_64"))
	})

	It("should successfully run a TaskRun on linux/arm64 platform", func(ctx context.Context) {
		runTaskRunPlatformTest(ctx, "linux/arm64", "aarch64", generateVerifyPlatformScript("aarch64"))
	})

	// FlakeAttempts(2) retries this whole spec once, on any failure. The failure
	// mode it absorbs is a dynamic host that never becomes reachable within its
	// allocation timeout; the cost is that a Windows-only regression which fails
	// intermittently can pass on the second attempt. The Linux specs are not
	// retried, so a cross-platform regression still fails on the first attempt.
	// Worst case is 2 x taskRunWaitTimeout (25 min), inside the Ginkgo CLI's
	// default 1 h suite timeout.
	It("should successfully run a TaskRun on windows/c4xlarge-amd64 platform", FlakeAttempts(2), func(ctx context.Context) {
		runTaskRunPlatformTest(ctx, "windows/c4xlarge-amd64", "AMD64", generateVerifyWindowsPlatformScript("AMD64"))
	})
})
