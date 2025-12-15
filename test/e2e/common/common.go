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
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck
	tekv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/konflux-ci/multi-platform-controller/test/utils"
)

// MPCNamespace where the project is deployed in
const MPCNamespace = "multi-platform-controller"

// DebugLogDirEnvVar is the environment variable name for the debug log directory
const DebugLogDirEnvVar = "E2E_DEBUG_LOG_DIR"

// TestContext holds test context information
type TestContext struct {
	ControllerPodName string
}

// PodsByStatus holds pod names categorized by their status
type PodsByStatus struct {
	// SucceededPods contains pods that completed successfully or are running and ready
	SucceededPods []types.NamespacedName
	// FailedPods contains pods that are not running/succeeded or not ready
	FailedPods []types.NamespacedName
	// ProblematicPodsDisplay contains formatted strings for display in summary
	ProblematicPodsDisplay []string
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
	cmd = exec.Command("kubectl", "get", //nolint:gosec // G204 - e2e test with controlled input
		"pods", podName, "-o", "jsonpath={.status.phase}",
		"-n", MPCNamespace,
	)
	output, err := utils.Run(cmd)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(output).To(Equal("Running"), "Incorrect pod status")

	return podName
}

// getDebugLogDir returns the debug log directory path.
// If the E2E_DEBUG_LOG_DIR environment variable is set, its value is returned.
// Otherwise, a temporary directory is created and its path is returned.
//
// Returns:
//   - string: the path to the debug log directory
//   - error: nil on success, or an error if temporary directory creation fails
func getDebugLogDir() (string, error) {
	logDir := os.Getenv(DebugLogDirEnvVar)
	if logDir != "" {
		return logDir, nil
	}
	// Create a temporary directory if env var is not set
	tempDir, err := os.MkdirTemp("", "e2e-debug-logs-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary log directory: %w", err)
	}
	return tempDir, nil
}

// writeToFile writes content to a file in the specified directory.
//
// Parameters:
//   - logDir: the directory path where the file will be created
//   - filename: the name of the file to create
//   - content: the content to write to the file
//
// Returns:
//   - error: nil on success, or an error if the file could not be written
func writeToFile(logDir, filename, content string) error {
	filePath := filepath.Join(logDir, filename)
	return os.WriteFile(filePath, []byte(content), 0600)
}

// collectControllerPodInfo collects controller pod logs and description.
// Files created: controller-pod-{name}.log, controller-pod-{name}-describe.txt
//
// Parameters:
//   - controllerPodName: the name of the controller pod; if empty, the function returns immediately
//   - logDir: the directory path where log files will be written
func collectControllerPodInfo(controllerPodName, logDir string) {
	if controllerPodName == "" {
		return
	}

	By(fmt.Sprintf("Fetching %s pod logs", controllerPodName))
	cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", MPCNamespace) //nolint:gosec // G204 - e2e test with controlled input
	logs, err := utils.Run(cmd)
	if err == nil {
		filename := fmt.Sprintf("controller-pod-%s.log", controllerPodName)
		_ = writeToFile(logDir, filename, logs)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get pod logs: %s\n", err)
	}

	By(fmt.Sprintf("Fetching %s description", controllerPodName))
	cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", MPCNamespace) //nolint:gosec // G204 - e2e test with controlled input
	podDescription, err := utils.Run(cmd)
	if err == nil {
		filename := fmt.Sprintf("controller-pod-%s-describe.txt", controllerPodName)
		_ = writeToFile(logDir, filename, podDescription)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to describe pod %s\n", controllerPodName)
	}
}

// collectEvents collects Kubernetes events from the MPC namespace.
// Files created: events-{namespace}.txt
//
// Parameters:
//   - logDir: the directory path where the events file will be written
func collectEvents(logDir string) {
	By("Fetching Kubernetes events")
	cmd := exec.Command("kubectl", "get", "events", "-n", MPCNamespace, "--sort-by=.lastTimestamp")
	eventsOutput, err := utils.Run(cmd)
	if err == nil {
		filename := fmt.Sprintf("events-%s.txt", MPCNamespace)
		_ = writeToFile(logDir, filename, eventsOutput)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s\n", err)
	}
}

