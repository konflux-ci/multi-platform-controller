package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const MultiPlatformManaged = "MultiPlatformManaged"

// Ec2Provider returns an AWS EC2 configuration that implements the CloudProvider interface.
func Ec2Provider(platformName string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	disk, err := strconv.Atoi(config["dynamic."+platformName+".disk"])
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

	return AwsDynamicConfig{Region: config["dynamic."+platformName+".region"],
		Ami:                 config["dynamic."+platformName+".ami"],
		InstanceType:        config["dynamic."+platformName+".instance-type"],
		KeyName:             config["dynamic."+platformName+".key-name"],
		Secret:              config["dynamic."+platformName+".aws-secret"],
		SecurityGroup:       config["dynamic."+platformName+".security-group"],
		SecurityGroupId:     config["dynamic."+platformName+".security-group-id"],
		SubnetId:            config["dynamic."+platformName+".subnet-id"],
		SpotInstancePrice:   config["dynamic."+platformName+".spot-price"],
		InstanceProfileName: config["dynamic."+platformName+".instance-profile-name"],
		InstanceProfileArn:  config["dynamic."+platformName+".instance-profile-arn"],
		SystemNamespace:     systemNamespace,
		Disk:                int32(disk),
		Iops:                iops,
		Throughput:          throughput,
		UserData:            userDataPtr,
	}
}

