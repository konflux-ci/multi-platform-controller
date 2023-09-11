package ibm

import (
	"context"
	"crypto/md5" //#nosec
	"encoding/base64"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/stuartwdouglas/multi-arch-host-resolver/pkg/cloud"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"net"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"

	"github.com/IBM/go-sdk-core/v4/core"
	"github.com/IBM/vpc-go-sdk/vpcv1"
)

func IBMZProvider(arch string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	return IBMZDynamicConfig{
		Region:          config["dynamic."+arch+".region"],
		Key:             config["dynamic."+arch+".key"],
		Subnet:          config["dynamic."+arch+".subnet"],
		Vpc:             config["dynamic."+arch+".vpc"],
		SecurityGroup:   config["dynamic."+arch+".security-group"],
		Image:           config["dynamic."+arch+".image"],
		Secret:          config["dynamic."+arch+".secret"],
		Url:             config["dynamic."+arch+".url"],
		SystemNamespace: systemNamespace,
	}
}

func (r IBMZDynamicConfig) LaunchInstance(kubeClient client.Client, log *logr.Logger, ctx context.Context, name string) (cloud.InstanceIdentifier, error) {
	vpcService, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return "", err
	}

	binary, err := uuid.New().MarshalBinary()
	if err != nil {
		return "", err
	}
	name = "multi-" + strings.Replace(strings.ToLower(base64.URLEncoding.EncodeToString(md5.New().Sum(binary))[0:20]), "_", "-", -1) + "x" //#nosec
	truebool := true
	size := int64(20)

	vpc, err := r.lookupVpc(vpcService)
	if err != nil {
		return "", err
	}

	key, err := r.lookupSSHKey(vpcService)
	if err != nil {
		return "", err
	}

	image, err := r.lookupImage(vpcService)
	if err != nil {
		return "", err
	}
	subnet, err := r.lookupSubnet(vpcService)
	if err != nil {
		return "", err
	}
	result, response, err := vpcService.CreateInstance(&vpcv1.CreateInstanceOptions{
		InstancePrototype: &vpcv1.InstancePrototype{
			Name: &name,
			Zone: &vpcv1.ZoneIdentityByName{Name: ptr(r.Region)},
			ResourceGroup: &vpcv1.ResourceGroupIdentity{
				ID: vpc.ResourceGroup.ID,
			},
			VPC:     &vpcv1.VPCIdentityByID{ID: vpc.ID},
			Profile: &vpcv1.InstanceProfileIdentityByName{Name: ptr("bz2-1x4")},
			Keys:    []vpcv1.KeyIdentityIntf{&vpcv1.KeyIdentity{ID: key.ID}},
			BootVolumeAttachment: &vpcv1.VolumeAttachmentPrototypeInstanceByImageContext{
				DeleteVolumeOnInstanceDelete: &truebool,
				Volume: &vpcv1.VolumePrototypeInstanceByImageContext{
					Name:     ptr(name + "-volume"),
					Capacity: &size,
					Profile: &vpcv1.VolumeProfileIdentity{
						Name: ptr("general-purpose"),
					},
				},
			},
			PrimaryNetworkInterface: &vpcv1.NetworkInterfacePrototype{
				Name:            ptr("eth0"),
				PrimaryIP:       &vpcv1.NetworkInterfaceIPPrototype{AutoDelete: &truebool},
				AllowIPSpoofing: new(bool),
				Subnet:          &vpcv1.SubnetIdentityByID{ID: subnet.ID},
				SecurityGroups:  []vpcv1.SecurityGroupIdentityIntf{&vpcv1.SecurityGroupIdentityByID{ID: vpc.DefaultSecurityGroup.ID}},
			},
			Image: &vpcv1.ImageIdentityByID{ID: image.ID},
		},
	})
	log.Info(response.String())
	if err != nil {
		return "", err
	}

	return cloud.InstanceIdentifier(*result.ID), nil

}

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
func (r IBMZDynamicConfig) lookupImage(vpcService *vpcv1.VpcV1) (*vpcv1.Image, error) {
	images, _, err := vpcService.ListImages(&vpcv1.ListImagesOptions{})
	if err != nil {
		return nil, err
	}
	var image *vpcv1.Image
	for i := range images.Images {
		if *images.Images[i].Name == r.Image {
			image = &images.Images[i]
			break
		}
	}
	if image == nil {
		return nil, fmt.Errorf("failed to find image %s", r.Image)
	}
	return image, nil
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
		println("VPC: " + *vpcs.Vpcs[i].Name)
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

func ptr(s string) *string {
	return &s
}

func (r IBMZDynamicConfig) authenticate(kubeClient client.Client, ctx context.Context) (*vpcv1.VpcV1, error) {
	apiKey := ""
	if kubeClient == nil {
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
	} else {
		s := v1.Secret{}
		err := kubeClient.Get(ctx, types2.NamespacedName{Name: r.Secret, Namespace: "multi-arch-controller"}, &s)
		if err != nil {
			return nil, err
		}
		apiKey = string(s.Data["api-key"])
	}
	// Instantiate the service with an API key based IAM authenticator
	vpcService, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{
		URL: "https://us-east.iaas.cloud.ibm.com/v1",
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	})
	return vpcService, err
}

