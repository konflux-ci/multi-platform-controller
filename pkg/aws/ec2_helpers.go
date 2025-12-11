package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"

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

// pingIPAddress tries to resolve the IP address ip. An error is returned if ipAddress couldn't be reached.
func pingIPAddress(ipAddress string) error {
	server, err := net.ResolveTCPAddr("tcp", ipAddress+":22")
	if err != nil {
		return err
	}
	conn, err := net.DialTCP("tcp", nil, server)
	if err != nil {
		return err
	}
	return conn.Close()
}

// validateIPAddress returns the IP address of the EC2 instance ec after determining that the address
// can be resolved (if the instance has one).
func (ec AWSEc2DynamicConfig) validateIPAddress(ctx context.Context, instance *types.Instance) (string, error) {
	var ip string
	var err error
	if instance.PublicDnsName != nil && *instance.PublicDnsName != "" {
		ip = *instance.PublicDnsName
	} else if instance.PublicIpAddress != nil && *instance.PublicIpAddress != "" {
		ip = *instance.PublicIpAddress
	} else if instance.PrivateIpAddress != nil && *instance.PrivateIpAddress != "" && !ec.StrictPublicAddress {
		ip = *instance.PrivateIpAddress
	}

	if ip != "" {
		err = pingIPAddress(ip)
	}
	if err != nil {
		log := logr.FromContextOrDiscard(ctx)
		log.Error(err, "failed to connect to AWS instance")
		err = fmt.Errorf("failed to resolve IP address %s: %w", ip, err)
	}
	return ip, err
}

// createClient uses AWS credentials and an EC2 configuration to create and return an EC2 client.
func (ec AWSEc2DynamicConfig) createClient(kubeClient client.Client, ctx context.Context) (*ec2.Client, error) {
	secretCredentials := SecretCredentialsProvider{Name: ec.Secret, Namespace: ec.SystemNamespace, Client: kubeClient}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(secretCredentials),
		config.WithRegion(ec.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to create an AWS config for an EC2 client: %w", err)
	}
	return ec2.NewFromConfig(cfg), nil
}

