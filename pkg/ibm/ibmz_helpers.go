package ibm

import (
	"context"
	"slices"

	// #nosec is added to bypass the golang security scan since the cryptographic
	// strength doesn't matter here
	"crypto/md5" //#nosec
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
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

// createInstanceName returns a unique instance name in the
// format  <instance_tag>-<instance_id> where the 'instance_id'
// is a 20 character universally unique ID generated using the
// md5 cryptographic hash function.
//
// The instanceTag is sanitized to comply with IBM Cloud instance naming requirements:
// uppercase letters are converted to lowercase, and underscores are replaced with hyphens.
// Special characters (other than alphanumeric, hyphens, and underscores) are not allowed.
//
// Used in for Both IBM System Z & IBM Power PC.
func createInstanceName(instanceTag string) (string, error) {
	// Validate instanceTag contains only alphanumeric characters, hyphens, and underscores
	validNamePattern := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !validNamePattern.MatchString(instanceTag) {
		return "", fmt.Errorf("instance tag contains invalid characters, only alphanumeric characters, hyphens, and underscores are allowed, got: %s", instanceTag)
	}

	binary, err := uuid.New().MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("failed to create a UUID for the instance name: %w", err)
	}
	// #nosec is added to bypass the golang security scan since the cryptographic
	// strength doesn't matter here
	md5EncodedBinary := md5.New().Sum(binary) //#nosec
	md5EncodedString := base64.URLEncoding.EncodeToString(md5EncodedBinary)[0:20]

	// Build instance name and sanitize: lowercase and replace underscores with hyphens
	instanceName := fmt.Sprintf("%s-%sx", instanceTag, md5EncodedString)
	return strings.ReplaceAll(strings.ToLower(instanceName), "_", "-"), nil
}

// checkIfIpIsLive tries to connect to the IP address ip. An
// error is returned if ip couldn't be reached.
//
// Used in for Both IBM System Z & IBM Power PC.
func checkIfIpIsLive(ctx context.Context, ip string) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info(fmt.Sprintf("checking if IP address %s is live", ip))

	server, err := net.ResolveTCPAddr("tcp", ip+":22")
	if err != nil {
		log.Error(err, "failed to resolve ip address")
	}
	conn, err := net.DialTimeout(server.Network(), server.String(), 5*time.Second)
	if err != nil {
		log.Info("WARN: failed to connect to IBM host", "ip", ip)
		return err
	}
	if err := conn.Close(); err != nil {
		log.Error(err, "failed to close connection")
		return err
	}
	return nil
}

func ptr[V any](s V) *V {
	return &s
}

// lookUpSubnet looks for iz's subnet in the provided IBM Virtual Private Cloud service's API.
// Either the corresponding subnet or an error with a nil object is returned.
func (iz IBMZDynamicConfig) lookUpSubnet(vpcService *vpcv1.VpcV1) (*vpcv1.Subnet, error) {
	subnets, _, err := vpcService.ListSubnets(&vpcv1.ListSubnetsOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list VPC subnets: %w", err)
	}

	var subnet *vpcv1.Subnet
	for i := range subnets.Subnets {
		if *subnets.Subnets[i].Name == iz.Subnet {
			subnet = &subnets.Subnets[i]
			break
		}
	}
	if subnet == nil {
		return nil, fmt.Errorf("failed to find subnet %s", iz.Subnet)
	}
	return subnet, nil
}

// lookUpSSHKey looks for iz's SSH key in the provided IBM Virtual Private Cloud service's API.
// Either the corresponding key or an error with a nil object is returned.
func (iz IBMZDynamicConfig) lookUpSSHKey(vpcService *vpcv1.VpcV1) (*vpcv1.Key, error) {
	keys, _, err := vpcService.ListKeys(&vpcv1.ListKeysOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list VPC keys: %w", err)
	}

	var key *vpcv1.Key
	for i := range keys.Keys {
		if *keys.Keys[i].Name == iz.Key {
			key = &keys.Keys[i]
			break
		}
	}
	if key == nil {
		return nil, fmt.Errorf("failed to find SSH key %s", iz.Key)
	}
	return key, nil
}

