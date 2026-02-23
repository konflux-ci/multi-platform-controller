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

package otelcol

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/multi-platform-controller/test/e2e/common"
)

func TestOtelcol(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting otelcol log verification test suite\n")
	RunSpecs(t, "otelcol suite")
}

var _ = BeforeSuite(func() {
	if common.S3LogsBucket() == "" {
		Skip(common.S3BucketEnvVar + " is not set, skipping otelcol tests")
	}
	if common.GitHubRunID() == "" {
		Skip(common.GitHubRunIDEnvVar + " is not set, skipping otelcol tests")
	}
})