// LaunchInstance creates an EC2 instance and returns its identifier.
func (r AwsDynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, name string, instanceTag string, additionalInstanceTags map[string]string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("attempting to launch AWS EC2 instance for %s", name))

	// Use AWS credentials and configuration to create an EC2 client
	// TODO: Why is 'multi-platform-controller' used as the namespace here instead of ec's SystemNamespace?
	secretCredentials := SecretCredentialsProvider{Name: r.Secret, Namespace: "multi-platform-controller", Client: kubeClient}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(secretCredentials),
		config.WithRegion(r.Region))
	if err != nil {
		return "", fmt.Errorf("failed to create an AWS config for an EC2 client: %w", err)
	}

	// Create an EC2 client
	ec2Client := ec2.NewFromConfig(cfg)

	// Specify the parameters for the new EC2 instance
	var subnet *string
	if r.SubnetId != "" {
		subnet = aws.String(r.SubnetId)
	}
	var securityGroups []string = nil
	if r.SecurityGroup != "" {
		securityGroups = []string{r.SecurityGroup}
	}
	var securityGroupIds []string = nil
	if r.SecurityGroupId != "" {
		securityGroupIds = []string{r.SecurityGroupId}
	}
	var instanceProfile *types.IamInstanceProfileSpecification
	if r.InstanceProfileName != "" || r.InstanceProfileArn != "" {
		instanceProfile = &types.IamInstanceProfileSpecification{}
		if r.InstanceProfileName != "" {
			instanceProfile.Name = aws.String(r.InstanceProfileName)
		}
		if r.InstanceProfileArn != "" {
			instanceProfile.Arn = aws.String(r.InstanceProfileArn)
		}
	}

	instanceTags := []types.Tag{
		{Key: aws.String(MultiPlatformManaged), Value: aws.String("true")},
		{Key: aws.String(cloud.InstanceTag), Value: aws.String(instanceTag)},
		{Key: aws.String("Name"), Value: aws.String("multi-platform-builder-" + name)},
	}

	for k, v := range additionalInstanceTags {
		instanceTags = append(instanceTags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	launchInput := &ec2.RunInstancesInput{
		KeyName:            aws.String(r.KeyName),
		ImageId:            aws.String(r.Ami), //ARM RHEL
		InstanceType:       types.InstanceType(r.InstanceType),
		MinCount:           aws.Int32(1),
		MaxCount:           aws.Int32(1),
		EbsOptimized:       aws.Bool(true),
		SecurityGroups:     securityGroups,
		SecurityGroupIds:   securityGroupIds,
		IamInstanceProfile: instanceProfile,
		SubnetId:           subnet,
		UserData:           r.UserData,
		BlockDeviceMappings: []types.BlockDeviceMapping{{
			DeviceName:  aws.String("/dev/sda1"),
			VirtualName: aws.String("ephemeral0"),
			Ebs:         &types.EbsBlockDevice{DeleteOnTermination: aws.Bool(true), VolumeSize: aws.Int32(r.Disk), VolumeType: types.VolumeTypeGp3, Iops: r.Iops, Throughput: r.Throughput},
		}},
		InstanceInitiatedShutdownBehavior: types.ShutdownBehaviorTerminate,
		TagSpecifications:                 []types.TagSpecification{{ResourceType: types.ResourceTypeInstance, Tags: instanceTags}},
	}
	spotInstanceRequested := r.SpotInstancePrice != ""
	if spotInstanceRequested {
		launchInput.InstanceMarketOptions = &types.InstanceMarketOptionsRequest{MarketType: types.MarketTypeSpot, SpotOptions: &types.SpotMarketOptions{MaxPrice: aws.String(r.SpotInstancePrice), InstanceInterruptionBehavior: types.InstanceInterruptionBehaviorTerminate, SpotInstanceType: types.SpotInstanceTypeOneTime}}
	}

	// Launch the new EC2 instance
	result, err := ec2Client.RunInstances(ctx, launchInput)
	if err != nil {
		// Check to see if there were market options for spot instances.
		// Launching can often fail if there are market options and no spot instances.
		if !spotInstanceRequested {
			return "", fmt.Errorf("failed to launch EC2 instance for %s: %w", name, err)
		}
		// If market options were specified, try launching again without any market options.
		log.Error(err, fmt.Sprintf("failed to launch spot instance, attempting to launch normal EC2 instance for %s", name))
		launchInput.InstanceMarketOptions = nil
		result, err = ec2Client.RunInstances(ctx, launchInput)
		if err != nil {
			return "", fmt.Errorf("failed to launch EC2 instance for %s: %w", name, err)
		}
	}

	if len(result.Instances) > 0 {
		//TODO: clarify comment -> hard coded 10m timeout
		return cloud.InstanceIdentifier(*result.Instances[0].InstanceId), nil
	} else {
		return "", fmt.Errorf("no EC2 instances were created")
	}
}

// CountInstances returns the number of EC2 instances whose names start with instanceTag.
func (r AwsDynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Attempting to count AWS EC2 instances")

	secretCredentials := SecretCredentialsProvider{Name: r.Secret, Namespace: r.SystemNamespace, Client: kubeClient}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(secretCredentials),
		config.WithRegion(r.Region))
	if err != nil {
		return -1, fmt.Errorf("failed to create an EC2 client: %w", err)
	}

	// Create an EC2 client
	ec2Client := ec2.NewFromConfig(cfg)
	res, err := ec2Client.DescribeInstances(
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
	for _, res := range res.Reservations {
		for _, inst := range res.Instances {
			// Verify the instance is running an is of the specified VM "flavor"
			if inst.State.Name != types.InstanceStateNameTerminated && string(inst.InstanceType) == r.InstanceType {
				log.Info(fmt.Sprintf("Counting instance %s towards running count", *inst.InstanceId))
				count++
			}
		}
	}
	return count, nil
}

// GetInstanceAddress returns the IP Address associated with the instanceId EC2 instance. If none is found, an empty
// string is returned.
func (r AwsDynamicConfig) GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("Attempting to get AWS EC2 instance %s's IP address", instanceId))

	secretCredentials := SecretCredentialsProvider{Name: r.Secret, Namespace: r.SystemNamespace, Client: kubeClient}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(secretCredentials),
		config.WithRegion(r.Region))
	if err != nil {
		return "", fmt.Errorf("failed to create an EC2 client: %w", err)
	}

	// Create an EC2 client and get instance
	ec2Client := ec2.NewFromConfig(cfg)
	res, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{string(instanceId)}})
	if err != nil {
		// This might be a transient error, so only log it
		log.Error(err, "failed to retrieve instance", "instanceId", instanceId)
		return "", nil
	}

	// Get IP address
	if len(res.Reservations) > 0 {
		if len(res.Reservations[0].Instances) > 0 {
			instance := res.Reservations[0].Instances[0]
			address, err := r.checkInstanceConnectivity(ctx, &instance)
			// This might be a transient error, so only log it; wait longer for
			// the instance to be ready
			if err != nil {
				return "", nil
			}
			return address, nil
		}
	}
	return "", nil
}

// checkInstanceConnectivity returns instance's IP address if it can be resolved.
func (r AwsDynamicConfig) checkInstanceConnectivity(ctx context.Context, instance *types.Instance) (string, error) {
	if instance.PublicDnsName != nil && *instance.PublicDnsName != "" {
		return pingSSHIp(ctx, *instance.PublicDnsName)
	} else if instance.PrivateIpAddress != nil && *instance.PrivateIpAddress != "" {
		return pingSSHIp(ctx, *instance.PrivateIpAddress)
	}
	return "", nil
}

// pingSSHIp returns the provided IP address if it can be resolved.
func pingSSHIp(ctx context.Context, ipAddress string) (string, error) {
	server, _ := net.ResolveTCPAddr("tcp", ipAddress+":22")
	conn, err := net.DialTCP("tcp", nil, server)
	if err != nil {
		log := logr.FromContextOrDiscard(ctx)
		log.Error(err, "failed to connect to AWS instance")
		return "", err
	}
	defer conn.Close()

	return ipAddress, nil
}

