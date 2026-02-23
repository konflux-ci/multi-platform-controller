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
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	S3BucketEnvVar    = "S3_LOGS_BUCKET"
	GitHubRunIDEnvVar = "GITHUB_RUN_ID"
	S3BasePrefix      = "mpc"
)

// S3LogsBucket returns the S3 bucket name from the environment.
func S3LogsBucket() string {
	return strings.TrimSpace(os.Getenv(S3BucketEnvVar))
}

// GitHubRunID returns the GitHub run ID from the environment.
func GitHubRunID() string {
	return strings.TrimSpace(os.Getenv(GitHubRunIDEnvVar))
}

// S3RunPrefix returns the S3 prefix for a given GitHub run ID,
// e.g. "mpc/12345678/".
func S3RunPrefix(runID string) string {
	return fmt.Sprintf("%s/%s/", S3BasePrefix, runID)
}

// NewS3Client creates an S3 client using the default AWS credential chain
// (environment variables set by aws-actions/configure-aws-credentials in CI).
func NewS3Client(ctx context.Context) (*s3.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return s3.NewFromConfig(cfg), nil
}

// ListLogObjects returns all S3 object keys under the given prefix.
func ListLogObjects(ctx context.Context, client *s3.Client, bucket, prefix string) ([]string, error) {
	var keys []string

	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing S3 objects with prefix %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
	}

	return keys, nil
}

// GetObjectContent downloads the content of a single S3 object.
func GetObjectContent(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
	output, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("getting S3 object %s: %w", key, err)
	}
	defer func() {
		_ = output.Body.Close()
	}()

	data, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, fmt.Errorf("reading S3 object %s: %w", key, err)
	}
	return data, nil
}

// ExtractHostIDs parses S3 keys of the form "mpc/<runID>/<hostID>/..."
// and returns the unique set of host IDs found.
func ExtractHostIDs(keys []string, runPrefix string) []string {
	seen := make(map[string]struct{})
	for _, key := range keys {
		rest := strings.TrimPrefix(key, runPrefix)
		if rest == key {
			continue
		}
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) >= 1 && parts[0] != "" {
			seen[parts[0]] = struct{}{}
		}
	}

	hosts := make([]string, 0, len(seen))
	for h := range seen {
		hosts = append(hosts, h)
	}
	return hosts
}

// KeysForHost filters S3 keys to only those belonging to a specific host.
func KeysForHost(keys []string, runPrefix, hostID string) []string {
	hostPrefix := runPrefix + hostID + "/"
	var result []string
	for _, key := range keys {
		if strings.HasPrefix(key, hostPrefix) {
			result = append(result, key)
		}
	}
	return result
}
