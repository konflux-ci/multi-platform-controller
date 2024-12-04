package ibm

import (
	"context"
	"crypto/md5" //#nosec
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func IBMZProvider(arch string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	privateIp, _ := strconv.ParseBool(config["dynamic."+arch+".private-ip"])
	return IBMZDynamicConfig{
		Region:          config["dynamic."+arch+".region"],
		Key:             config["dynamic."+arch+".key"],
		Subnet:          config["dynamic."+arch+".subnet"],
		Vpc:             config["dynamic."+arch+".vpc"],
		SecurityGroup:   config["dynamic."+arch+".security-group"],
		ImageId:         config["dynamic."+arch+".image-id"],
		Secret:          config["dynamic."+arch+".secret"],
		Url:             config["dynamic."+arch+".url"],
		Profile:         config["dynamic."+arch+".profile"],
		PrivateIP:       privateIp,
		SystemNamespace: systemNamespace,
	}
}

func (r IBMZDynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, taskRunName string, instanceTag string, _ map[string]string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("provider", "ibm-z", "taskrun", taskRunName)
	log.Info("attempting to launch instance")
	vpcService, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", err
	}

	binary, err := uuid.New().MarshalBinary()
	if err != nil {
		return "", err
	}
	name := instanceTag + "-" + strings.Replace(strings.ToLower(base64.URLEncoding.EncodeToString(md5.New().Sum(binary))[0:20]), "_", "-", -1) + "x" //#nosec
	truebool := true
	size := int64(100)

	vpc, err := r.lookupVpc(vpcService)
	if err != nil {
		return "", err
	}

	key, err := r.lookupSSHKey(vpcService)
	if err != nil {
		return "", err
	}

	image := r.ImageId
	subnet, err := r.lookupSubnet(vpcService)
	if err != nil {
		return "", err
	}
	result, _, err := vpcService.CreateInstance(&vpcv1.CreateInstanceOptions{
		InstancePrototype: &vpcv1.InstancePrototype{
			Name: &name,
			Zone: &vpcv1.ZoneIdentityByName{Name: ptr(r.Region)},
			ResourceGroup: &vpcv1.ResourceGroupIdentity{
				ID: vpc.ResourceGroup.ID,
			},
			VPC:     &vpcv1.VPCIdentityByID{ID: vpc.ID},
			Profile: &vpcv1.InstanceProfileIdentityByName{Name: ptr(r.Profile)},
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
			PrimaryNetworkAttachment: &vpcv1.InstanceNetworkAttachmentPrototype{
				Name: ptr("eth0"),
				VirtualNetworkInterface: &vpcv1.InstanceNetworkAttachmentPrototypeVirtualNetworkInterface{
					AllowIPSpoofing:         new(bool),
					AutoDelete:              &truebool,
					EnableInfrastructureNat: &truebool,
					Ips:                     []vpcv1.VirtualNetworkInterfaceIPPrototypeIntf{&vpcv1.VirtualNetworkInterfaceIPPrototype{AutoDelete: &truebool}},
					PrimaryIP:               &vpcv1.VirtualNetworkInterfacePrimaryIPPrototype{AutoDelete: &truebool},
					Subnet:                  &vpcv1.SubnetIdentityByID{ID: subnet.ID},
					SecurityGroups:          []vpcv1.SecurityGroupIdentityIntf{&vpcv1.SecurityGroupIdentityByID{ID: vpc.DefaultSecurityGroup.ID}},
				},
			},
			AvailabilityPolicy: &vpcv1.InstanceAvailabilityPolicyPrototype{HostFailure: ptr("stop")},
			Image:              &vpcv1.ImageIdentityByID{ID: &image},
		},
	})
	if err != nil {
		return "", err
	}

	return cloud.InstanceIdentifier(*result.ID), nil

}

