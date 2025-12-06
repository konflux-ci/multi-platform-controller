/*
Copyright 2021-2022 Red Hat, Inc.

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
	"fmt"
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/multi-platform-controller/test/utils"
)

// TestTaskRun runs the TaskRun execution test suite. These tests verify
// that TaskRuns can be executed successfully on different platforms.
// This suite runs tests in parallel for faster execution.
func TestTaskRun(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting TaskRun execution test suite\n")
	RunSpecs(t, "taskrun suite")
}

func init() {
	// Enable parallel execution for this suite
	// This allows specs to run in parallel when invoked with -p flag
}

var _ = BeforeSuite(func() {
	By("checking if Tekton is installed")
	cmd := exec.Command("kubectl", "get", "crd", "taskruns.tekton.dev")
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Tekton isn't installed")
})
