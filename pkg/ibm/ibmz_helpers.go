package ibm

import (
	"context"
	"crypto/md5" // TODO: clarify comment -> #nosec
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// createInstanceName returns a unique instance name in the
// format  <instance_tag>-<instance_id> where the 'instance_id'
// is a 20 character universally unique ID generated using the
// md5 cryptographic hash function.
//
// Used in for Both IBM System Z & IBM Power PC.
func createInstanceName(instanceTag string) (string, error) {
	binary, err := uuid.New().MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("failed to create a UUID: %w", err)
	}
	encodedUuid := md5.New().Sum(binary)
	stringUuid := base64.URLEncoding.EncodeToString(encodedUuid)[0:20]
	instanceId := strings.Replace(strings.ToLower(stringUuid), "_", "-", -1)
	return fmt.Sprintf("%s-%sx", instanceTag, instanceId), nil //TODO: clarify comment -> #nosec
}

func ptr[V any](s V) *V {
	return &s
}

// lookUpSubnet looks for ibmz's subnet in the provided IBM Virtual Private Cloud service's API.
// Either the corresponding subnet or an error with a nil object is returned.
func (ibmz IBMZDynamicConfig) lookUpSubnet(vpcService *vpcv1.VpcV1) (*vpcv1.Subnet, error) {
	subnets, _, err := vpcService.ListSubnets(&vpcv1.ListSubnetsOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list VPC subnets: %w", err)
	}

	var subnet *vpcv1.Subnet
	for i := range subnets.Subnets {
		if *subnets.Subnets[i].Name == ibmz.Subnet {
			subnet = &subnets.Subnets[i]
			break
		}
	}
	if subnet == nil {
		return nil, fmt.Errorf("failed to find the subnet %s", ibmz.Subnet)
	}
	return subnet, nil
}

// lookUpSSHKey looks for ibmz's SSH key in the provided IBM Virtual Private Cloud service's API.
// Either the corresponding key or an error with a nil object is returned.
func (ibmz IBMZDynamicConfig) lookUpSSHKey(vpcService *vpcv1.VpcV1) (*vpcv1.Key, error) {
	keys, _, err := vpcService.ListKeys(&vpcv1.ListKeysOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list VPC keys: %w", err)
	}

	var key *vpcv1.Key
	for i := range keys.Keys {
		if *keys.Keys[i].Name == ibmz.Key {
			key = &keys.Keys[i]
			break
		}
	}
	if key == nil {
		return nil, fmt.Errorf("failed to find the SSH key %s", ibmz.Key)
	}
	return key, nil
}

// lookUpVpcNetwork looks for ibmz's VPC network in the provided IBM Virtual Private Cloud service's API.
// Either the corresponding VPC network or an error with a nil object is returned.
func (ibmz IBMZDynamicConfig) lookUpVpcNetwork(vpcService *vpcv1.VpcV1) (*vpcv1.VPC, error) {
	vpcs, _, err := vpcService.ListVpcs(&vpcv1.ListVpcsOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list VPC networks: %w", err)
	}

	var vpc *vpcv1.VPC
	for i := range vpcs.Vpcs {
		if *vpcs.Vpcs[i].Name == ibmz.Vpc {
			vpc = &vpcs.Vpcs[i]
			break
		}
	}
	if vpc == nil {
		return nil, fmt.Errorf("failed to find the VPC network %s", ibmz.Vpc)
	}
	return vpc, nil
}

// createAuthenticatedVPC generates a Virtual Private Cloud service with an API key-based IAM (Identity and Access
// Management) authenticator.
func (ibmz IBMZDynamicConfig) createAuthenticatedVPC(ctx context.Context, kubeClient client.Client) (*vpcv1.VpcV1, error) {
	apiKey := ""
	if kubeClient == nil { // Get API key from an environment variable
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
		if apiKey == "" {
			return nil, errors.New("the API key from the IBM_CLOUD_API_KEY environment variable was empty")
		}
	} else { // Get API key from the ibmz Kubernetes Secret
		secret := v1.Secret{}
		nameSpacedSecret := types2.NamespacedName{Name: ibmz.Secret, Namespace: ibmz.SystemNamespace}
		err := kubeClient.Get(ctx, nameSpacedSecret, &secret)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve the secret %s from the Kubernetes client: %w", nameSpacedSecret, err)
		}
		apiKeyByte, ok := secret.Data["api-key"]
		if !ok {
			return nil, fmt.Errorf("the secret %s did not have an API key field", nameSpacedSecret)
		}
		apiKey = string(apiKeyByte)
	}

	// Instantiate the VPC service
	vpcService, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{
		URL: ibmz.Url,
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	})
	return vpcService, err
}

// assignNetworkInterfaceFloatingIP returns an IP address that is already associated with the instance
// network interface. If no IP addresses are found, an empty string is returned.
func assignNetworkInterfaceFloatingIP(instance *vpcv1.Instance, vpcService *vpcv1.VpcV1) (ip string, err error) {
	instanceNetworkInterfaceOptions := &vpcv1.ListInstanceNetworkInterfaceFloatingIpsOptions{
		InstanceID:         instance.ID,
		NetworkInterfaceID: instance.PrimaryNetworkInterface.ID,
	}
	networkInterfaceIps, _, _ := vpcService.ListInstanceNetworkInterfaceFloatingIps(instanceNetworkInterfaceOptions)
	// Even if there is an error, don't return it TODO: clarify comment -> not permanent, this can take a while to appear
	if len(networkInterfaceIps.FloatingIps) > 0 {
		ip = *networkInterfaceIps.FloatingIps[0].Address
	}
	return ip, nil
}