// collectTaskRuns collects TaskRuns from all namespaces and returns a list of failed ones.
// Files created: taskruns-{namespace}.yaml
//
// Parameters:
//   - ctx: context for the Kubernetes client operations
//   - k8sClient: the Kubernetes client to use for listing TaskRuns
//   - logDir: the directory path where the taskruns YAML file will be written
//   - namespace: the namespace to collect taskruns from
//
// Returns:
//   - []string: list of formatted strings describing failed/incomplete TaskRuns
//     (format: "  namespace/name (Status: status reason)")
func collectTaskRuns(ctx context.Context, k8sClient client.Client, logDir, namespace string) []string {
	By("Fetching TaskRuns")

	taskRunList := &tekv1.TaskRunList{}
	if err := k8sClient.List(ctx, taskRunList, client.InNamespace(namespace)); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to list taskruns: %s\n", err)
		return nil
	}

	// Write TaskRuns YAML to file
	taskrunsYaml, err := yaml.Marshal(taskRunList)
	if err == nil {
		filename := fmt.Sprintf("taskruns-%s.yaml", namespace)
		_ = writeToFile(logDir, filename, string(taskrunsYaml))
	}

	// Find failed TaskRuns
	var failedTaskRuns []string
	for _, tr := range taskRunList.Items {
		// Check the Succeeded condition
		for _, condition := range tr.Status.Conditions {
			if condition.Type == "Succeeded" && condition.Status != corev1.ConditionTrue {
				failedTaskRuns = append(failedTaskRuns,
					fmt.Sprintf("  %s/%s (Status: %s %s)", tr.Namespace, tr.Name, condition.Status, condition.Reason))
				break
			}
		}
	}

	return failedTaskRuns
}

// collectPodsInfo collects pods info from a single namespace and categorizes them by status.
// Files created: pods-{namespace}.yaml
//
// Parameters:
//   - ctx: context for the Kubernetes client operations
//   - k8sClient: the Kubernetes client to use for listing pods
//   - logDir: the directory path where pod YAML files will be written
//   - namespace: the namespace to collect pod information from
//
// Returns:
//   - PodsByStatus: struct containing succeeded pods, failed pods, and display strings
func collectPodsInfo(ctx context.Context, k8sClient client.Client, logDir, namespace string) PodsByStatus {
	By("Fetching Pods in " + namespace + " namespace")

	result := PodsByStatus{}

	podList := &corev1.PodList{}
	if err := k8sClient.List(ctx, podList, client.InNamespace(namespace)); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to list pods in %s: %s\n", namespace, err)
		return result
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "Found %d pods in %s namespace\n", len(podList.Items), namespace)

	// Write pods YAML to file
	podsYaml, err := yaml.Marshal(podList)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to marshal pods to YAML: %s\n", err)
	} else {
		filename := fmt.Sprintf("pods-%s.yaml", namespace)
		if writeErr := writeToFile(logDir, filename, string(podsYaml)); writeErr != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to write pods YAML file: %s\n", writeErr)
		}
	}

	// Categorize pods by status
	for _, pod := range podList.Items {
		podNN := types.NamespacedName{Namespace: namespace, Name: pod.Name}
		phase := pod.Status.Phase

		// Check if pod is ready
		isReady := false
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				isReady = true
				break
			}
		}

		// Check if any container failed (non-zero exit code)
		hasFailedContainer := false
		var failedContainerInfo string
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
				hasFailedContainer = true
				failedContainerInfo = fmt.Sprintf("container %s exited with code %d",
					cs.Name, cs.State.Terminated.ExitCode)
				break
			}
		}

		switch {
		case phase == corev1.PodFailed:
			result.FailedPods = append(result.FailedPods, podNN)
			result.ProblematicPodsDisplay = append(result.ProblematicPodsDisplay,
				fmt.Sprintf("  %s (Status: %s)", podNN, phase))
		case phase == corev1.PodSucceeded && hasFailedContainer:
			// Pod completed but a container failed
			result.FailedPods = append(result.FailedPods, podNN)
			result.ProblematicPodsDisplay = append(result.ProblematicPodsDisplay,
				fmt.Sprintf("  %s (Status: %s, %s)", podNN, phase, failedContainerInfo))
		case phase == corev1.PodSucceeded:
			result.SucceededPods = append(result.SucceededPods, podNN)
		case phase == corev1.PodRunning && isReady:
			// Running and ready - not problematic
			result.SucceededPods = append(result.SucceededPods, podNN)
		case phase == corev1.PodRunning && !isReady:
			// Running but not ready
			result.FailedPods = append(result.FailedPods, podNN)
			result.ProblematicPodsDisplay = append(result.ProblematicPodsDisplay,
				fmt.Sprintf("  %s (Status: %s, Ready: false)", podNN, phase))
		default:
			// Pending, Unknown, etc.
			result.FailedPods = append(result.FailedPods, podNN)
			result.ProblematicPodsDisplay = append(result.ProblematicPodsDisplay,
				fmt.Sprintf("  %s (Status: %s)", podNN, phase))
		}
	}

	return result
}

