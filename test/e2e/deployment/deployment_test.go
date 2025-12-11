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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/konflux-ci/multi-platform-controller/test/e2e/common"
)

var _ = Describe("Deployment", Ordered, func() {
	var k8sClient client.Client
	testContext := &common.TestContext{}

	BeforeEach(func() {
		By("Creating a k8s client")
		k8sClient = common.GetK8sClientOrDie(context.Background())
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func(ctx context.Context) {
		common.CollectDebugInfo(ctx, k8sClient, testContext, "")
	})

	Context("Manager", func() {
		It("should validate that the controller-manager pod is running", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				podName := common.VerifyControllerPodRunning(g)
				testContext.SetControllerPodName(podName)
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		// TODO: Customize the e2e test suite with scenarios specific to your project.
		// Consider applying sample/CR(s) and check their status and/or verifying
		// the reconciliation by using the metrics, i.e.:
		// metricsOutput := getMetricsOutput()
		// Expect(metricsOutput).To(ContainSubstring(
		//    fmt.Sprintf(`controller_runtime_reconcile_total{controller="%s",result="success"} 1`,
		//    strings.ToLower(<Kind>),
		// ))
	})
})
