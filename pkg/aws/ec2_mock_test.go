package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// mockEC2Client satisfies the ec2API interface for testing.
type mockEC2Client struct {
	RunInstancesOutput       *ec2.RunInstancesOutput
	RunInstancesErr          error
	DescribeInstancesOutput  *ec2.DescribeInstancesOutput
	DescribeInstancesErr     error
	TerminateInstancesOutput *ec2.TerminateInstancesOutput
	TerminateInstancesErr    error
}

func (m *mockEC2Client) RunInstances(_ context.Context, _ *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return m.RunInstancesOutput, m.RunInstancesErr
}

func (m *mockEC2Client) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.DescribeInstancesOutput, m.DescribeInstancesErr
}

func (m *mockEC2Client) TerminateInstances(_ context.Context, _ *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return m.TerminateInstancesOutput, m.TerminateInstancesErr
}

// mockEC2ClientFunc is a function adapter for ec2API that allows inline
// definition of RunInstances behaviour (used for the spot-fallback test).
type mockEC2ClientFunc func(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)

func (f mockEC2ClientFunc) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return f(ctx, params, optFns...)
}
func (f mockEC2ClientFunc) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return nil, nil
}
func (f mockEC2ClientFunc) TerminateInstances(_ context.Context, _ *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return nil, nil
}