// collectNamespacePodLogs collects pod logs for the specified pods.
// Files created: pod-logs-{namespace}-{podname}.log for each pod
//
// Parameters:
//   - logDir: the directory path where log files will be written
//   - pods: list of pods to collect logs from
//   - failedPods: list of failed pods; logs for these pods will be returned for printing in summary
//
// Returns:
//   - map[types.NamespacedName]string: map of pod identifiers to their logs for failed pods only
func collectNamespacePodLogs(logDir string, pods []types.NamespacedName, failedPods []types.NamespacedName) map[types.NamespacedName]string {
	failedPodLogs := make(map[types.NamespacedName]string)

	for _, pod := range pods {
		By(fmt.Sprintf("Fetching logs for pod %s in %s namespace", pod.Name, pod.Namespace))
		cmd := exec.Command("kubectl", "logs", pod.Name, "-n", pod.Namespace, "--all-containers=true") //nolint:gosec // G204 - e2e test with controlled input
		podLogs, err := utils.Run(cmd)
		if err == nil {
			filename := fmt.Sprintf("pod-logs-%s-%s.log", pod.Namespace, pod.Name)
			_ = writeToFile(logDir, filename, podLogs)

			// Collect logs for failed pods to print in summary
			if slices.Contains(failedPods, pod) {
				failedPodLogs[pod] = podLogs
			}
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get logs for pod %s: %s\n", pod.Name, err)
		}
	}

	return failedPodLogs
}