// assignFloatingIP returns a floating IP address in the region. If no floating IP addresses
// are found, an empty string is returned.
func assignFloatingIP(instance *vpcv1.Instance, vpcService *vpcv1.VpcV1) (string, error) {
	floatingIps, _, err := vpcService.ListFloatingIps(&vpcv1.ListFloatingIpsOptions{ResourceGroupID: instance.ResourceGroup.ID})
	if err != nil {
		return "", fmt.Errorf("failed to retrieve any floating IP addresses: %w", err)
	}
	for _, ip := range floatingIps.FloatingIps {
		if *ip.Status == vpcv1.FloatingIPStatusAvailableConst {
			instanceNetworkIpOptions := &vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions{
				InstanceID:         instance.ID,
				NetworkInterfaceID: instance.PrimaryNetworkInterface.ID,
				ID:                 ip.ID,
			}
			_, _, err = vpcService.AddInstanceNetworkInterfaceFloatingIP(instanceNetworkIpOptions)
			if err != nil {
				return "", fmt.Errorf(
					"failed to assign the floating IP %s to the instance network interface %s: %w",
					*ip.ID,
					*instance.PrimaryNetworkInterface.ID,
					err,
				)
			}
			return *ip.Address, nil
		}
	}
	return "", nil
}

// assignFloatingIP creates and assigns an IP address to the instance network interface. A string
// version of the newly allocated IP address is returned.
func assignNewlyAllocatedIP(instance *vpcv1.Instance, vpcService *vpcv1.VpcV1) (string, error) {
	floatingIpPrototypeOptions := &vpcv1.FloatingIPPrototype{
		Zone: &vpcv1.ZoneIdentityByName{Name: ptr("us-east-2")},
		ResourceGroup: &vpcv1.ResourceGroupIdentity{
			ID: instance.ResourceGroup.ID,
		}}
	ip, _, err := vpcService.CreateFloatingIP(&vpcv1.CreateFloatingIPOptions{FloatingIPPrototype: floatingIpPrototypeOptions})
	if err != nil {
		return "", fmt.Errorf("failed to create a floating IP address for instance %s: %w", *instance.ID, err)
	}

	floatingIpNetworkInterfaceOptions := &vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions{
		InstanceID:         instance.ID,
		NetworkInterfaceID: instance.PrimaryNetworkInterface.ID,
		ID:                 ip.ID,
	}
	_, _, err = vpcService.AddInstanceNetworkInterfaceFloatingIP(floatingIpNetworkInterfaceOptions)
	if err != nil {
		return "", fmt.Errorf(
			"failed to assign the floating IP address to the instance network interface %s: %w",
			*instance.PrimaryNetworkInterface.ID,
			err,
		)
	}
	return *ip.Address, nil
}

// assignIpToInstance finds an available IP address and assigns it to the Virtual Private Cloud instance and
// its network interface. The string version of the IP address (an empty string if none was found) is returned.
func (ibmz IBMZDynamicConfig) assignIpToInstance(instance *vpcv1.Instance, vpcService *vpcv1.VpcV1) (string, error) {
	if ibmz.PrivateIP {
		for _, networkInterface := range instance.NetworkInterfaces {
			if networkInterface.PrimaryIP != nil {
				if networkInterface.PrimaryIP.Address != nil && *networkInterface.PrimaryIP.Address != "0.0.0.0" {
					return *networkInterface.PrimaryIP.Address, nil
				}
			}
		}
		return "", nil
	}

	// Try to find an already associated IP address first
	ip, err := assignNetworkInterfaceFloatingIP(instance, vpcService)
	if ip != "" || err != nil {
		return ip, err
	}

	// If no IPs associated with an instance network interface are found, ensure the VPC instance is not in an
	// undesireable state before looking for a floating IP in the region
	switch *instance.Status {
	case vpcv1.InstanceStatusDeletingConst:
		return "", fmt.Errorf("instance %s was deleted before an IP could be assigned", *instance.ID)
	case vpcv1.InstanceStatusFailedConst:
		return "", fmt.Errorf("instance %s failed before an IP could be assigned", *instance.ID)
	case vpcv1.InstanceStatusPendingConst:
		return "", nil
	case vpcv1.InstanceStatusRestartingConst:
		return "", nil
	case vpcv1.InstanceStatusStartingConst:
		return "", nil
	case vpcv1.InstanceStatusStoppedConst:
		return "", fmt.Errorf("instance %s was stopped before an IP could be assigned", *instance.ID)
	case vpcv1.InstanceStatusStoppingConst:
		return "", fmt.Errorf("instance %s is in the process of stopping and an IP cannot be assigned", *instance.ID)
	}

	// Try to find an unattached floating IP address next
	ip, err = assignFloatingIP(instance, vpcService)
	if ip != "" || err != nil {
		return ip, err
	}

	// As a last resort, allocate a new IP Address. This is very expensive, as if we allocate a new IP address,
	// we are charged for the full month TODO: clarify this portion of comment -> (60c)
	ip, err = assignNewlyAllocatedIP(instance, vpcService)
	return ip, err
}

// checkIfIpIsLive tries to resolve the IP address ip. An
// error is returned if ip couldn't be reached.
func checkIfIpIsLive(ctx context.Context, ip string) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("checking if IP address %s is live", ip))

	server, _ := net.ResolveTCPAddr("tcp", ip+":22")
	conn, err := net.DialTimeout(server.Network(), server.String(), 5*time.Second)
	if err != nil {
		log.Info("failed to connect to IBM host " + ip)
		return err
	}
	defer conn.Close()
	return nil
}
