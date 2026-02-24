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
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/multi-platform-controller/test/e2e/common"
)

// OTLP JSON structs — only the fields needed to extract sourcetype attributes.
type exportedLogs struct {
	ResourceLogs []resourceLogs `json:"resourceLogs"`
}

type resourceLogs struct {
	ScopeLogs []scopeLogs `json:"scopeLogs"`
}

type scopeLogs struct {
	LogRecords []logRecord `json:"logRecords"`
}

type logRecord struct {
	Attributes []attribute `json:"attributes"`
}

type attribute struct {
	Key   string         `json:"key"`
	Value attributeValue `json:"value"`
}

type attributeValue struct {
	StringValue string `json:"stringValue"`
}

// requiredSourceTypes lists the otelcol sourcetypes that must be present
// for each Linux host (defined in otelcollector.yaml receivers).
var requiredSourceTypes = []string{"audit", "messages", "secure"}

// minimumLinuxHosts is the minimum number of distinct hosts we expect
// (linux/amd64 + linux/arm64).
const minimumLinuxHosts = 2

var _ = Describe("Otelcol logs verification", Ordered, func() {
	var (
		s3Client  *s3.Client
		bucket    string
		runPrefix string
	)

	BeforeAll(func() {
		ctx := context.Background()

		var err error
		s3Client, err = common.NewS3Client(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to create S3 client")

		bucket = common.S3LogsBucket()
		runPrefix = common.S3RunPrefix(common.GitHubRunID())

		_, _ = fmt.Fprintf(GinkgoWriter, "S3 bucket: %s, prefix: %s\n", bucket, runPrefix)
	})

	It("should find at least two Linux hosts with logs", func(ctx context.Context) {
		Eventually(func(g Gomega) {
			keys, err := common.ListLogObjects(ctx, s3Client, bucket, runPrefix)
			g.Expect(err).ShouldNot(HaveOccurred(), "failed to list S3 objects")

			hostIDs := common.ExtractHostIDs(keys, runPrefix)
			_, _ = fmt.Fprintf(GinkgoWriter, "Found %d host(s): %v\n", len(hostIDs), hostIDs)

			g.Expect(len(hostIDs)).Should(BeNumerically(">=", minimumLinuxHosts),
				"expected at least %d hosts, got %d", minimumLinuxHosts, len(hostIDs))
		}, 5*time.Minute, 30*time.Second).Should(Succeed())
	})

	It("should contain all three log types for each host", func(ctx context.Context) {
		Eventually(func(g Gomega) {
			allKeys, err := common.ListLogObjects(ctx, s3Client, bucket, runPrefix)
			g.Expect(err).ShouldNot(HaveOccurred(), "failed to list S3 objects")

			hostIDs := common.ExtractHostIDs(allKeys, runPrefix)
			g.Expect(len(hostIDs)).Should(BeNumerically(">=", minimumLinuxHosts),
				"not enough hosts found yet")

			for _, hostID := range hostIDs {
				hostKeys := common.KeysForHost(allKeys, runPrefix, hostID)
				g.Expect(hostKeys).ShouldNot(BeEmpty(),
					"no log objects found for host %s", hostID)

				sourceTypes := extractSourceTypes(ctx, g, s3Client, bucket, hostKeys)
				_, _ = fmt.Fprintf(GinkgoWriter,
					"Host %s: found sourcetypes %v\n", hostID, sourceTypes)

				for _, required := range requiredSourceTypes {
					g.Expect(sourceTypes).Should(HaveKey(required),
						"host %s missing sourcetype %q (found: %v)",
						hostID, required, sourceTypes)
				}
			}
		}, 5*time.Minute, 30*time.Second).Should(Succeed())
	})
})

// extractSourceTypes downloads the S3 objects for the given keys, parses
// the OTLP JSON, and returns the set of com.splunk.sourcetype values found.
func extractSourceTypes(ctx context.Context, g Gomega, client *s3.Client, bucket string, keys []string) map[string]struct{} {
	found := make(map[string]struct{})

	for _, key := range keys {
		data, err := common.GetObjectContent(ctx, client, bucket, key)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: failed to read %s: %v\n", key, err)
			continue
		}

		var logs exportedLogs
		if err := json.Unmarshal(data, &logs); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: failed to parse %s: %v\n", key, err)
			continue
		}

		for _, rl := range logs.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, lr := range sl.LogRecords {
					for _, attr := range lr.Attributes {
						if attr.Key == "com.splunk.sourcetype" && attr.Value.StringValue != "" {
							found[attr.Value.StringValue] = struct{}{}
						}
					}
				}
			}
		}
	}

	return found
}
