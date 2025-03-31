// Package aws implements methods described in the [cloud] package for interacting with AWS cloud instances.
// Currently only EC2 instances are supported.
//
// All methods of the CloudProvider interface are implemented and separated from other helper functions used
// across the methods.
package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const MultiPlatformManaged = "MultiPlatformManaged"

// CreateEc2CloudConfig returns an AWS EC2 cloud configuration that implements the CloudProvider interface.
func CreateEc2CloudConfig(platformName string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	disk, err := strconv.ParseInt(config["dynamic."+platformName+".disk"], 10, 32)
	if err != nil {
		disk = 40
	}

	var iops *int32
	iopsString := config["dynamic."+platformName+".iops"]
	if iopsString != "" {
		iopsTmp, err := strconv.Atoi(iopsString)
		if err == nil {
			iops = aws.Int32(int32(iopsTmp))
		}
	}

	var throughput *int32
	throughputString := config["dynamic."+platformName+".throughput"]
	if throughputString != "" {
		throughputTmp, err := strconv.Atoi(throughputString)
		if err == nil {
			throughput = aws.Int32(int32(throughputTmp))
		}
	}

	userDataString := config["dynamic."+platformName+".user-data"]
	var userDataPtr *string
	if userDataString != "" {
		base54val := base64.StdEncoding.EncodeToString([]byte(userDataString))
		userDataPtr = &base54val
	}

	return AwsEc2DynamicConfig{Region: config["dynamic."+platformName+".region"],
		Ami:                  config["dynamic."+platformName+".ami"],
		InstanceType:         config["dynamic."+platformName+".instance-type"],
		KeyName:              config["dynamic."+platformName+".key-name"],
		Secret:               config["dynamic."+platformName+".aws-secret"],
		SecurityGroup:        config["dynamic."+platformName+".security-group"],
		SecurityGroupId:      config["dynamic."+platformName+".security-group-id"],
		SubnetId:             config["dynamic."+platformName+".subnet-id"],
		MaxSpotInstancePrice: config["dynamic."+platformName+".spot-price"],
		InstanceProfileName:  config["dynamic."+platformName+".instance-profile-name"],
		InstanceProfileArn:   config["dynamic."+platformName+".instance-profile-arn"],
		SystemNamespace:      systemNamespace,
		Disk:                 int32(disk),
		Iops:                 iops,
		Throughput:           throughput,
		UserData:             userDataPtr,
	}
}

// LaunchInstance creates an EC2 instance and returns its identifier.
func (ec AwsEc2DynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, name string, instanceTag string, additionalInstanceTags map[string]string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("attempting to launch AWS EC2 instance for %s", name))

	// Use AWS credentials and configuration to create an EC2 client
	ec2Client, err := ec.createClient(kubeClient, ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create an EC2 client: %w", err)
	}

	// Launch the new EC2 instance
	launchInput := ec.configureInstance(name, instanceTag, additionalInstanceTags)
	runInstancesOutput, err := ec2Client.RunInstances(ctx, launchInput)
	if err != nil {
		// Check to see if there were market options for spot instances.
		// Launching can often fail if there are market options and no spot instances.
		if launchInput.InstanceMarketOptions == nil {
			return "", fmt.Errorf("failed to launch EC2 instance for %s: %w", name, err)
		}
		// If market options were specified, try launching again without any market options.
		log.Error(err, fmt.Sprintf("failed to launch spot instance, attempting to launch normal EC2 instance for %s", name))
		launchInput.InstanceMarketOptions = nil
		runInstancesOutput, err = ec2Client.RunInstances(ctx, launchInput)
		if err != nil {
			return "", fmt.Errorf("failed to launch EC2 instance for %s: %w", name, err)
		}
	}

	if len(runInstancesOutput.Instances) > 0 {
		//TODO: clarify comment -> hard coded 10m timeout
		return cloud.InstanceIdentifier(*runInstancesOutput.Instances[0].InstanceId), nil
	}
	return "", fmt.Errorf("no EC2 instances were created")
}

// CountInstances returns the number of EC2 instances whose names start with instanceTag.
func (ec AwsEc2DynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Attempting to count AWS EC2 instances")

	// Use AWS credentials and configuration to create an EC2 client
	ec2Client, err := ec.createClient(kubeClient, ctx)
	if err != nil {
		return -1, fmt.Errorf("failed to create an EC2 client: %w", err)
	}

	// Get instances
	instancesOutput, err := ec2Client.DescribeInstances(
		ctx,
		&ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{Name: aws.String("tag:" + cloud.InstanceTag), Values: []string{instanceTag}},
				{Name: aws.String("tag:" + MultiPlatformManaged), Values: []string{"true"}},
			},
		},
	)
	if err != nil {
		log.Error(err, "failed to retrieve EC2 instances", "instanceTag", instanceTag)
		return -1, fmt.Errorf("failed to retrieve EC2 instances starting with %s: %w", instanceTag, err)
	}

	count := 0
	for _, reservation := range instancesOutput.Reservations {
		for _, instance := range reservation.Instances {
			// Verify the instance is running an is of the specified VM "flavor"
			if instance.State.Name != types.InstanceStateNameTerminated && string(instance.InstanceType) == ec.InstanceType {
				log.Info(fmt.Sprintf("Counting instance %s towards running count", *instance.InstanceId))
				count++
			}
		}
	}
	return count, nil
}

