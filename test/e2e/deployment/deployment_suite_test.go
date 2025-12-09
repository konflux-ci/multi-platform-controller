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

package deployment

import (
	"fmt"
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/multi-platform-controller/test/utils"
)

// TestDeployment runs the deployment validation test suite. These tests verify
// that the controller deployment is successful and the controller pod is running.
func TestDeployment(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting deployment validation test suite\n")
	RunSpecs(t, "deployment suite")
}

var _ = BeforeSuite(func() {
	By("checking if Tekton is installed")
	cmd := exec.Command("kubectl", "get", "crd", "taskruns.tekton.dev")
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Tekton isn't installed")
})
