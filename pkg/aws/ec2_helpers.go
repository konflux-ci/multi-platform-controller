package aws

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// checkInstanceConnectivity returns instance's IP address if it can be resolved.
func (r AwsEc2DynamicConfig) checkInstanceConnectivity(ctx context.Context, instance *types.Instance) (string, error) {
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
