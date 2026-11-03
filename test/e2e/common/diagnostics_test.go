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
	"strings"
	"testing"

	tekv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kapi "knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

func TestDescribeTaskRunState(t *testing.T) {
	tests := []struct {
		name     string
		taskRun  *tekv1.TaskRun
		contains []string
	}{
		{
			name:     "no object observed",
			taskRun:  nil,
			contains: []string{"no object was observed"},
		},
		{
			name: "no condition yet",
			taskRun: &tekv1.TaskRun{
				ObjectMeta: metav1.ObjectMeta{Namespace: "test-linux-amd64", Name: "test-linux-amd64abcd"},
			},
			contains: []string{"test-linux-amd64/test-linux-amd64abcd", "no Succeeded condition yet"},
		},
		{
			name: "allocation timed out",
			taskRun: &tekv1.TaskRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-windows-c4xlarge-amd64",
					Name:      "test-windows-c4xlarge-amd64wvlww",
					Annotations: map[string]string{
						cloudInstanceIDAnnotation:     "i-0bf081f8b45efd606",
						allocationStartTimeAnnotation: "1793643701",
					},
				},
				Status: tekv1.TaskRunStatus{
					TaskRunStatusFields: tekv1.TaskRunStatusFields{PodName: "test-windows-c4xlarge-amd64wvlww-pod"},
					Status: duckv1.Status{Conditions: duckv1.Conditions{{
						Type:    kapi.ConditionSucceeded,
						Status:  corev1.ConditionFalse,
						Reason:  "Failed",
						Message: "timed out waiting for instance address",
					}}},
				},
			},
			contains: []string{
				"pod=test-windows-c4xlarge-amd64wvlww-pod",
				"cloud-instance-id=i-0bf081f8b45efd606",
				"allocation-start-time=1793643701",
				`Succeeded=False reason="Failed" message="timed out waiting for instance address"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescribeTaskRunState(tt.taskRun)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("DescribeTaskRunState() = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}
