package aws

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	"github.com/stuartwdouglas/multi-arch-host-resolver/pkg/cloud"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"net"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func Ec2Provider(arch string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	return AwsDynamicConfig{Region: config["dynamic."+arch+".region"],
		Ami:             config["dynamic."+arch+".ami"],
		InstanceType:    config["dynamic."+arch+".instance-type"],
		KeyName:         config["dynamic."+arch+".key-name"],
		Secret:          config["dynamic."+arch+".aws-secret"],
		SystemNamespace: systemNamespace,
	}
}

func (configMapInfo AwsDynamicConfig) LaunchInstance(kubeClient client.Client, log *logr.Logger, ctx context.Context, name string) (cloud.InstanceIdentifier, error) {
	// Load AWS credentials and configuration

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(SecretCredentialsProvider{Name: configMapInfo.Secret, Namespace: "multi-arch-controller", Client: kubeClient}),
		config.WithRegion(configMapInfo.Region))
	if err != nil {
		return "", err
	}

	// Create an EC2 client
	ec2Client := ec2.NewFromConfig(cfg)

	// Specify the parameters for the new EC2 instance
	launchInput := &ec2.RunInstancesInput{
		KeyName:        aws.String(configMapInfo.KeyName),
		ImageId:        aws.String(configMapInfo.Ami), //ARM RHEL
		InstanceType:   types.InstanceType(configMapInfo.InstanceType),
		MinCount:       aws.Int32(1),
		MaxCount:       aws.Int32(1),
		EbsOptimized:   aws.Bool(true),
		SecurityGroups: []string{"launch-wizard-1"},
		BlockDeviceMappings: []types.BlockDeviceMapping{{
			DeviceName:  aws.String("/dev/sda1"),
			VirtualName: aws.String("ephemeral0"),
			Ebs:         &types.EbsBlockDevice{VolumeSize: aws.Int32(40)},
		}},
		InstanceInitiatedShutdownBehavior: types.ShutdownBehaviorTerminate,
		TagSpecifications:                 []types.TagSpecification{{ResourceType: types.ResourceTypeInstance, Tags: []types.Tag{{Key: aws.String("multi-arch-builder"), Value: aws.String("true")}, {Key: aws.String("Name"), Value: aws.String(name)}}}},
	}

	// Launch the new EC2 instance
	result, err := ec2Client.RunInstances(context.TODO(), launchInput)
	if err != nil {
		return "", err
	}

	// The result will contain information about the newly created instance(s)
	if len(result.Instances) > 0 {
		//hard coded 10m timeout
		return cloud.InstanceIdentifier(*result.Instances[0].InstanceId), nil
	} else {
		return "", fmt.Errorf("no instances were created")
	}
}

func (configMapInfo AwsDynamicConfig) GetInstanceAddress(kubeClient client.Client, log *logr.Logger, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	// Load AWS credentials and configuration

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(SecretCredentialsProvider{Name: configMapInfo.Secret, Namespace: configMapInfo.SystemNamespace, Client: kubeClient}),
		config.WithRegion(configMapInfo.Region))
	if err != nil {
		return "", err
	}

	// Create an EC2 client
	ec2Client := ec2.NewFromConfig(cfg)
	res, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{string(instanceId)}})
	if err != nil {
		log.Error(err, "failed to describe instance")
		return "", err
	}
	if len(res.Reservations) > 0 {
		if len(res.Reservations[0].Instances) > 0 {
			if res.Reservations[0].Instances[0].PublicDnsName != nil && *res.Reservations[0].Instances[0].PublicDnsName != "" {

				server, _ := net.ResolveTCPAddr("tcp", *res.Reservations[0].Instances[0].PublicDnsName+":22")
				conn, err := net.DialTCP("tcp", nil, server)
				if err != nil {
					log.Error(err, "failed to connect to AWS instance")
					return "", err
				}
				defer conn.Close()

				return *res.Reservations[0].Instances[0].PublicDnsName, nil
			}
		}
	}
	return "", nil
}

func (configMapInfo AwsDynamicConfig) TerminateInstance(kubeClient client.Client, log *logr.Logger, ctx context.Context, instance cloud.InstanceIdentifier) error {

	// Load AWS credentials and configuration

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(SecretCredentialsProvider{Name: configMapInfo.Secret, Namespace: "multi-arch-controller", Client: kubeClient}),
		config.WithRegion(configMapInfo.Region))
	if err != nil {
		return err
	}

	ec2Client := ec2.NewFromConfig(cfg)
	_, err = ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{string(instance)}})
	return err
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
	Region          string
	Ami             string
	InstanceType    string
	KeyName         string
	Secret          string
	SystemNamespace string
}

func (configMapInfo AwsDynamicConfig) SshUser() string {
	return "ec2-user"
}
