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
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// DebugLogDirEnvVar is the environment variable name for the debug log directory
const DebugLogDirEnvVar = "E2E_DEBUG_LOG_DIR"

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
	cmd = exec.Command("kubectl", "get", //nolint:gosec // G204 - e2e test with controlled input
		"pods", podName, "-o", "jsonpath={.status.phase}",
		"-n", MPCNamespace,
	)
	output, err := utils.Run(cmd)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(output).To(Equal("Running"), "Incorrect pod status")

	return podName
}

// getDebugLogDir returns the debug log directory from environment variable.
// If the environment variable is not set, it creates a temporary directory.
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

// writeToFile writes content to a file in the specified directory
func writeToFile(logDir, filename, content string) error {
	filePath := filepath.Join(logDir, filename)
	return os.WriteFile(filePath, []byte(content), 0600)
}

// DumpPodsLogs fetches and prints logs for pods in the given namespace
func DumpPodsLogs(nsName string, podNamesOutput string, logDir string) []string {
	podNames := utils.GetNonEmptyLines(podNamesOutput)
	var writtenFiles []string
	for _, podName := range podNames {
		By(fmt.Sprintf("Fetching logs for pod %s in %s namespace", podName, nsName))
		cmd := exec.Command("kubectl", "logs", podName, "-n", nsName, "--all-containers=true") //nolint:gosec // G204 - e2e test with controlled input
		podLogs, err := utils.Run(cmd)
		if err == nil {
			filename := fmt.Sprintf("pod-logs-%s-%s.log", nsName, podName)
			if writeErr := writeToFile(logDir, filename, podLogs); writeErr == nil {
				writtenFiles = append(writtenFiles, filename)
			}
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get logs for pod %s: %s\n", podName, err)
		}
	}
	return writtenFiles
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
	if !specReport.Failed() {
		return
	}

	logDir, err := getDebugLogDir()
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get log directory: %s\n", err)
		return
	}
	var writtenFiles []string

	// Ensure log directory exists
	if err := os.MkdirAll(logDir, 0750); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to create log directory %s: %s\n", logDir, err)
		return
	}

	By("Collecting debug information for failed test")

	// Collect controller pod logs and description
	if testContext.ControllerPodName != "" {
		By(fmt.Sprintf("Fetching %s pod logs", testContext.ControllerPodName))
		cmd := exec.Command("kubectl", "logs", testContext.ControllerPodName, "-n", MPCNamespace) //nolint:gosec // G204 - e2e test with controlled input
		logs, err := utils.Run(cmd)
		if err == nil {
			filename := fmt.Sprintf("controller-pod-%s.log", testContext.ControllerPodName)
			if writeErr := writeToFile(logDir, filename, logs); writeErr == nil {
				writtenFiles = append(writtenFiles, filename)
			}
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get pod logs: %s\n", err)
		}

		By(fmt.Sprintf("Fetching %s description", testContext.ControllerPodName))
		cmd = exec.Command("kubectl", "describe", "pod", testContext.ControllerPodName, "-n", MPCNamespace) //nolint:gosec // G204 - e2e test with controlled input
		podDescription, err := utils.Run(cmd)
		if err == nil {
			filename := fmt.Sprintf("controller-pod-%s-describe.txt", testContext.ControllerPodName)
			if writeErr := writeToFile(logDir, filename, podDescription); writeErr == nil {
				writtenFiles = append(writtenFiles, filename)
			}
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to describe pod %s\n", testContext.ControllerPodName)
		}
	}

	// Collect Kubernetes events
	By("Fetching Kubernetes events")
	cmd := exec.Command("kubectl", "get", "events", "-n", MPCNamespace, "--sort-by=.lastTimestamp")
	eventsOutput, err := utils.Run(cmd)
	if err == nil {
		filename := fmt.Sprintf("events-%s.txt", MPCNamespace)
		if writeErr := writeToFile(logDir, filename, eventsOutput); writeErr == nil {
			writtenFiles = append(writtenFiles, filename)
		}
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s\n", err)
	}

	// Collect TaskRuns (full YAML for files, summary for output)
	By("Fetching TaskRuns")
	cmd = exec.Command("kubectl", "get", "-A", "-o", "yaml", "taskruns")
	taskrunsYaml, err := utils.Run(cmd)
	if err == nil {
		filename := "taskruns-all.yaml"
		if writeErr := writeToFile(logDir, filename, taskrunsYaml); writeErr == nil {
			writtenFiles = append(writtenFiles, filename)
		}
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get taskruns: %s\n", err)
	}

	// Get TaskRuns summary for console output
	var failedTaskRuns []string
	cmd = exec.Command("kubectl", "get", "taskruns", "-A", "-o", "custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,SUCCEEDED:.status.conditions[0].status,REASON:.status.conditions[0].reason")
	taskrunsSummary, err := utils.Run(cmd)
	if err == nil {
		lines := strings.Split(taskrunsSummary, "\n")
		for _, line := range lines[1:] { // Skip header
			if strings.TrimSpace(line) == "" {
				continue
			}
			// Check for failed taskruns (SUCCEEDED != True)
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[2] != "True" {
				failedTaskRuns = append(failedTaskRuns, fmt.Sprintf("  %s/%s (Status: %s)", fields[0], fields[1], strings.Join(fields[2:], " ")))
			}
		}
	}

	// Collect Pods for each namespace
	var problematicPods []string
	for _, ns := range []string{MPCNamespace, testNamespace} {
		By("Fetching Pods in " + ns + " namespace")
		cmd = exec.Command("kubectl", "get", "pods", "-n", ns, "-o", "yaml") //nolint:gosec // G204 - e2e test with controlled input
		podsYaml, err := utils.Run(cmd)
		if err == nil {
			filename := fmt.Sprintf("pods-%s.yaml", ns)
			if writeErr := writeToFile(logDir, filename, podsYaml); writeErr == nil {
				writtenFiles = append(writtenFiles, filename)
			}
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get pods: %s\n", err)
		}

		// Get pods summary for console output
		cmd = exec.Command("kubectl", "get", "pods", "-n", ns, "-o", "custom-columns=NAME:.metadata.name,STATUS:.status.phase,READY:.status.conditions[?(@.type=='Ready')].status") //nolint:gosec // G204 - e2e test with controlled input
		podsSummary, err := utils.Run(cmd)
		if err == nil {
			lines := strings.Split(podsSummary, "\n")
			for _, line := range lines[1:] { // Skip header
				if strings.TrimSpace(line) == "" {
					continue
				}
				fields := strings.Fields(line)
				// Check for non-running or not-ready pods
				if len(fields) >= 2 && (fields[1] != "Running" && fields[1] != "Succeeded") {
					problematicPods = append(problematicPods, fmt.Sprintf("  %s/%s (Status: %s)", ns, fields[0], fields[1]))
				} else if len(fields) >= 3 && fields[2] != "True" && fields[1] == "Running" {
					problematicPods = append(problematicPods, fmt.Sprintf("  %s/%s (Status: %s, Ready: %s)", ns, fields[0], fields[1], fields[2]))
				}
			}
		}
	}

	// Collect logs of taskrun pods in the MPC namespace
	By("Fetching logs of taskrun pods in the " + MPCNamespace + " namespace")
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
		files := DumpPodsLogs(MPCNamespace, podNamesOutput, logDir)
		writtenFiles = append(writtenFiles, files...)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get pod names in %s namespace: %s\n", MPCNamespace, err)
	}

	// Collect logs of all pods in the test namespace
	By("Fetching logs of all pods in the " + testNamespace + " namespace")
	cmd = exec.Command("kubectl", "get", "pods", "-n", testNamespace, "-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	podNamesOutput, err = utils.Run(cmd)
	if err == nil {
		files := DumpPodsLogs(testNamespace, podNamesOutput, logDir)
		writtenFiles = append(writtenFiles, files...)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get pod names in %s namespace: %s\n", testNamespace, err)
	}

	// Print summary
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

	_, _ = fmt.Fprintf(GinkgoWriter, "\nDebug logs written to: %s\n", logDir)
	if len(writtenFiles) > 0 {
		_, _ = fmt.Fprintf(GinkgoWriter, "Files created:\n")
		for _, f := range writtenFiles {
			_, _ = fmt.Fprintf(GinkgoWriter, "  - %s\n", f)
		}
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n")
}
