package ibm

import (
	"context"
	// #nosec is added to bypass the golang security scan since the cryptographic
	// strength doesn't matter here
	"crypto/md5" //#nosec
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
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

// IBMZProvider returns an IBM System Z cloud configuration that implements the CloudProvider interface.
func IBMZProvider(arch string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	privateIp, _ := strconv.ParseBool(config["dynamic."+arch+".private-ip"])
	volumeSize, err := strconv.Atoi(config["dynamic."+arch+".disk"])
	if err != nil {
		volumeSize = 100
	}
	return IBMZDynamicConfig{
		Region:          config["dynamic."+arch+".region"],
		Key:             config["dynamic."+arch+".key"],
		Subnet:          config["dynamic."+arch+".subnet"],
		Vpc:             config["dynamic."+arch+".vpc"],
		ImageId:         config["dynamic."+arch+".image-id"],
		Secret:          config["dynamic."+arch+".secret"],
		Url:             config["dynamic."+arch+".url"],
		Profile:         config["dynamic."+arch+".profile"],
		PrivateIP:       privateIp,
		Disk:            volumeSize,
		SystemNamespace: systemNamespace,
	}
}

// LaunchInstance creates a System Z Virtual Server instance and returns its identifier. This function is implemented as
// part of the CloudProvider interface, which is why some of the arguments are unused for this particular implementation.
func (r IBMZDynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, taskRunName string, instanceTag string, _ map[string]string) (cloud.InstanceIdentifier, error) {
	vpcService, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", fmt.Errorf("failed to create an authenticated VPC service: %w", err)
	}

	binary, err := uuid.New().MarshalBinary()
	if err != nil {
		return "", err
	}
	// #nosec is added to bypass the golang security scan since the cryptographic
	// strength doesn't matter here
	md5EncodedBinary := md5.New().Sum(binary) //#nosec
	md5EncodedString := base64.URLEncoding.EncodeToString(md5EncodedBinary)[0:20]
	name := instanceTag + "-" + strings.Replace(strings.ToLower(md5EncodedString), "_", "-", -1) + "x"

	// Gather required information for the VPC instance
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
				DeleteVolumeOnInstanceDelete: ptr(true),
				Volume: &vpcv1.VolumePrototypeInstanceByImageContext{
					Name:     ptr(name + "-volume"),
					Capacity: ptr(int64(r.Disk)),
					Profile: &vpcv1.VolumeProfileIdentity{
						Name: ptr("general-purpose"),
					},
				},
			},
			PrimaryNetworkAttachment: &vpcv1.InstanceNetworkAttachmentPrototype{
				Name: ptr("eth0"),
				VirtualNetworkInterface: &vpcv1.InstanceNetworkAttachmentPrototypeVirtualNetworkInterface{
					AllowIPSpoofing:         new(bool),
					AutoDelete:              ptr(true),
					EnableInfrastructureNat: ptr(true),
					Ips: []vpcv1.VirtualNetworkInterfaceIPPrototypeIntf{
						&vpcv1.VirtualNetworkInterfaceIPPrototype{AutoDelete: ptr(true)},
					},
					PrimaryIP: &vpcv1.VirtualNetworkInterfacePrimaryIPPrototype{AutoDelete: ptr(true)},
					Subnet:    &vpcv1.SubnetIdentityByID{ID: subnet.ID},
					SecurityGroups: []vpcv1.SecurityGroupIdentityIntf{
						&vpcv1.SecurityGroupIdentityByID{ID: vpc.DefaultSecurityGroup.ID},
					},
				},
			},
			AvailabilityPolicy: &vpcv1.InstanceAvailabilityPolicyPrototype{HostFailure: ptr("stop")},
			Image:              &vpcv1.ImageIdentityByID{ID: &image},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create the System Z virtual server instance %s: %w", name, err)
	}

	return cloud.InstanceIdentifier(*result.ID), nil

}

// CountInstances returns the number of System Z virtual server instances whose names start with instanceTag.
func (r IBMZDynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	vpcService, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return -1, fmt.Errorf("failed to create an authenticated VPC service: %w", err)
	}

	vpc, err := r.lookupVpc(vpcService)
	if err != nil {
		return -1, err
	}
	instances, _, err := vpcService.ListInstances(&vpcv1.ListInstancesOptions{ResourceGroupID: vpc.ResourceGroup.ID, VPCName: &r.Vpc})
	if err != nil {
		return -1, fmt.Errorf("failed to list virtual server instances in VPC network %s: %w", r.Vpc, err)
	}
	count := 0
	for _, instance := range instances.Instances {
		if strings.HasPrefix(*instance.Name, instanceTag) {
			count++
		}
	}
	return count, nil
}

