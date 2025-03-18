package ibm

import (
	"context"
	// #nosec is added to bypass the golang security scan since the cryptographic
	// strength doesn't matter here
	"crypto/md5" //#nosec
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const maxS390NameLength = 63

// CreateIbmZCloudConfig returns an IBM System Z cloud configuration that implements the CloudProvider interface.
func CreateIbmZCloudConfig(arch string, config map[string]string, systemNamespace string) cloud.CloudProvider {
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
func (ibmz IBMZDynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, taskRunName string, instanceTag string, _ map[string]string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx)
	vpcService, err := ibmz.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", fmt.Errorf("failed to create an authenticated VPC service: %w", err)
	}

	binary, err := uuid.New().MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("failed to create a UUID for the instance name: %w", err)
	}
	// #nosec is added to bypass the golang security scan since the cryptographic
	// strength doesn't matter here
	md5EncodedBinary := md5.New().Sum(binary) //#nosec
	md5EncodedString := base64.URLEncoding.EncodeToString(md5EncodedBinary)[0:20]
	instanceName := instanceTag + "-" + strings.Replace(strings.ToLower(md5EncodedString), "_", "-", -1) + "x"
	// workaround to avoid BadRequest-s, after config validation introduced that might be not an issue anymore
	if len(instanceName) > maxS390NameLength {
		log.Info("WARN: generated name too long. Instance tag need to be shortened. Truncating to the max possible length.", "tag", instanceTag)
		instanceName = instanceName[:maxS390NameLength]
	}

	// Gather required information for the VPC instance
	vpc, err := ibmz.lookupVpc(vpcService)
	if err != nil {
		return "", err
	}

	key, err := ibmz.lookupSSHKey(vpcService)
	if err != nil {
		return "", err
	}

	image := ibmz.ImageId
	subnet, err := ibmz.lookupSubnet(vpcService)
	if err != nil {
		return "", err
	}

	vpcInstance, _, err := vpcService.CreateInstance(&vpcv1.CreateInstanceOptions{
		InstancePrototype: &vpcv1.InstancePrototype{
			Name: &instanceName,
			Zone: &vpcv1.ZoneIdentityByName{Name: ptr(ibmz.Region)},
			ResourceGroup: &vpcv1.ResourceGroupIdentity{
				ID: vpc.ResourceGroup.ID,
			},
			VPC:     &vpcv1.VPCIdentityByID{ID: vpc.ID},
			Profile: &vpcv1.InstanceProfileIdentityByName{Name: ptr(ibmz.Profile)},
			Keys:    []vpcv1.KeyIdentityIntf{&vpcv1.KeyIdentity{ID: key.ID}},
			BootVolumeAttachment: &vpcv1.VolumeAttachmentPrototypeInstanceByImageContext{
				DeleteVolumeOnInstanceDelete: ptr(true),
				Volume: &vpcv1.VolumePrototypeInstanceByImageContext{
					Name:     ptr(instanceName + "-volume"),
					Capacity: ptr(int64(ibmz.Disk)),
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
		return "", fmt.Errorf("failed to create the System Z virtual server instance %s: %w", instanceName, err)
	}

	return cloud.InstanceIdentifier(*vpcInstance.ID), nil

}

// CountInstances returns the number of System Z virtual server instances whose names start with instanceTag.
func (ibmz IBMZDynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	vpcService, err := ibmz.authenticatedService(ctx, kubeClient)
	if err != nil {
		return -1, fmt.Errorf("failed to create an authenticated VPC service: %w", err)
	}

	vpc, err := ibmz.lookupVpc(vpcService)
	if err != nil {
		return -1, err
	}
	instances, _, err := vpcService.ListInstances(&vpcv1.ListInstancesOptions{ResourceGroupID: vpc.ResourceGroup.ID, VPCName: &ibmz.Vpc})
	if err != nil {
		return -1, fmt.Errorf("failed to list virtual server instances in VPC network %s: %w", ibmz.Vpc, err)
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
func (ibmz IBMZDynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	vpcService, err := ibmz.authenticatedService(ctx, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create an authenticated VPC service: %w", err)
	}

	vpc, err := ibmz.lookupVpc(vpcService)
	if err != nil {
		return nil, err
	}

	vpcInstances, _, err := vpcService.ListInstances(&vpcv1.ListInstancesOptions{ResourceGroupID: vpc.ResourceGroup.ID, VPCName: &ibmz.Vpc})
	if err != nil {
		return nil, fmt.Errorf("failed to list VPC instances in the VPC network %s: %w", *vpc.ID, err)
	}

	vmInstances := []cloud.CloudVMInstance{}
	log := logr.FromContextOrDiscard(ctx)

	// Ensure all listed instances have a reachable IP address
	for _, instance := range vpcInstances.Instances {
		if strings.HasPrefix(*instance.Name, instanceTag) {
			addr, err := ibmz.instanceIP(ctx, &instance, kubeClient)
			if err != nil {
				log.Error(err, "not listing instance as IP address cannot be assigned yet", "instance", *instance.ID)
				continue
			}
			if err := checkAddressLive(ctx, addr); err != nil {
				log.Error(err, "not listing instance as IP address cannot be accessed yet", "instance", *instance.ID)
				continue
			}
			newVmInstance := cloud.CloudVMInstance{
				InstanceId: cloud.InstanceIdentifier(*instance.ID),
				Address:    addr,
				StartTime:  time.Time(*instance.CreatedAt),
			}
			vmInstances = append(vmInstances, newVmInstance)
		}
	}
	log.Info("Finished listing VPC instances.", "count", len(vmInstances))
	return vmInstances, nil
}

// GetInstanceAddress returns the IP Address associated with the instanceID System Z virtual server instance.
func (ibmz IBMZDynamicConfig) GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	log := logr.FromContextOrDiscard(ctx)
	vpcService, err := ibmz.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", fmt.Errorf("failed to create an authenticated VPC service: %w", err)
	}

	instance, _, err := vpcService.GetInstance(&vpcv1.GetInstanceOptions{ID: ptr(string(instanceId))})
	if err != nil {
		return "", nil // TODO: clarify comment -> not permanent, this can take a while to appear
	}

	ip, err := ibmz.instanceIP(ctx, instance, kubeClient)
	if err != nil {
		log.Error(err, "failed to lookup IP address", "instanceId", instanceId)
		return "", fmt.Errorf("failed to look up/assign an IP address for instance %s: %w", instanceId, err)
	}
	if ip != "" {
		if err = checkAddressLive(ctx, ip); err != nil {
			return "", nil
		}
	}
	return ip, nil
}

// TerminateInstance tries to delete a specific System Z virtual server instance for 10 minutes or until the instance is deleted.
func (ibmz IBMZDynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx)
	vpcService, err := ibmz.authenticatedService(context.Background(), kubeClient)
	if err != nil {
		return fmt.Errorf("failed to create an authenticated VPC service: %w", err)
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
					log.Error(err, "failed to delete System Z instance; unable to get instance", "instanceId", instanceId)
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
				log.Error(err, "failed to System Z instance", "instanceID", *instance.ID)
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

func (ibmz IBMZDynamicConfig) SshUser() string {
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
