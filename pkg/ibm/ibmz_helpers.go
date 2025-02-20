package ibm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// lookupSubnet looks for ibmz's subnet in the provided IBM Virtual Private Cloud service's API.
// Either the corresponding subnet or an error with a nil object is returned.
func (r IBMZDynamicConfig) lookupSubnet(vpcService *vpcv1.VpcV1) (*vpcv1.Subnet, error) {
	subnets, _, err := vpcService.ListSubnets(&vpcv1.ListSubnetsOptions{})
	if err != nil {
		return nil, err
	}

	var subnet *vpcv1.Subnet
	for i := range subnets.Subnets {
		if *subnets.Subnets[i].Name == r.Subnet {
			subnet = &subnets.Subnets[i]
			break
		}
	}
	if subnet == nil {
		return nil, fmt.Errorf("failed to find subnet %s", r.Subnet)
	}
	return subnet, nil
}

// lookupSSHKey looks for ibmz's SSH key in the provided IBM Virtual Private Cloud service's API.
// Either the corresponding key or an error with a nil object is returned.
func (r IBMZDynamicConfig) lookupSSHKey(vpcService *vpcv1.VpcV1) (*vpcv1.Key, error) {
	keys, _, err := vpcService.ListKeys(&vpcv1.ListKeysOptions{})
	if err != nil {
		return nil, err
	}

	var key *vpcv1.Key
	for i := range keys.Keys {
		if *keys.Keys[i].Name == r.Key {
			key = &keys.Keys[i]
			break
		}
	}
	if key == nil {
		return nil, fmt.Errorf("failed to find SSH key %s", r.Key)
	}
	return key, nil
}

// lookUpVpc looks for ibmz's VPC network in the provided IBM Virtual Private Cloud service's API.
// Either the corresponding VPC network or an error with a nil object is returned.
func (r IBMZDynamicConfig) lookupVpc(vpcService *vpcv1.VpcV1) (*vpcv1.VPC, error) {
	vpcs, _, err := vpcService.ListVpcs(&vpcv1.ListVpcsOptions{})
	if err != nil {
		return nil, err
	}

	var vpc *vpcv1.VPC
	for i := range vpcs.Vpcs {
		//println("VPC: " + *vpcs.Vpcs[i].Name)
		if *vpcs.Vpcs[i].Name == r.Vpc {
			vpc = &vpcs.Vpcs[i]
			break
		}
	}
	if vpc == nil {
		return nil, fmt.Errorf("failed to find VPC %s", r.Vpc)
	}
	return vpc, nil
}

func ptr[V any](s V) *V {
	return &s
}

// authenticatedService generates a Virtual Private Cloud service with an API key-based IAM (Identity and Access
// Management) authenticator.
func (r IBMZDynamicConfig) authenticatedService(ctx context.Context, kubeClient client.Client) (*vpcv1.VpcV1, error) {
	apiKey := ""
	if kubeClient == nil { // Get API key from an environment variable
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
		if apiKey == "" {
			return nil, errors.New("the API key from the IBM_CLOUD_API_KEY environment variable was empty")
		}
	} else { // Get API key from the ibmz Kubernetes Secret
		s := v1.Secret{}
		nameSpacedSecret := types2.NamespacedName{Name: r.Secret, Namespace: r.SystemNamespace}
		err := kubeClient.Get(ctx, nameSpacedSecret, &s)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve the secret %s from the Kubernetes client: %w", nameSpacedSecret, err)
		}

		apiKeyByte, ok := s.Data["api-key"]
		if !ok {
			return nil, fmt.Errorf("the secret %s did not have an API key field", nameSpacedSecret)
		}
		apiKey = string(apiKeyByte)
	}

	// Instantiate the VPC service
	vpcService, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{
		URL: r.Url,
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	})
	return vpcService, err
}