// ListInstances returns a collection of accessible System Z virtual server instances whose names start with instanceTag.
func (r IBMZDynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	vpcService, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create an authenticated VPC service: %w", err)
	}

	vpc, err := r.lookupVpc(vpcService)
	if err != nil {
		return nil, err
	}

	instances, _, err := vpcService.ListInstances(&vpcv1.ListInstancesOptions{ResourceGroupID: vpc.ResourceGroup.ID, VPCName: &r.Vpc})
	if err != nil {
		return nil, fmt.Errorf("failed to list VPC instances in the VPC network %s: %w", *vpc.ID, err)
	}

	ret := []cloud.CloudVMInstance{}
	log := logr.FromContextOrDiscard(ctx)

	// Ensure all listed instances have a reachable IP address
	for _, instance := range instances.Instances {
		if strings.HasPrefix(*instance.Name, instanceTag) {
			addr, err := r.instanceIP(ctx, &instance, kubeClient)
			if err != nil {
				log.Error(err, "not listing instance as address cannot be assigned yet", "instance", *instance.ID)
				continue
			}
			if err := checkAddressLive(ctx, addr); err != nil {
				log.Error(err, "not listing instance as address cannot be accessed yet", "instance", *instance.ID)
				continue
			}
			ret = append(ret, cloud.CloudVMInstance{
				InstanceId: cloud.InstanceIdentifier(*instance.ID),
				Address:    addr,
				StartTime:  time.Time(*instance.CreatedAt),
			})
		}
	}
	return ret, nil
}

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

// GetInstanceAddress returns the IP Address associated with the instanceID System Z virtual server instance.
func (r IBMZDynamicConfig) GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	log := logr.FromContextOrDiscard(ctx)
	vpcService, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", err
	}

	instance, _, err := vpcService.GetInstance(&vpcv1.GetInstanceOptions{ID: ptr(string(instanceId))})
	if err != nil {
		return "", nil // TODO: clarify comment -> not permanent, this can take a while to appear
	}

	ip, err := r.instanceIP(ctx, instance, kubeClient)
	if err != nil {
		log.Error(err, "Failed to lookup IP", "instanceId", instanceId, "error", err.Error())
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

// TerminateInstance tries to delete a specific System Z virtual server instance for 10 minutes or until the instance is deleted.
func (r IBMZDynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx)
	vpcService, err := r.authenticatedService(context.Background(), kubeClient)
	if err != nil {
		return err
	}

	// Iterate for 10 minutes
	timeout := time.Now().Add(time.Minute * 10)
	go func() {
		repeats := 0
		localCtx := context.WithoutCancel(ctx)
		for {
			instance, resp, err := vpcService.GetInstanceWithContext(localCtx, &vpcv1.GetInstanceOptions{ID: ptr(string(instanceId))})
			if err != nil {
				// Log an error if it's the first iteration or there is a non-404 code response
				if repeats == 0 || (resp != nil && resp.StatusCode != http.StatusNotFound) {
					log.Error(err, "failed to delete System Z instance, unable to get instance", "instanceId", *instance.ID)
				}
				return
			}

			switch *instance.Status {
			// Instance is already being deleted
			case vpcv1.InstanceStatusDeletingConst:
				return
			// Instance won't delete properly because its pending
			case vpcv1.InstanceStatusPendingConst:
				time.Sleep(time.Second * 10)
				continue
			}

			_, err = vpcService.DeleteInstanceWithContext(localCtx, &vpcv1.DeleteInstanceOptions{ID: instance.ID})
			if err != nil {
				log.Error(err, "failed to VPC instance", "instanceID", *instance.ID)
			}
			if timeout.Before(time.Now()) {
				return
			}
			repeats++
			// Sleep 10 seconds between each execution
			time.Sleep(time.Second * 10)
		}
	}()

	return nil
}

func (r IBMZDynamicConfig) SshUser() string {
	return "root"
}

// An IBMZDynamicConfig represents a configuration for an IBM System Z virtual server
// instance in an IBM Virtual Private Cloud.  The zero value (where each field will
// be assigned its type's zero value) is not a valid IBMZDynamicConfig.
type IBMZDynamicConfig struct {
	// SystemNamespace is the name of the Kubernetes namespace where the specified
	// secrets are stored.
	SystemNamespace string

	// Secret is the name of the Kubernetes ExternalSecret resource to use to
	// connect and authenticate with the IBM cloud service.
	Secret string

	// Region is the geographcal area where the data center hosting the cloud
	// instance is to be located. See [regions](https://cloud.ibm.com/docs/overview?topic=overview-locations)
	// for a list of valid region identifiers.
	Region string

	// Key is the name of the public SSH key to be used when creating the cloud
	// instance.
	Key string

	// Subnet is the name of the subnet to be used when creating the cloud instance.
	Subnet string

	// Vpc is the Virtual Private Cloud network to be used when creating the cloud.
	// instance.
	Vpc string

	// ImageId is the image to use when creating the cloud instance.
	ImageId string

	// Url is the url to use when creating a Virtual Private Cloud service for
	// the cloud instance.
	Url string

	// Profile is the name of the [profile](https://cloud.ibm.com/docs/vpc?topic=vpc-profiles)
	// to use when creating the cloud instance.
	Profile string

	// Disk is the amount of permanent storage (in GB) allocated to the cloud instance.
	Disk int

	// PrivateIP is whether the cloud instance will use an IP address provided by the
	// associated Virtual Private Cloud service.
	PrivateIP bool
}
