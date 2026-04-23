package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// mockEC2Client satisfies the ec2API interface for testing.
// Output/Err fields control return values; Input fields capture the last call.
type mockEC2Client struct {
	RunInstancesOutput      *ec2.RunInstancesOutput
	RunInstancesErr         error
	DescribeInstancesOutput *ec2.DescribeInstancesOutput
	DescribeInstancesErr    error
	DescribeInstancesInput  *ec2.DescribeInstancesInput
	TerminateInstancesErr   error
}

func (m *mockEC2Client) RunInstances(_ context.Context, _ *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return m.RunInstancesOutput, m.RunInstancesErr
}

func (m *mockEC2Client) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	m.DescribeInstancesInput = input
	return m.DescribeInstancesOutput, m.DescribeInstancesErr
}

func (m *mockEC2Client) TerminateInstances(_ context.Context, _ *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return nil, m.TerminateInstancesErr
}