// lookUpVpc looks for iz's VPC network in the provided IBM Virtual Private Cloud service's API.
// Either the corresponding VPC network or an error with a nil object is returned.
func (iz IBMZDynamicConfig) lookUpVpc(vpcService *vpcv1.VpcV1) (*vpcv1.VPC, error) {
	vpcs, _, err := vpcService.ListVpcs(&vpcv1.ListVpcsOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list VPC networks: %w", err)
	}

	var vpc *vpcv1.VPC
	for i := range vpcs.Vpcs {
		if *vpcs.Vpcs[i].Name == iz.Vpc {
			vpc = &vpcs.Vpcs[i]
			break
		}
	}
	if vpc == nil {
		return nil, fmt.Errorf("failed to find VPC network %s", iz.Vpc)
	}
	return vpc, nil
}

// createAuthenticatedVpcService generates a Virtual Private Cloud service with an API key-based IAM (Identity and Access
// Management) authenticator.
func (iz IBMZDynamicConfig) createAuthenticatedVpcService(ctx context.Context, kubeClient client.Client) (*vpcv1.VpcV1, error) {
	apiKey := ""
	if kubeClient == nil { // Get API key from an environment variable
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
		if apiKey == "" {
			return nil, errors.New("the API key from the IBM_CLOUD_API_KEY environment variable was empty")
		}
	} else { // Get API key from the iz Kubernetes Secret
		secret := v1.Secret{}
		nameSpacedSecret := types2.NamespacedName{Name: iz.Secret, Namespace: iz.SystemNamespace}
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
		URL: iz.Url,
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	})
	return vpcService, err
}

// assignNetworkInterfaceFloatingIP returns an IP address that is already associated with the instance
// network interface. If no IP addresses are found, an empty string is returned.
func assignNetworkInterfaceFloatingIP(instance *vpcv1.Instance, vpcService *vpcv1.VpcV1) string {
	instanceNetworkInterfaceOpts := &vpcv1.ListInstanceNetworkInterfaceFloatingIpsOptions{
		InstanceID:         instance.ID,
		NetworkInterfaceID: instance.PrimaryNetworkInterface.ID,
	}

	networkInterfaceIps, _, _ := vpcService.ListInstanceNetworkInterfaceFloatingIps(instanceNetworkInterfaceOpts)
	if len(networkInterfaceIps.FloatingIps) > 0 {
		return *networkInterfaceIps.FloatingIps[0].Address
	}
	return ""
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
			instanceNetworkInterfaceOptns := &vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions{
				InstanceID:         instance.ID,
				NetworkInterfaceID: instance.PrimaryNetworkInterface.ID,
				ID:                 ip.ID,
			}
			_, _, err = vpcService.AddInstanceNetworkInterfaceFloatingIP(instanceNetworkInterfaceOptns)
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
	floatingIpPrototypeOpts := &vpcv1.CreateFloatingIPOptions{
		FloatingIPPrototype: &vpcv1.FloatingIPPrototype{
			Zone: &vpcv1.ZoneIdentityByName{Name: ptr("us-east-2")},
			ResourceGroup: &vpcv1.ResourceGroupIdentity{
				ID: instance.ResourceGroup.ID,
			},
		},
	}
	ip, _, err := vpcService.CreateFloatingIP(floatingIpPrototypeOpts)
	if err != nil {
		return "", fmt.Errorf("failed to create a floating IP address for instance %s: %w", *instance.ID, err)
	}

	networkInterfaceFloatingIpOpts := &vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions{
		InstanceID: instance.ID, NetworkInterfaceID: instance.PrimaryNetworkInterface.ID,
		ID: ip.ID,
	}
	_, _, err = vpcService.AddInstanceNetworkInterfaceFloatingIP(networkInterfaceFloatingIpOpts)
	if err != nil {
		return "", fmt.Errorf(
			"failed to assign the floating IP address %s to the instance network interface %s: %w",
			*ip.Address,
			*instance.PrimaryNetworkInterface.ID,
			err,
		)
	}
	return *ip.Address, nil
}

