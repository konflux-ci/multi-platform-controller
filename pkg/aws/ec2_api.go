package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// ec2API is the subset of the EC2 client API used by this package.
// The real *ec2.Client satisfies this interface; tests can substitute a mock.
type ec2API interface {
	RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
}