// TerminateInstance tries to delete the instanceId EC2 instance.
func (r AwsDynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instance cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("Attempting to terminate AWS EC2 instance %s", instance))

	secretCredentials := SecretCredentialsProvider{Name: r.Secret, Namespace: "multi-platform-controller", Client: kubeClient}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(secretCredentials),
		config.WithRegion(r.Region))
	if err != nil {
		return fmt.Errorf("failed to create an EC2 client: %w", err)
	}

	ec2Client := ec2.NewFromConfig(cfg)
	_, err = ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{string(instance)}})
	return err
}

// ListInstances returns a collection of accessible EC2 instances whose names start with instanceTag.
func (r AwsDynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Attempting to list AWS EC2 instances")

	secretCredentials := SecretCredentialsProvider{Name: r.Secret, Namespace: r.SystemNamespace, Client: kubeClient}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(secretCredentials),
		config.WithRegion(r.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to create an EC2 client: %w", err)
	}

	// Create an EC2 client & retrieve instances
	ec2Client := ec2.NewFromConfig(cfg)
	res, err := ec2Client.DescribeInstances(
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
	ret := []cloud.CloudVMInstance{}
	for _, res := range res.Reservations {
		for i := range res.Instances {
			inst := res.Instances[i]
			// Verify the instance is running an is of the specified VM "flavor"
			if inst.State.Name != types.InstanceStateNameTerminated && string(inst.InstanceType) == r.InstanceType {
				// Only list instance if it has an accessible IP
				address, err := r.checkInstanceConnectivity(ctx, &inst)
				if err == nil {
					newVmInstance := cloud.CloudVMInstance{
						InstanceId: cloud.InstanceIdentifier(*inst.InstanceId),
						StartTime:  *inst.LaunchTime,
						Address:    address,
					}
					ret = append(ret, newVmInstance)
					log.Info(fmt.Sprintf("counting instance %s towards running count", *inst.InstanceId))
				}
			}
		}
	}
	return ret, nil
}

// A SecretCredentialsProvider is a collection of information needed to generate
// AWS credentials. It implements the AWS CredentialsProvider interface.
type SecretCredentialsProvider struct {
	// Name is the name of the the Kubernetes ExternalSecret resource that contains
	// the SSH key's access key ID and secret access key.
	Name string

	// Namespace is the Kubernetes namespace the ExternalSecret resource resides in.
	Namespace string

	// Client is the client (if any) to use to connect to Kubernetes.
	Client client.Client
}

// Retrieve is one of the AWS CredntialsProvider interface's methods that uses external Kubernetes
// secrets or local environment variables to generate AWS credentials.
func (r SecretCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	// Use local environment variables for credentials
	if r.Client == nil {
		// TODO: add a check if the ENVs are empty?
		return aws.Credentials{
			AccessKeyID:     os.Getenv("MULTI_ARCH_ACCESS_KEY"),
			SecretAccessKey: os.Getenv("MULTI_ARCH_SECRET_KEY"),
		}, nil

	}

	// Connect to Kubernetes to get credentials info
	s := v1.Secret{}
	nameSpacedSecret := types2.NamespacedName{Name: r.Name, Namespace: r.Namespace}
	err := r.Client.Get(ctx, nameSpacedSecret, &s)
	if err != nil {
		return aws.Credentials{},
			fmt.Errorf("failed to retrieve the secret %v from the Kubernetes client: %w", nameSpacedSecret, err)
	}

	return aws.Credentials{
		AccessKeyID:     string(s.Data["access-key-id"]),
		SecretAccessKey: string(s.Data["secret-access-key"]),
	}, nil
}

// An AwsDynamicConfig represents a configuration for an AWS EC2 instance.
// The zero value (where each field will be assigned its type's zero value) is not a
// valid AwsEc2DynamicConfig.
type AwsDynamicConfig struct {
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

	// SecurityGroup is the name of the scurity group to be used on the instance.
	SecurityGroup string

	// SecurityGroupID is the unique identifier of the scurity group to be used on
	// the instance.
	SecurityGroupId string

	// SubnetId is the ID of the subnet to use when creating the instance.
	SubnetId string

	// Disk is the amount of permanent storage (in GB) to allocate the instance.
	Disk int32

	// SpotInstancePrice is the maximum price (TODO: find out format) the user
	// is willing to pay for an EC2 [Spot instance](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-spot-instances.html)
	SpotInstancePrice string

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

func (r AwsDynamicConfig) SshUser() string {
	return "ec2-user"
}