// configureInstance creates and returns an EC2 instance configuration.
func (ec AWSEc2DynamicConfig) configureInstance(taskRunName string, instanceTag string, additionalInstanceTags map[string]string) (*ec2.RunInstancesInput, error) {
	// Validate that MacOS-specific fields are either all set or all empty
	tenancySet := ec.Tenancy != ""
	hostResourceGroupSet := ec.HostResourceGroupArn != ""
	licenseConfigSet := ec.LicenseConfigurationArn != ""

	if tenancySet || hostResourceGroupSet || licenseConfigSet {
		if !tenancySet || !hostResourceGroupSet || !licenseConfigSet {
			return nil, errors.New("MacOS dedicated host configuration requires all three fields: Tenancy, HostResourceGroupArn, and LicenseConfigurationArn must be set together")
		}
	}

	var subnet *string
	var securityGroups []string
	var securityGroupIds []string
	var instanceProfile *types.IamInstanceProfileSpecification
	var instanceMarketOpts *types.InstanceMarketOptionsRequest
	var placement *types.Placement
	var licenseSpecifications []types.LicenseConfigurationRequest

	if ec.SubnetId != "" {
		subnet = aws.String(ec.SubnetId)
	}
	if ec.SecurityGroup != "" {
		securityGroups = []string{ec.SecurityGroup}
	}
	if ec.SecurityGroupId != "" {
		securityGroupIds = []string{ec.SecurityGroupId}
	}
	if ec.InstanceProfileName != "" || ec.InstanceProfileArn != "" {
		instanceProfile = &types.IamInstanceProfileSpecification{}
		if ec.InstanceProfileName != "" {
			instanceProfile.Name = aws.String(ec.InstanceProfileName)
		}
		if ec.InstanceProfileArn != "" {
			instanceProfile.Arn = aws.String(ec.InstanceProfileArn)
		}
	}

	if ec.MaxSpotInstancePrice != "" {
		instanceMarketOpts = &types.InstanceMarketOptionsRequest{
			MarketType: types.MarketTypeSpot,
			SpotOptions: &types.SpotMarketOptions{
				MaxPrice:                     aws.String(ec.MaxSpotInstancePrice),
				InstanceInterruptionBehavior: types.InstanceInterruptionBehaviorTerminate,
				SpotInstanceType:             types.SpotInstanceTypeOneTime,
			},
		}
	}

	// Configure placement and license for dedicated hosts (e.g., Mac instances)
	// All three fields (Tenancy, HostResourceGroupArn, LicenseConfigurationArn) are validated to be set together
	if ec.Tenancy != "" {
		placement = &types.Placement{
			Tenancy:              types.Tenancy(ec.Tenancy),
			HostResourceGroupArn: aws.String(ec.HostResourceGroupArn),
		}
		licenseSpecifications = []types.LicenseConfigurationRequest{
			{LicenseConfigurationArn: aws.String(ec.LicenseConfigurationArn)},
		}
	}

	instanceTags := []types.Tag{
		{Key: aws.String(MultiPlatformManaged), Value: aws.String("true")},
		{Key: aws.String(cloud.InstanceTag), Value: aws.String(instanceTag)},
		{Key: aws.String("Name"), Value: aws.String("multi-platform-builder-" + taskRunName)},
	}
	for k, v := range additionalInstanceTags {
		instanceTags = append(instanceTags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	return &ec2.RunInstancesInput{
		KeyName:            aws.String(ec.KeyName),
		ImageId:            aws.String(ec.Ami), //ARM RHEL
		InstanceType:       types.InstanceType(ec.InstanceType),
		MinCount:           aws.Int32(1),
		MaxCount:           aws.Int32(1),
		EbsOptimized:       aws.Bool(true),
		SecurityGroups:     securityGroups,
		SecurityGroupIds:   securityGroupIds,
		IamInstanceProfile: instanceProfile,
		SubnetId:           subnet,
		UserData:           ec.UserData,
		BlockDeviceMappings: []types.BlockDeviceMapping{{
			DeviceName:  aws.String("/dev/sda1"),
			VirtualName: aws.String("ephemeral0"),
			Ebs: &types.EbsBlockDevice{
				DeleteOnTermination: aws.Bool(true),
				VolumeSize:          aws.Int32(ec.Disk),
				VolumeType:          types.VolumeTypeGp3,
				Iops:                ec.Iops,
				Throughput:          ec.Throughput,
			},
		}},
		InstanceInitiatedShutdownBehavior: types.ShutdownBehaviorTerminate,
		InstanceMarketOptions:             instanceMarketOpts,
		Placement:                         placement,
		LicenseSpecifications:             licenseSpecifications,
		TagSpecifications: []types.TagSpecification{
			{ResourceType: types.ResourceTypeInstance, Tags: instanceTags},
		},
	}, nil
}

// findInstancesWithoutTaskRuns iterates over instances retrieved from the ec cloud and returns a list of those that
// are associated with a non-existing Tekton TaskRun. Each instance should have a tag with the associated TaskRun's
// namespace and name. This is compared to existingTaskRuns, which is a map of namespaces to a list of TaskRuns in
// that namespace, to determine if this instance's TaskRun still exists.
func (ec AWSEc2DynamicConfig) findInstancesWithoutTaskRuns(log logr.Logger, reservations []types.Reservation, existingTaskRuns map[string][]string) []string {
	var instancesWithoutTaskRuns []string

	// Iterate over all VM instances
	for _, reservation := range reservations {
		for _, instance := range reservation.Instances {
			// Try to get this instance's TaskRun ID
			// **Assumes the Key & Value of the tag are non-nil
			tagIndex := slices.IndexFunc(instance.Tags, func(tag types.Tag) bool {
				return *tag.Key == cloud.TaskRunTagKey
			})
			if tagIndex == -1 {
				msg := "WARN: no taskRun ID tag found; appending to no TaskRun list anyway..."
				log.Info(msg, "instanceID", *instance.InstanceId)
				instancesWithoutTaskRuns = append(instancesWithoutTaskRuns, *instance.InstanceId)
				continue
			}

			taskRunID := *instance.Tags[tagIndex].Value
			err := cloud.ValidateTaskRunID(taskRunID)
			if err != nil {
				msg := fmt.Sprintf("WARN: invalid TaskRun ID - %s; appending to no TaskRun list anyway...", err.Error())
				log.Info(msg, "instanceID", *instance.InstanceId)
				instancesWithoutTaskRuns = append(instancesWithoutTaskRuns, *instance.InstanceId)
				continue
			}

			// Try to find this instance's TaskRun
			taskRunInfo := strings.Split(taskRunID, ":")
			taskRunNamespace, taskRunName := taskRunInfo[0], taskRunInfo[1]
			taskRuns, ok := existingTaskRuns[taskRunNamespace]
			// Add the VM instance to the no TaskRun list if the TaskRun namespace or TaskRun does not exist
			if !ok || !slices.Contains(taskRuns, taskRunName) {
				instancesWithoutTaskRuns = append(instancesWithoutTaskRuns, *instance.InstanceId)
			}
		}
	}
	return instancesWithoutTaskRuns
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

// Retrieve is one of the AWS CredentialsProvider interface's methods that uses external Kubernetes
// secrets or local environment variables to generate AWS credentials.
func (r SecretCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	// Use local environment variables for credentials
	if r.Client == nil {
		// TODO: add a check if the ENVs are empty?
		return aws.Credentials{
			AccessKeyID:     os.Getenv("MULTI_ARCH_ACCESS_KEY"),
			SecretAccessKey: os.Getenv("MULTI_ARCH_SECRET_KEY"),
			SessionToken:    os.Getenv("MULTI_ARCH_SESSION_TOKEN"),
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

	accessKeyID := string(s.Data["access-key-id"])
	secretAccessKey := string(s.Data["secret-access-key"])
	sessionToken := ""
	if sessionTokenData, ok := s.Data["session-token"]; ok {
		sessionToken = string(sessionTokenData)
	}

	return aws.Credentials{
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:    sessionToken,
	}, nil
}