// assignIPToInstance finds an available IP address and assigns it to the Virtual Private Cloud instance and
// its network interface. The string version of the IP address (an empty string if none was found) is returned.
func (iz IBMZDynamicConfig) assignIPToInstance(instance *vpcv1.Instance, vpcService *vpcv1.VpcV1) (string, error) {
	if iz.PrivateIP {
		for _, i := range instance.NetworkInterfaces {
			if i.PrimaryIP != nil && i.PrimaryIP.Address != nil && *i.PrimaryIP.Address != "0.0.0.0" {
				return *i.PrimaryIP.Address, nil
			}
		}
		return "", nil
	}

	// Try to find an already associated IP first
	ip := assignNetworkInterfaceFloatingIP(instance, vpcService)
	if ip != "" {
		return ip, nil
	}

	// If no IPs associated with an instance network interface are found, ensure the VPC instance is not in an
	// undesirable state before looking for a floating IP in the region
	switch *instance.Status {
	case vpcv1.InstanceStatusDeletingConst:
		return "", errors.New("instance was deleted")
	case vpcv1.InstanceStatusFailedConst:
		return "", errors.New("instance failed")
	case vpcv1.InstanceStatusPendingConst:
		return "", nil
	case vpcv1.InstanceStatusRestartingConst:
		return "", nil
	case vpcv1.InstanceStatusStartingConst:
		return "", nil
	case vpcv1.InstanceStatusStoppedConst:
		return "", errors.New("instance was stopped")
	case vpcv1.InstanceStatusStoppingConst:
		return "", errors.New("instance was stopping")
	}

	// Try to find an unattached floating IP address next
	ip, err := assignFloatingIP(instance, vpcService)
	if ip != "" || err != nil {
		return ip, err
	}

	// As a last resort, allocate a new IP Address. This is very expensive, as if we allocate a new IP address,
	// we are charged for the full month TODO: clarify this portion of comment -> (60c)
	ip, err = assignNewlyAllocatedIP(instance, vpcService)
	return ip, err
}

// findInstancesWithoutTaskRuns iterates over instances retrieved from the iz cloud and returns a list of those that
// are associated with a non-existing Tekton TaskRun. Each instance's volume should have a tag with the associated
// TaskRun's namespace and name. This is compared to existingTaskRuns, which is a map of namespaces to a list of
// TaskRuns in that namespace, to determine if this instance's TaskRun still exists.
func (iz IBMZDynamicConfig) findInstancesWithoutTaskRuns(log logr.Logger, vpcService *vpcv1.VpcV1, instances []vpcv1.Instance, existingTaskRuns map[string][]string) []string {
	var instancesWithoutTaskRuns []string

	// Iterate over all VM instances
	for _, instance := range instances {
		volumeId := instance.VolumeAttachments[0].ID
		// Get instance's volume; assumes only one volume per instance
		volume, _, err := vpcService.GetVolume(&vpcv1.GetVolumeOptions{ID: volumeId})
		if err != nil {
			msg := "WARN failed to get instance's volume; skipping this instance..."
			log.Info(msg, "instanceID", *instance.ID)
			continue
		}

		// Try to find the volume's TaskRun ID tag
		volumeTagIndex := slices.IndexFunc(volume.UserTags, func(tag string) bool {
			return cloud.ValidateTaskRunID(tag) == nil
		})
		if volumeTagIndex == -1 {
			log.Info("WARN: failed to find a valid TaskRun ID; appending to no TaskRun list anyway...", "instanceID", *instance.ID)
			instancesWithoutTaskRuns = append(instancesWithoutTaskRuns, *instance.ID)
			continue
		}
		volumeTag := volume.UserTags[volumeTagIndex]

		// Try to find this instance's TaskRun
		taskRunInfo := strings.Split(volumeTag, ":")
		taskRunNamespace, taskRunName := taskRunInfo[0], taskRunInfo[1]
		taskRuns, ok := existingTaskRuns[taskRunNamespace]
		// Add the VM instance to the no TaskRun list if the TaskRun namespace or TaskRun does not exist
		if !ok || !slices.Contains(taskRuns, taskRunName) {
			instancesWithoutTaskRuns = append(instancesWithoutTaskRuns, *instance.ID)
		}
	}
	return instancesWithoutTaskRuns
}
