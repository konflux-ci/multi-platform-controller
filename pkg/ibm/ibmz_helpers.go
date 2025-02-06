package ibm

import (
	"context"
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

func (r IBMZDynamicConfig) authenticatedService(ctx context.Context, kubeClient client.Client) (*vpcv1.VpcV1, error) {
	apiKey := ""
	if kubeClient == nil {
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
	} else {
		s := v1.Secret{}
		err := kubeClient.Get(ctx, types2.NamespacedName{Name: r.Secret, Namespace: r.SystemNamespace}, &s)
		if err != nil {
			return nil, err
		}
		apiKey = string(s.Data["api-key"])
	}
	// Instantiate the service with an API key based IAM authenticator
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
	ips, _, err := vpcService.ListInstanceNetworkInterfaceFloatingIps(&vpcv1.ListInstanceNetworkInterfaceFloatingIpsOptions{InstanceID: instance.ID, NetworkInterfaceID: instance.PrimaryNetworkInterface.ID})
	if err != nil {
		return "", nil //not permanent, this can take a while to appear
	}
	if len(ips.FloatingIps) > 0 {
		return *ips.FloatingIps[0].Address, nil
	}
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

	//we want to find an existing floating IP
	//these are expensive, as if we allocate one we are charged for the full month (60c)
	//first search for an unbound one before we allocate a new one
	existingIps, _, err := vpcService.ListFloatingIps(&vpcv1.ListFloatingIpsOptions{ResourceGroupID: instance.ResourceGroup.ID})
	if err != nil {
		return "", err
	}
	for _, ip := range existingIps.FloatingIps {
		if *ip.Status == vpcv1.FloatingIPStatusAvailableConst {
			_, _, err = vpcService.AddInstanceNetworkInterfaceFloatingIP(&vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions{InstanceID: instance.ID, NetworkInterfaceID: instance.PrimaryNetworkInterface.ID, ID: ip.ID})
			if err != nil {
				return "", err
			}
			return *ip.Address, nil
		}

	}

	//allocate a new one
	ip, _, err := vpcService.CreateFloatingIP(&vpcv1.CreateFloatingIPOptions{FloatingIPPrototype: &vpcv1.FloatingIPPrototype{
		Zone: &vpcv1.ZoneIdentityByName{Name: ptr("us-east-2")},
		ResourceGroup: &vpcv1.ResourceGroupIdentity{
			ID: instance.ResourceGroup.ID,
		}}})
	if err != nil {
		return "", err
	}
	_, _, err = vpcService.AddInstanceNetworkInterfaceFloatingIP(&vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions{InstanceID: instance.ID, NetworkInterfaceID: instance.PrimaryNetworkInterface.ID, ID: ip.ID})
	if err != nil {
		return "", err
	}
	return *ip.Address, nil
}

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