func (r IBMZDynamicConfig) GetInstanceAddress(kubeClient client.Client, log *logr.Logger, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	vpcService, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return "", err
	}
	instance, _, err := vpcService.GetInstance(&vpcv1.GetInstanceOptions{ID: ptr(string(instanceId))})
	if err != nil {
		return "", err
	}
	ips, _, err := vpcService.ListInstanceNetworkInterfaceFloatingIps(&vpcv1.ListInstanceNetworkInterfaceFloatingIpsOptions{InstanceID: instance.ID, NetworkInterfaceID: instance.PrimaryNetworkInterface.ID})

	if err != nil {
		return "", err
	}
	if len(ips.FloatingIps) > 0 {
		return checkAddressLive(*ips.FloatingIps[0].Address, log)
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
	return checkAddressLive(*ip.Address, log)
}

func checkAddressLive(addr string, log *logr.Logger) (string, error) {
	server, _ := net.ResolveTCPAddr("tcp", addr+":22")
	conn, err := net.DialTCP("tcp", nil, server)
	if err != nil {
		log.Info("Failed to connect to IBM host " + addr)
		return "", err
	}
	defer conn.Close()
	return addr, nil

}

func (r IBMZDynamicConfig) TerminateInstance(kubeClient client.Client, log *logr.Logger, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	vpcService, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return err
	}
	instance, _, err := vpcService.GetInstance(&vpcv1.GetInstanceOptions{ID: ptr(string(instanceId))})
	if err != nil {
		return err
	}
	ips, _, err := vpcService.ListInstanceNetworkInterfaceFloatingIps(&vpcv1.ListInstanceNetworkInterfaceFloatingIpsOptions{InstanceID: instance.ID, NetworkInterfaceID: instance.PrimaryNetworkInterface.ID})

	if err != nil {
		return err
	}
	for _, ip := range ips.FloatingIps {
		_, err := vpcService.DeleteFloatingIP(&vpcv1.DeleteFloatingIPOptions{ID: ip.ID})
		if err != nil {
			return err
		}
	}
	switch *instance.Status {
	case vpcv1.InstanceStatusDeletingConst:
		//already done
		return nil
	}
	_, err = vpcService.DeleteInstance(&vpcv1.DeleteInstanceOptions{ID: instance.ID})
	return err
}

type SecretCredentialsProvider struct {
	Name      string
	Namespace string
	Client    client.Client
}

type IBMZDynamicConfig struct {
	SystemNamespace string
	Secret          string
	Region          string
	Key             string
	Subnet          string
	Vpc             string
	SecurityGroup   string
	Image           string
	Url             string
}

func (r IBMZDynamicConfig) SshUser() string {
	return "root"
}
