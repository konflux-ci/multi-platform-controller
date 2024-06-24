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

func (r AwsDynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, name string, instanceTag string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("attempting to launch AWS instance for %s", name))
	// Load AWS credentials and configuration

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(SecretCredentialsProvider{Name: r.Secret, Namespace: "multi-platform-controller", Client: kubeClient}),
		config.WithRegion(r.Region))
	if err != nil {
		return "", err
	}

	// Create an EC2 client
	ec2Client := ec2.NewFromConfig(cfg)

	var subnet *string
	if r.SubnetId != "" {
		subnet = aws.String(r.SubnetId)
	}
	// Specify the parameters for the new EC2 instance
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
		TagSpecifications:                 []types.TagSpecification{{ResourceType: types.ResourceTypeInstance, Tags: []types.Tag{{Key: aws.String(MultiPlatformManaged), Value: aws.String("true")}, {Key: aws.String(cloud.InstanceTag), Value: aws.String(instanceTag)}, {Key: aws.String("Name"), Value: aws.String("multi-platform-builder-" + name)}}}},
	}
	spotInstanceRequested := r.SpotInstancePrice != ""
	if spotInstanceRequested {
		launchInput.InstanceMarketOptions = &types.InstanceMarketOptionsRequest{MarketType: types.MarketTypeSpot, SpotOptions: &types.SpotMarketOptions{MaxPrice: aws.String(r.SpotInstancePrice), InstanceInterruptionBehavior: types.InstanceInterruptionBehaviorTerminate, SpotInstanceType: types.SpotInstanceTypeOneTime}}
	}

	// Launch the new EC2 instance
	result, err := ec2Client.RunInstances(ctx, launchInput)
	if err != nil {
		// If often we can fail if there are no spot instances available
		// Try again with non spot instances
		if !spotInstanceRequested {
			return "", err
		}
		log.Error(err, fmt.Sprintf("failed to launch spot instance, attempting to launch normal instance for %s", name))
		launchInput.InstanceMarketOptions = nil
		result, err = ec2Client.RunInstances(ctx, launchInput)
		if err != nil {
			return "", err
		}
	}

	// The result will contain information about the newly created instance(s)
	if len(result.Instances) > 0 {
		//hard coded 10m timeout
		return cloud.InstanceIdentifier(*result.Instances[0].InstanceId), nil
	} else {
		return "", fmt.Errorf("no instances were created")
	}
}

func (r AwsDynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("attempting to count AWS instances")
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(SecretCredentialsProvider{Name: r.Secret, Namespace: r.SystemNamespace, Client: kubeClient}),
		config.WithRegion(r.Region))
	if err != nil {
		return 0, err
	}

	// Create an EC2 client
	ec2Client := ec2.NewFromConfig(cfg)
	res, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: []types.Filter{{Name: aws.String("tag:" + cloud.InstanceTag), Values: []string{instanceTag}}, {Name: aws.String("tag:" + MultiPlatformManaged), Values: []string{"true"}}}})
	if err != nil {
		log.Error(err, "failed to describe instance")
		return 0, err
	}
	count := 0
	for _, res := range res.Reservations {
		for _, inst := range res.Instances {
			if inst.State.Name != types.InstanceStateNameTerminated && string(inst.InstanceType) == r.InstanceType {
				log.Info(fmt.Sprintf("counting instance %s towards running count", *inst.InstanceId))
				count++
			}
		}
	}
	return count, nil
}

func (r AwsDynamicConfig) GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("attempting to get AWS instance address %s", instanceId))
	// Load AWS credentials and configuration

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(SecretCredentialsProvider{Name: r.Secret, Namespace: r.SystemNamespace, Client: kubeClient}),
		config.WithRegion(r.Region))
	if err != nil {
		return "", err
	}

	// Create an EC2 client
	ec2Client := ec2.NewFromConfig(cfg)
	res, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{string(instanceId)}})
	if err != nil {
		log.Error(err, "failed to describe instance")
		//this might be transient, just log it
		return "", nil
	}
	if len(res.Reservations) > 0 {
		if len(res.Reservations[0].Instances) > 0 {
			instance := res.Reservations[0].Instances[0]
			address, err := r.checkInstanceConnectivity(ctx, &instance)
			if err != nil {
				// this might be transient, wait more for the instance to be ready
				return "", nil
			}
			return address, nil
		}
	}
	return "", nil
}