// GetInstanceAddress returns the IP Address associated with the instanceId EC2 instance. If none is found, an empty
// string is returned.
func (ec AwsEc2DynamicConfig) GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("Attempting to get AWS EC2 instance %s's IP address", instanceId))

	// Use AWS credentials and configuration to create an EC2 client
	ec2Client, err := ec.createClient(kubeClient, ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create an EC2 client: %w", err)
	}

	// Get instances
	instancesOutput, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{string(instanceId)}})
	if err != nil {
		// This might be a transient error, so only log it
		log.Error(err, "failed to retrieve instance", "instanceId", instanceId)
		return "", nil
	}

	// Get IP address
	if len(instancesOutput.Reservations) > 0 {
		if len(instancesOutput.Reservations[0].Instances) > 0 {
			instance := instancesOutput.Reservations[0].Instances[0]
			ip, err := ec.validateIPAddress(ctx, &instance)
			// This might be a transient error, so only log it; wait longer for
			// the instance to be ready
			if err != nil {
				return "", nil
			}
			return ip, nil
		}
	}
	return "", nil
}

// TerminateInstance tries to delete the instanceId EC2 instance.
func (ec AwsEc2DynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("Attempting to terminate AWS EC2 instance %s", instanceId))

	// Use AWS credentials and configuration to create an EC2 client
	ec2Client, err := ec.createClient(kubeClient, ctx)
	if err != nil {
		return fmt.Errorf("failed to create an EC2 client: %w", err)
	}

	_, err = ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{string(instanceId)}})
	return err
}

// ListInstances returns a collection of accessible EC2 instances whose names start with instanceTag.
func (ec AwsEc2DynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Attempting to list AWS EC2 instances")

	// Use AWS credentials and configuration to create an EC2 client
	ec2Client, err := ec.createClient(kubeClient, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create an EC2 client: %w", err)
	}

	// Get instances
	instancesOutput, err := ec2Client.DescribeInstances(
		ctx,
		&ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{Name: aws.String("tag:" + cloud.InstanceTag), Values: []string{instanceTag}},
				{Name: aws.String("tag:" + MultiPlatformManaged), Values: []string{"true"}},
			},
		},
	)
	if err != nil {
		log.Error(err, "failed to retrieve EC2 instances", "instanceTag", instanceTag)
		return nil, fmt.Errorf("failed to retrieve EC2 instances starting with %s: %w", instanceTag, err)
	}

	// Append each instance to the list of CloudVMInstances
	vmInstances := []cloud.CloudVMInstance{}
	for _, reservation := range instancesOutput.Reservations {
		for i := range reservation.Instances {
			instance := reservation.Instances[i]
			// Verify the instance is running an is of the specified VM "flavor"
			if instance.State.Name != types.InstanceStateNameTerminated && string(instance.InstanceType) == ec.InstanceType {
				// Only list instance if it has an accessible IP
				ip, err := ec.validateIPAddress(ctx, &instance)
				if err == nil {
					newVmInstance := cloud.CloudVMInstance{
						InstanceId: cloud.InstanceIdentifier(*instance.InstanceId),
						StartTime:  *instance.LaunchTime,
						Address:    ip,
					}
					vmInstances = append(vmInstances, newVmInstance)
					log.Info(fmt.Sprintf("Counting instance %s towards running count", *instance.InstanceId))
				}
			}
		}
	}
	return vmInstances, nil
}

func (r AwsEc2DynamicConfig) SshUser() string {
	return "ec2-user"
}

// TODO: implement this function.
func (ec AwsEc2DynamicConfig) GetState(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	return "ACTIVE", nil
}

// An AwsEc2DynamicConfig represents a configuration for an AWS EC2 instance.
// The zero value (where each field will be assigned its type's zero value) is not a
// valid AwsEc2DynamicConfig.
type AwsEc2DynamicConfig struct {
	// Region is the geographical area to be associated with the instance.
	// See the [AWS region docs](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Concepts.RegionsAndAvailabilityZones.html)
	// for valid regions.
	Region string

	// Ami is the Amazon Machine Image used to provide the software to the instance.
	Ami string

	// InstanceType corresponds to the AWS instance type, which specifies the
	// hardware of the host computer used for the instance. See the
	// [AWS instance naming docs](https://docs.aws.amazon.com/ec2/latest/instancetypes/instance-type-names.html)
	// for proper instance type naming conventions.
	InstanceType string

	// KeyName is the name of the SSH key inside of AWS.
	KeyName string

	// Secret is the name of the Kubernetes ExternalSecret resource that contains
	// the SSH key's access key ID and secret access key.
	Secret string

	// SystemNamespace is the name of the Kubernetes namespace where the specified
	// secrets are stored.
	SystemNamespace string

	// SecurityGroup is the name of the security group to be used on the instance.
	SecurityGroup string

	// SecurityGroupID is the unique identifier of the security group to be used on
	// the instance.
	SecurityGroupId string

	// SubnetId is the ID of the subnet to use when creating the instance.
	SubnetId string

	// Disk is the amount of permanent storage (in GB) to allocate the instance.
	Disk int32

	// MaxSpotInstancePrice is the maximum price (TODO: find out format) the user
	// is willing to pay for an EC2 [Spot instance](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-spot-instances.html)
	MaxSpotInstancePrice string

	// InstanceProfileName is the name of the instance profile (a container for
	// an AWS IAM role attached to an EC2 instance).
	InstanceProfileName string

	// InstanceProfileArn is the Amazon Resource Name of the instance profile.
	InstanceProfileArn string

	// Throughput is the amount of traffic (in MiB/s) provisioned for the
	// instance's EBS volume(s).
	Throughput *int32

	// Iops is the number of input/output (I/O) operations per second provisioned
	// for the instance's EBS volume(s).
	Iops *int32

	// TODO: determine what this is for (see commonUserData in ibmp_test.go)
	UserData *string
}