func (r IBMZDynamicConfig) instanceIP(ctx context.Context, instance *vpcv1.Instance, kubeClient client.Client) (string, error) {

	if r.PrivateIP {
		for _, i := range instance.NetworkInterfaces {
			if i.PrimaryIP != nil && i.PrimaryIP.Address != nil && *i.PrimaryIP.Address != "0.0.0.0" {
				return *i.PrimaryIP.Address, nil
			}
		}
		return "", nil
	}

	vpcService, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", err
	}

	// Try to get an IP address that is already associated with the instance
	// network interface.
	ips, _, err := vpcService.ListInstanceNetworkInterfaceFloatingIps(
		&vpcv1.ListInstanceNetworkInterfaceFloatingIpsOptions{
			InstanceID:         instance.ID,
			NetworkInterfaceID: instance.PrimaryNetworkInterface.ID,
		},
	)
	if err != nil {
		return "", nil // TODO: clarify comment -> not permanent, this can take a while to appear
	}
	if len(ips.FloatingIps) > 0 {
		return *ips.FloatingIps[0].Address, nil
	}

	// If no IPs associated with an instance network interface are found, ensure the VPC instance is not in an
	// undesireable state before looking for a floating IP in the region
	switch *instance.Status {
	case vpcv1.InstanceStatusDeletingConst:
		return "", fmt.Errorf("instance was deleted")
	case vpcv1.InstanceStatusFailedConst:
		return "", fmt.Errorf("instance failed")
	case vpcv1.InstanceStatusPendingConst:
		return "", nil
	case vpcv1.InstanceStatusRestartingConst:
		return "", nil
	case vpcv1.InstanceStatusStartingConst:
		return "", nil
	case vpcv1.InstanceStatusStoppedConst:
		return "", fmt.Errorf("instance was stopped")
	case vpcv1.InstanceStatusStoppingConst:
		return "", fmt.Errorf("instance was stopping")
	}

	// Try to find a floating IP address in the region.
	existingIps, _, err := vpcService.ListFloatingIps(&vpcv1.ListFloatingIpsOptions{ResourceGroupID: instance.ResourceGroup.ID})
	if err != nil {
		return "", err
	}
	for _, ip := range existingIps.FloatingIps {
		if *ip.Status == vpcv1.FloatingIPStatusAvailableConst {
			_, _, err = vpcService.AddInstanceNetworkInterfaceFloatingIP(
				&vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions{
					InstanceID:         instance.ID,
					NetworkInterfaceID: instance.PrimaryNetworkInterface.ID,
					ID:                 ip.ID,
				})
			if err != nil {
				return "", err
			}
			return *ip.Address, nil
		}

	}

	// As a last resort, allocate a new IP Address. This is very expensive, as if we allocate a new IP address,
	// we are charged for the full month TODO: clarify this portion of comment -> (60c)
	ip, _, err := vpcService.CreateFloatingIP(&vpcv1.CreateFloatingIPOptions{FloatingIPPrototype: &vpcv1.FloatingIPPrototype{
		Zone: &vpcv1.ZoneIdentityByName{Name: ptr("us-east-2")},
		ResourceGroup: &vpcv1.ResourceGroupIdentity{
			ID: instance.ResourceGroup.ID,
		}}})
	if err != nil {
		return "", err
	}
	_, _, err = vpcService.AddInstanceNetworkInterfaceFloatingIP(
		&vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions{
			InstanceID: instance.ID, NetworkInterfaceID: instance.PrimaryNetworkInterface.ID,
			ID: ip.ID,
		})
	if err != nil {
		return "", err
	}
	return *ip.Address, nil
}

// checkIfIpIsLive tries to resolve the IP address ip. An
// error is returned if ip couldn't be reached.
func checkAddressLive(ctx context.Context, addr string) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("checking if address %s is live", addr))

	server, _ := net.ResolveTCPAddr("tcp", addr+":22")
	conn, err := net.DialTimeout(server.Network(), server.String(), 5*time.Second)
	if err != nil {
		log.Info("failed to connect to IBM host " + addr)
		return err
	}
	defer conn.Close()
	return nil

}