func (r AwsDynamicConfig) checkInstanceConnectivity(ctx context.Context, instance *types.Instance) (string, error) {
	if instance.PublicDnsName != nil && *instance.PublicDnsName != "" {
		return pingSSHIp(ctx, *instance.PublicDnsName)
	} else if instance.PrivateIpAddress != nil && *instance.PrivateIpAddress != "" {
		return pingSSHIp(ctx, *instance.PrivateIpAddress)
	}
	return "", nil
}

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

func (r AwsDynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instance cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("attempting to terminate AWS instance %s", instance))

	// Load AWS credentials and configuration

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(SecretCredentialsProvider{Name: r.Secret, Namespace: "multi-platform-controller", Client: kubeClient}),
		config.WithRegion(r.Region))
	if err != nil {
		return err
	}

	ec2Client := ec2.NewFromConfig(cfg)
	_, err = ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{string(instance)}})
	return err
}

func (r AwsDynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("attempting to list AWS instances")
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(SecretCredentialsProvider{Name: r.Secret, Namespace: r.SystemNamespace, Client: kubeClient}),
		config.WithRegion(r.Region))
	if err != nil {
		return nil, err
	}

	// Create an EC2 client
	ec2Client := ec2.NewFromConfig(cfg)
	res, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: []types.Filter{{Name: aws.String("tag:" + cloud.InstanceTag), Values: []string{instanceTag}}, {Name: aws.String("tag:" + MultiPlatformManaged), Values: []string{"true"}}}})
	if err != nil {
		log.Error(err, "failed to describe instance")
		return nil, err
	}
	ret := []cloud.CloudVMInstance{}
	for _, res := range res.Reservations {
		for i := range res.Instances {
			inst := res.Instances[i]
			if inst.State.Name != types.InstanceStateNameTerminated && string(inst.InstanceType) == r.InstanceType {
				address, err := r.checkInstanceConnectivity(ctx, &inst)
				if err == nil {
					ret = append(ret, cloud.CloudVMInstance{InstanceId: cloud.InstanceIdentifier(*inst.InstanceId), StartTime: *inst.LaunchTime, Address: address})
					log.Info(fmt.Sprintf("counting instance %s towards running count", *inst.InstanceId))
				}
			}
		}
	}
	return ret, nil
}

type SecretCredentialsProvider struct {
	Name      string
	Namespace string
	Client    client.Client
}

func (r SecretCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	if r.Client == nil {
		return aws.Credentials{AccessKeyID: os.Getenv("MULTI_ARCH_ACCESS_KEY"), SecretAccessKey: os.Getenv("MULTI_ARCH_SECRET_KEY")}, nil

	}

	s := v1.Secret{}
	err := r.Client.Get(ctx, types2.NamespacedName{Namespace: r.Namespace, Name: r.Name}, &s)
	if err != nil {
		return aws.Credentials{}, err
	}

	return aws.Credentials{AccessKeyID: string(s.Data["access-key-id"]), SecretAccessKey: string(s.Data["secret-access-key"])}, nil
}

type AwsDynamicConfig struct {
	Region              string
	Ami                 string
	InstanceType        string
	KeyName             string
	Secret              string
	SystemNamespace     string
	SecurityGroup       string
	SecurityGroupId     string
	SubnetId            string
	Disk                int32
	SpotInstancePrice   string
	InstanceProfileName string
	InstanceProfileArn  string
	Throughput          *int32
	Iops                *int32
	UserData            *string
}

func (r AwsDynamicConfig) SshUser() string {
	return "ec2-user"
}
