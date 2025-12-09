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
	"os/exec"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck

	"github.com/konflux-ci/multi-platform-controller/test/utils"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega" //nolint:staticcheck
	tekv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// MPCNamespace where the project is deployed in
const MPCNamespace = "multi-platform-controller"

// TestContext holds test context information
type TestContext struct {
	ControllerPodName string
}

// GetControllerPodName returns the controller pod name
func (tc *TestContext) GetControllerPodName() string {
	return tc.ControllerPodName
}

// SetControllerPodName sets the controller pod name
func (tc *TestContext) SetControllerPodName(name string) {
	tc.ControllerPodName = name
}

// VerifyControllerPodRunning gets the controller-manager pod name and validates it's running.
// It returns the pod name.
func VerifyControllerPodRunning(g Gomega) string {
	// Get the name of the controller-manager pod
	cmd := exec.Command("kubectl", "get",
		"pods", "-l", "app=multi-platform-controller",
		"-o", "go-template={{ range .items }}"+
			"{{ if not .metadata.deletionTimestamp }}"+
			"{{ .metadata.name }}"+
			"{{ \"\\n\" }}{{ end }}{{ end }}",
		"-n", MPCNamespace,
	)

	podOutput, err := utils.Run(cmd)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve pod information")
	podNames := utils.GetNonEmptyLines(podOutput)
	g.Expect(podNames).To(HaveLen(1), "expected 1 pod running")
	podName := podNames[0]
	g.Expect(podName).To(ContainSubstring("multi-platform-controller"))

	// Validate the pod's status
	cmd = exec.Command("kubectl", "get",
		"pods", podName, "-o", "jsonpath={.status.phase}",
		"-n", MPCNamespace,
	)
	output, err := utils.Run(cmd)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(output).To(Equal("Running"), "Incorrect pod status")

	return podName
}

// DumpPodsLogs fetches and prints logs for pods in the given namespace
func DumpPodsLogs(nsName string, podNamesOutput string) {
	podNames := utils.GetNonEmptyLines(podNamesOutput)
	for _, podName := range podNames {
		By(fmt.Sprintf("Fetching logs for pod %s in test namespace", podName))
		cmd := exec.Command("kubectl", "logs", podName, "-n", nsName, "--all-containers=true")
		podLogs, err := utils.Run(cmd)
		if err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Logs for pod %s:\n%s\n", podName, podLogs)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get logs for pod %s: %s\n", podName, err)
		}
	}
}

// GetK8sClientOrDie creates a Kubernetes client or panics on failure
func GetK8sClientOrDie(ctx context.Context) client.Client {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(tekv1.AddToScheme(scheme))

	cfg := ctrl.GetConfigOrDie()

	k8sCache, err := cache.New(cfg, cache.Options{Scheme: scheme, ReaderFailOnMissingInformer: true})
	Expect(err).ToNot(HaveOccurred(), "failed to create cache")

	_, err = k8sCache.GetInformer(ctx, &tekv1.TaskRun{})
	Expect(err).ToNot(HaveOccurred(), "failed to setup informer for taskruns")

	go func() {
		if err := k8sCache.Start(ctx); err != nil {
			panic(err)
		}
	}()

	if synced := k8sCache.WaitForCacheSync(ctx); !synced {
		panic("failed waiting for cache to sync")
	}

	k8sClient, err := client.New(
		cfg,
		client.Options{
			Cache:  &client.CacheOptions{Reader: k8sCache},
			Scheme: scheme,
		},
	)
	Expect(err).ToNot(HaveOccurred(), "failed to create client")

	return k8sClient
}

// CollectDebugInfo collects debug information when a test fails
func CollectDebugInfo(testContext *TestContext, testNamespace string) {
	specReport := CurrentSpecReport()
	if specReport.Failed() {
		if testContext.ControllerPodName != "" {
			By(fmt.Sprintf("Fetching %s pod logs", testContext.ControllerPodName))
			cmd := exec.Command("kubectl", "logs", testContext.ControllerPodName, "-n", MPCNamespace)
			logs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "pod logs:\n %s", logs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get pod logs: %s", err)
			}

			By(fmt.Sprintf("Fetching %s description\n", testContext.ControllerPodName))
			cmd = exec.Command("kubectl", "describe", "pod", testContext.ControllerPodName, "-n", MPCNamespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Pod description: %s\n", podDescription)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to describe pod %s\n", testContext.ControllerPodName)
			}
		}

		By("Fetching Kubernetes events")
		cmd := exec.Command("kubectl", "get", "events", "-n", MPCNamespace, "--sort-by=.lastTimestamp")
		eventsOutput, err := utils.Run(cmd)
		if err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
		}

		By("Fetching TaskRuns")
		cmd = exec.Command("kubectl", "get", "-A", "-o", "yaml", "taskruns")
		taskruns, err := utils.Run(cmd)
		if err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "taskruns:\n %s", taskruns)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get taskruns: %s", err)
		}

		for _, ns := range []string{MPCNamespace, testNamespace} {
			By("Fetching Pods in " + ns + " namespace")
			cmd = exec.Command("kubectl", "get", "pods", "-n", ns, "-o", "yaml")
			pods, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "pods:\n %s", pods)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get pods: %s", err)
			}
		}

		By("Fetching logs of taskruns pods in the " + MPCNamespace + " namespace")
		cmd = exec.Command(
			"kubectl",
			"get",
			"pods",
			"-n",
			MPCNamespace,
			"-l",
			"app.kubernetes.io/managed-by=tekton-pipelines",
			"-o",
			"jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}",
		)
		podNamesOutput, err := utils.Run(cmd)
		if err == nil {
			DumpPodsLogs(MPCNamespace, podNamesOutput)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get pod names in %s namespace: %s", MPCNamespace, err)
		}

		By("Fetching logs of all pods in the " + testNamespace + " namespace")
		cmd = exec.Command("kubectl", "get", "pods", "-n", testNamespace, "-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
		podNamesOutput, err = utils.Run(cmd)
		if err == nil {
			DumpPodsLogs(testNamespace, podNamesOutput)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get pod names in %s namespace: %s", MPCNamespace, err)
		}
	}
}
