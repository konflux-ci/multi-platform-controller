package aws

import (
	"context"
	"net"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r AwsEc2DynamicConfig) checkInstanceConnectivity(ctx context.Context, instance *types.Instance) (string, error) {
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
