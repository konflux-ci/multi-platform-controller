package aws

import (
	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var _ = Describe("AWS EC2 Helper Functions", func() {
	DescribeTable("Find VM instances linked to non-existent TaskRuns",
		func(log logr.Logger, ec2Reservations []types.Reservation, existingTaskRuns map[string][]string, expectedInstances []string) {
			cfg := AWSEc2DynamicConfig{}
			Expect(cfg.findInstancesWithoutTaskRuns(log, ec2Reservations, existingTaskRuns)).To(Equal(expectedInstances))
		},
		Entry("no reservations", logr.Discard(),
			[]types.Reservation{}, map[string][]string{},
			nil,
		),
		Entry("no instances", logr.Discard(),
			[]types.Reservation{{Instances: []types.Instance{}}},
			map[string][]string{},
			nil,
		),
		Entry("instance w/ no tags", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{InstanceId: aws.String("id"), Tags: []types.Tag{}},
				}},
			},
			map[string][]string{},
			[]string{"id"},
		),
		Entry("instance w/ no TaskRun ID tag", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("id"),
						Tags:       []types.Tag{{Key: aws.String("key"), Value: aws.String("value")}},
					},
				}},
			},
			map[string][]string{},
			[]string{"id"},
		),
		Entry("instance w/ invalid TaskRun ID", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("id"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("value")}},
					},
				}},
			},
			map[string][]string{},
			[]string{"id"},
		),
		Entry("all instances have existing TaskRuns", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("task1"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task1")}},
					},
					{
						InstanceId: aws.String("task2"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task2")}},
					},
					{
						InstanceId: aws.String("task3"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task3")}},
					},
					{
						InstanceId: aws.String("task4"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task4")}},
					},
				}},
			},
			map[string][]string{"test": {"task1", "task2", "task3", "task4"}},
			nil,
		),
		Entry("one instance doesn't have a TaskRun", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("task-a"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task-a")}},
					},
					{
						InstanceId: aws.String("task2"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task2")}},
					},
					{
						InstanceId: aws.String("task3"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task3")}},
					},
					{
						InstanceId: aws.String("task4"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task4")}},
					},
				}},
			},
			map[string][]string{"test": {"task1", "task2", "task3", "task4"}},
			[]string{"task-a"},
		),
		Entry("multiple instances don't have a TaskRun", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("task-a"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task-a")}},
					},
					{
						InstanceId: aws.String("task-b"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("")}},
					},
					{
						InstanceId: aws.String("task3"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task3")}},
					},
					{
						InstanceId: aws.String("task4"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task4")}},
					},
				}},
			},
			map[string][]string{"test": {"task1", "task2", "task3", "task4"}},
			[]string{"task-a", "task-b"}),
		Entry("no instances have a TaskRun", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("task1"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task1")}},
					},
					{
						InstanceId: aws.String("task2"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task2")}},
					},
					{
						InstanceId: aws.String("task3"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task3")}},
					},
					{
						InstanceId: aws.String("task4"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task4")}},
					},
				}},
			},
			map[string][]string{"test-namespace": {"task1", "task2", "task3", "task4"}},
			[]string{"task1", "task2", "task3", "task4"}),
	)
})