func (r IBMZDynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("provider", "ibm-z")
	log.Info("attempting to count instances")
	vpcService, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return 0, err
	}

	vpc, err := r.lookupVpc(vpcService)
	if err != nil {
		return 0, err
	}
	instances, _, err := vpcService.ListInstances(&vpcv1.ListInstancesOptions{ResourceGroupID: vpc.ResourceGroup.ID, VPCName: &r.Vpc})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, instance := range instances.Instances {
		if strings.HasPrefix(*instance.Name, instanceTag) {
			count++
		}
	}
	log.Info("Instances count done", "count", count)
	return count, nil
}

func (r IBMZDynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("provider", "ibm-z")
	log.Info("Listing instances", "tag", instanceTag)
	vpcService, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return nil, err
	}

	vpc, err := r.lookupVpc(vpcService)
	if err != nil {
		return nil, err
	}
	instances, _, err := vpcService.ListInstances(&vpcv1.ListInstancesOptions{ResourceGroupID: vpc.ResourceGroup.ID, VPCName: &r.Vpc})
	if err != nil {
		return nil, err
	}
	ret := []cloud.CloudVMInstance{}
	for _, instance := range instances.Instances {
		if strings.HasPrefix(*instance.Name, instanceTag) {
			addr, err := r.instanceIP(ctx, &instance, kubeClient)
			if err != nil {
				log.Info("not listing instance as address cannot be assigned yet", "instance", *instance.ID, "message", err.Error())
				continue
			}
			if err := checkAddressLive(ctx, addr); err != nil {
				log.Info("not listing instance as address cannot be accessed yet", "instance", *instance.ID, "message", err.Error())
				continue
			}
			ret = append(ret, cloud.CloudVMInstance{InstanceId: cloud.InstanceIdentifier(*instance.ID), Address: addr, StartTime: time.Time(*instance.CreatedAt)})
		}
	}
	log.Info("Listing instances done.", "count", len(ret))
	return ret, nil
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

func ptr(s string) *string {
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

func (r IBMZDynamicConfig) GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("provider", "ibm-z", "instanceId", instanceId)
	vpcService, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", err
	}
	instance, _, err := vpcService.GetInstance(&vpcv1.GetInstanceOptions{ID: ptr(string(instanceId))})
	if err != nil {
		return "", nil //not permanent, this can take a while to appear
	}

	ip, err := r.instanceIP(ctx, instance, kubeClient)
	if err != nil {
		log.Info("Failed to lookup IP", "error", err.Error())
		return "", err
	}
	if ip != "" {
		if err = checkAddressLive(ctx, ip); err != nil {
			return "", nil
		}
	}
	return ip, nil
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
	log := logr.FromContextOrDiscard(ctx).WithValues("provider", "ibm-z")
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

func (r IBMZDynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx).WithValues("provider", "ibm-p", "instanceId", instanceId)
	log.Info("attempting to terminate server")
	timeout := time.Now().Add(time.Minute * 10)
	go func() {
		vpcService, err := r.authenticatedService(context.Background(), kubeClient)
		if err != nil {
			return
		}
		for {
			instance, _, err := vpcService.GetInstance(&vpcv1.GetInstanceOptions{ID: ptr(string(instanceId))})
			if err != nil {
				log.Error(err, "failed to delete instance, unable to get instance")
				return
			}
			switch *instance.Status {
			case vpcv1.InstanceStatusDeletingConst:
				//already done
				return
			case vpcv1.InstanceStatusPendingConst:
				//pending instances don't delete properly
				time.Sleep(time.Second * 10)
				continue
			}
			_, err = vpcService.DeleteInstance(&vpcv1.DeleteInstanceOptions{ID: instance.ID})
			if err != nil {
				log.Error(err, "failed to delete instance")
			}
			if timeout.Before(time.Now()) {
				return
			}
			time.Sleep(time.Second * 10)
		}
	}()

	return nil
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
	ImageId         string
	Url             string
	Profile         string
	PrivateIP       bool
}

func (r IBMZDynamicConfig) SshUser() string {
	return "root"
}