// printDebugSummary prints the debug information summary to the console.
// The summary includes failed TaskRuns, problematic pods, their logs, and the log directory path.
//
// Parameters:
//   - failedTaskRuns: list of formatted strings describing failed TaskRuns
//   - problematicPods: list of formatted strings describing problematic pods
//   - failedPodLogs: map of pod identifiers to their logs
//   - logDir: the directory path where debug logs were written
func printDebugSummary(failedTaskRuns, problematicPods []string, failedPodLogs map[types.NamespacedName]string, logDir string) {
	_, _ = fmt.Fprintf(GinkgoWriter, "\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "         DEBUG INFO SUMMARY\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n")

	if len(failedTaskRuns) > 0 {
		_, _ = fmt.Fprintf(GinkgoWriter, "\nFailed/Incomplete TaskRuns:\n")
		for _, tr := range failedTaskRuns {
			_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", tr)
		}
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "\nNo failed TaskRuns found.\n")
	}

	if len(problematicPods) > 0 {
		_, _ = fmt.Fprintf(GinkgoWriter, "\nProblematic Pods (not Running/Succeeded or not Ready):\n")
		for _, pod := range problematicPods {
			_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", pod)
		}
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "\nNo problematic pods found.\n")
	}

	// Print logs of failed pods
	if len(failedPodLogs) > 0 {
		_, _ = fmt.Fprintf(GinkgoWriter, "\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "         FAILED POD LOGS\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n")
		for podKey, logs := range failedPodLogs {
			_, _ = fmt.Fprintf(GinkgoWriter, "\n--- Logs for failed pod %s ---\n%s\n--- End of logs for %s ---\n", podKey, logs, podKey)
		}
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "\nDebug logs written to: %s\n", logDir)
	_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n")
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

	_, err = k8sCache.GetInformer(ctx, &corev1.Pod{})
	Expect(err).ToNot(HaveOccurred(), "failed to setup informer for pods")

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

// CollectDebugInfo collects debug information when a test fails.
// It gathers controller pod logs, Kubernetes events, TaskRuns, pod information,
// and pod logs, writing them to files and printing a summary to the console.
// This function only executes if the current test spec has failed.
//
// Parameters:
//   - ctx: context for Kubernetes client operations
//   - k8sClient: the Kubernetes client to use for API operations
//   - testContext: test context containing the controller pod name
//   - testNamespace: the namespace where test resources are created
func CollectDebugInfo(ctx context.Context, k8sClient client.Client, testContext *TestContext, testNamespace string) {
	specReport := CurrentSpecReport()
	if !specReport.Failed() {
		return
	}

	logDir, err := getDebugLogDir()
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get log directory: %s\n", err)
		return
	}

	// Ensure log directory exists
	if err := os.MkdirAll(logDir, 0750); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to create log directory %s: %s\n", logDir, err)
		return
	}

	By("Collecting debug information for failed test")

	collectControllerPodInfo(testContext.ControllerPodName, logDir)
	collectEvents(logDir)
	// Collect taskruns
	failedTaskRuns := collectTaskRuns(ctx, k8sClient, logDir, MPCNamespace)
	if testNamespace != "" {
		failedTaskRuns = append(failedTaskRuns, collectTaskRuns(ctx, k8sClient, logDir, testNamespace)...)
	}

	// Collect pod info from both namespaces
	mpcPodsInfo := collectPodsInfo(ctx, k8sClient, logDir, MPCNamespace)

	// Initialize with default values
	var allFailedPods []types.NamespacedName
	var problematicPods []string
	failedPodLogs := make(map[types.NamespacedName]string)

	// Add MPC namespace pods
	allFailedPods = append(allFailedPods, mpcPodsInfo.FailedPods...)
	problematicPods = append(problematicPods, mpcPodsInfo.ProblematicPodsDisplay...)

	// Only collect test namespace pods if testNamespace is not empty
	var testPodsInfo PodsByStatus
	if testNamespace != "" {
		testPodsInfo = collectPodsInfo(ctx, k8sClient, logDir, testNamespace)
		allFailedPods = append(allFailedPods, testPodsInfo.FailedPods...)
		problematicPods = append(problematicPods, testPodsInfo.ProblematicPodsDisplay...)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "%s namespace is empty\n", testNamespace)
	}

	// Get all pods from MPC namespace (succeeded + failed)
	allMPCPods := append(mpcPodsInfo.SucceededPods, mpcPodsInfo.FailedPods...)
	By("Fetching logs of pods in the " + MPCNamespace + " namespace")
	maps.Copy(failedPodLogs, collectNamespacePodLogs(logDir, allMPCPods, allFailedPods))

	// Get all pods from test namespace (succeeded + failed) if test namespace is specified
	if testNamespace != "" {
		allTestPods := append(testPodsInfo.SucceededPods, testPodsInfo.FailedPods...)
		By("Fetching logs of all pods in the " + testNamespace + " namespace")
		maps.Copy(failedPodLogs, collectNamespacePodLogs(logDir, allTestPods, allFailedPods))
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "%s namespace is empty\n", testNamespace)
	}

	printDebugSummary(failedTaskRuns, problematicPods, failedPodLogs, logDir)
}
