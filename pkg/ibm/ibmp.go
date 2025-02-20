// Package ibm implements methods described in the [cloud] package for interacting with IBM cloud instances.
// Currently System Z and Power Systems instances are supported with a Virtual Private Cloud (VPC) running System
// Z virtual server instances and a Power Virtual Server Workspace running Power Systems virtual server instances.
//
// All methods of the CloudProvider interface are implemented.
package ibm

import (
	"context"
	// #nosec is added to bypass the golang security scan since the cryptographic
	// strength doesn't matter here
	"crypto/md5" //#nosec
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IBM-Cloud/power-go-client/power/models"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IBMPowerProvider returns an IBM Power Systems cloud configuration that implements the CloudProvider interface.
func IBMPowerProvider(platform string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	mem, err := strconv.ParseFloat(config["dynamic."+platform+".memory"], 64)
	if err != nil {
		mem = 2
	}
	cores, err := strconv.ParseFloat(config["dynamic."+platform+".cores"], 64)
	if err != nil {
		cores = 0.25
	}
	volumeSize, err := strconv.ParseFloat(config["dynamic."+platform+".disk"], 64)
	// IBM docs says it is potentially unwanted to downsize the bootable volume
	if err != nil || volumeSize < 100 {
		volumeSize = 100
	}

	userDataString := config["dynamic."+platform+".user-data"]
	var base64userData = ""
	if userDataString != "" {
		base64userData = base64.StdEncoding.EncodeToString([]byte(userDataString))
	}

	return IBMPowerDynamicConfig{
		Key:             config["dynamic."+platform+".key"],
		Image:           config["dynamic."+platform+".image"],
		Secret:          config["dynamic."+platform+".secret"],
		Url:             config["dynamic."+platform+".url"],
		CRN:             config["dynamic."+platform+".crn"],
		Network:         config["dynamic."+platform+".network"],
		System:          config["dynamic."+platform+".system"],
		Cores:           cores,
		Memory:          mem,
		Disk:            volumeSize,
		SystemNamespace: systemNamespace,
		UserData:        base64userData,
		ProcType:        "shared",
	}
}

// LaunchInstance creates a Power Systems Virtual Server instance and returns its identifier. This function
// is implemented as part of the CloudProvider interface, which is why some of the arguments are unused for this particular
// implementation.
func (r IBMPowerDynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, taskRunName string, instanceTag string, _ map[string]string) (cloud.InstanceIdentifier, error) {
	service, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", fmt.Errorf("failed to create an authenticated base service: %w", err)
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

	instance, err := r.createServerInstance(ctx, service, name)
	if err != nil {
		return "", fmt.Errorf("failed to create a Power Systems instance: %w", err)
	}
	return instance, err

}

// CountInstances returns the number of Power Systems Virtual Server instances whose names start with instanceTag.
func (r IBMPowerDynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	instances, err := r.fetchInstances(ctx, kubeClient)
	if err != nil {
		return -1, fmt.Errorf("failed to fetch Power Systems instances: %w", err)
	}

	count := len(instances.PvmInstances)
	for _, instance := range instances.PvmInstances {
		if !strings.HasPrefix(*instance.ServerName, instanceTag) {
			count--
		}
	}
	return count, nil
}

// authenticatedService generates a base service with an API key-based IAM (Identity and Access
// Management) authenticator.
func (r IBMPowerDynamicConfig) authenticatedService(ctx context.Context, kubeClient client.Client) (*core.BaseService, error) {
	apiKey := ""
	if kubeClient == nil { // Get API key from an environment variable
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
	} else { // Get API key from the ibmp Kubernetes Secret
		s := v1.Secret{}
		namespacedSecret := types2.NamespacedName{Name: r.Secret, Namespace: r.SystemNamespace}
		err := kubeClient.Get(ctx, namespacedSecret, &s)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve the secret %s from the Kubernetes client: %w", namespacedSecret, err)
		}

		apiKeyByte, ok := s.Data["api-key"]
		if !ok {
			return nil, fmt.Errorf("the secret %s did not have an API key field", namespacedSecret)
		}
		apiKey = string(apiKeyByte)
	}

	serviceOptions := &core.ServiceOptions{
		URL: r.Url,
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	}
	baseService, err := core.NewBaseService(serviceOptions)
	return baseService, err
}

// GetInstanceAddress returns the IP Address associated with the instanceID Power Systems Virtual Server instance.
func (r IBMPowerDynamicConfig) GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	log := logr.FromContextOrDiscard(ctx)
	service, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", fmt.Errorf("failed to create an authenticated base service: %w", err)
	}

	// Errors regarding looking up the IP and checking if the address is live are not returned
	// as we are waiting for the network interface to start up. This is a normal part of the
	// instance allocation process.
	ip, err := r.lookupIp(ctx, service, string(instanceId))
	if err != nil {
		log.Error(err, "Failed to look up IP address", "instanceId", instanceId)
		return "", nil // TODO: clarify comment -> check for permanent errors
	}
	if err = checkAddressLive(ctx, ip); err != nil {
		log.Error(err, "Failed to check IP address", "instanceId", instanceId)
		return "", nil // TODO: figure out why an error is not returned here
	}
	return ip, nil
}

// ListInstances returns a collection of accessible Power Systems Virtual Server instances whose names start with instanceTag.
func (r IBMPowerDynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Listing Power Systems instances", "tag", instanceTag)
	instances, err := r.fetchInstances(ctx, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Power Systems instances: %w", err)
	}

	ret := make([]cloud.CloudVMInstance, 0, len(instances.PvmInstances))
	// Ensure all listed instances have a reachable IP address
	for _, instance := range instances.PvmInstances {
		if !strings.HasPrefix(*instance.ServerName, instanceTag) {
			continue
		}
		identifier := cloud.InstanceIdentifier(*instance.PvmInstanceID)
		createdAt := time.Time(instance.CreationDate)
		ip, err := r.instanceIP(instance.PvmInstanceID, instance.Networks)
		if err != nil {
			log.Error(err, "not listing instance as IP address cannot be assigned yet", "instance", identifier)
			continue
		}
		if err = checkAddressLive(ctx, ip); err != nil {
			log.Error(err, "not listing instance as IP address cannot be accessed yet", "instanceId", identifier)
			continue
		}
		ret = append(ret, cloud.CloudVMInstance{InstanceId: identifier, Address: ip, StartTime: createdAt})

	}
	log.Info("Finished listing Power Systems instances.", "count", len(ret))
	return ret, nil

}

// fetchInstances returns all of the Power Systems Virtual Server instances on r's cloud instance.
func (r IBMPowerDynamicConfig) fetchInstances(ctx context.Context, kubeClient client.Client) (models.PVMInstances, error) {
	log := logr.FromContextOrDiscard(ctx)
	service, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to create an authenticated base service: %w", err)
	}

	// Formulate the HTTP GET request
	builder := core.NewRequestBuilder(core.GET).WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud": r.pCloudId(),
	}
	_, err = builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the GET request
	instances := models.PVMInstances{}
	_, err = service.Request(request, &instances)
	if err != nil {
		log.Error(err, "Failed to request instances")
		return models.PVMInstances{}, err
	}
	return instances, nil
}

// TerminateInstance tries to delete a specific Power Systems Virtual Server instance for 10 minutes or until the instance
// is deleted.
func (r IBMPowerDynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("attempting to terminate power server", "instance", instanceId)
	service, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return fmt.Errorf("failed to create an authenticated base service: %w", err)
	}

	_ = r.deleteServer(ctx, service, string(instanceId))

	// Iterate for 10 minutes
	timeout := time.Now().Add(time.Minute * 10)
	go func() {
		localCtx := context.WithoutCancel(ctx)
		service, err := r.authenticatedService(localCtx, kubeClient)
		if err != nil {
			return
		}

		for {
			_, err := r.lookupInstance(localCtx, service, string(instanceId))
			// Instance has already been deleted
			if err != nil {
				return
			}
			//TODO: clarify comment ->we want to make really sure it is gone, delete opts don't
			// really work when the server is starting so we just try in a loop
			err = r.deleteServer(localCtx, service, string(instanceId))
			if err != nil {
				log.Error(err, "failed to delete system power vm instance")
			}
			if timeout.Before(time.Now()) {
				return
			}

			// Sleep 10 seconds between each execution
			time.Sleep(time.Second * 10)
		}
	}()
	return nil
}

func (r IBMPowerDynamicConfig) SshUser() string {
	return "root"
}

// An IBMPowerDynamicConfig represents a configuration for an IBM Power Systems cloud instance.
// The zero value (where each field will be assigned its type's zero value) is not a
// valid IBMPowerDynamicConfig.
type IBMPowerDynamicConfig struct {
	// SystemNamespace is the name of the Kubernetes namespace where the specified
	// secrets are stored.
	SystemNamespace string

	// Secret is the name of the Kubernetes ExternalSecret resource to use to
	// connect and authenticate with the IBM cloud service.
	Secret string

	// Key is the name of the public SSH key to be used when creating the instance.
	Key string

	// Image is the image to use when creating the instance.
	Image string

	// Url is the url to use when creating the base service for the instance.
	Url string

	// CRN is the Cloud Resource Name used to uniquely identify the cloud the instance
	// is hosted on.
	CRN string

	// Network is the network ID to use when creating the instance.
	Network string

	// Cores is the number of computer cores to allocate for the instance.
	Cores float64

	// Memory is the amount of memory (in GB) allocated to the instance.
	Memory float64

	// Disk is the amount of permanent storage (in GB) allocated to the instance.
	Disk float64

	// System is the type of system to start in the instance.
	System string

	// TODO: determine what this is for (see commonUserData in ibmp_test.go)
	UserData string

	// ProcessorType is the processor type to be used in the instance.
	// Possible values are "dedicated", "shared", and "capped".
	ProcType string
}

// pCloudId returns the service instance of ibmp's Cloud Resource Name.
// See the [IBM CRN docs](https://cloud.ibm.com/docs/account?topic=account-crn#service-instance-crn)
// for more information on CRN formatting.
func (r IBMPowerDynamicConfig) pCloudId() string {
	return strings.Split(strings.Split(r.CRN, "/")[1], ":")[1]
}

// createServerInstance returns the instance ID of the Power Systems Virtual Server Instance created with an HTTP
// request on r's cloud instance.
func (r IBMPowerDynamicConfig) createServerInstance(ctx context.Context, service *core.BaseService, name string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx)
	builder := core.NewRequestBuilder(core.POST)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	// Set request values
	pathParamsMap := map[string]string{
		"cloud": r.pCloudId(),
	}

	network := strings.Split(r.Network, ",")
	body := models.PVMInstanceCreate{
		ServerName:  &name,
		ImageID:     &r.Image,
		Processors:  &r.Cores,
		ProcType:    &r.ProcType,
		Memory:      &r.Memory,
		NetworkIDs:  network,
		KeyPairName: r.Key,
		SysType:     r.System,
		UserData:    r.UserData,
	}
	_, err := builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return "", fmt.Errorf("failed to encode the request with parameters: %w", err)
	}
	_, err = builder.SetBodyContentJSON(&body)
	if err != nil {
		return "", fmt.Errorf("failed to set the body of the request: %w", err)
	}
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Content-Type", "application/json")
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return "", fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Send the request
	instances := make([]models.PVMInstance, 1)
	_, err = service.Request(request, &instances)
	if err != nil {
		log.Error(err, "failed to start Power Systems virtual server")
		return "", err
	}

	instanceId := instances[0].PvmInstanceID
	log.Info("started power server", "instance", instanceId)
	r.resizeInstanceVolume(ctx, service, instanceId)
	return cloud.InstanceIdentifier(*instanceId), nil
}

// lookupIp returns a string representing the IP address of the instance with ID pvmId.
func (r IBMPowerDynamicConfig) lookupIp(ctx context.Context, service *core.BaseService, pvmId string) (string, error) {
	instance, err := r.lookupInstance(ctx, service, pvmId)
	if err != nil {
		return "", err
	}
	return r.instanceIP(instance.PvmInstanceID, instance.Networks)
}

// instanceIP returns a string representing the IP address of instance instanceId.
func (r IBMPowerDynamicConfig) instanceIP(instanceId *string, networks []*models.PVMInstanceNetwork) (string, error) {
	if len(networks) == 0 {
		return "", fmt.Errorf("no networks found for Power Systems VM %s", *instanceId)
	}
	network := networks[0]
	if network.ExternalIP != "" {
		return network.ExternalIP, nil
	} else if network.IPAddress != "" {
		return network.IPAddress, nil
	} else {
		return "", fmt.Errorf("no IP address found for Power Systems VM %s", *instanceId)
	}
}

// lookupInstance returns the specified Power Systems Virtual Server instance on r's cloud instance.
func (r IBMPowerDynamicConfig) lookupInstance(ctx context.Context, service *core.BaseService, pvmId string) (*models.PVMInstance, error) {
	// Formulate the HTTP GET request
	builder := core.NewRequestBuilder(core.GET)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud":           r.pCloudId(),
		"pvm_instance_id": pvmId,
	}
	_, err := builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the request with parameters: %w", err)
	}
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the GET request
	instance := models.PVMInstance{}
	_, err = service.Request(request, &instance)
	if err != nil {
		return nil, fmt.Errorf("failed to GET Power Systems VM instance %s: %w", pvmId, err)
	}
	return &instance, nil
}

// deleteServer removes the specified Power Systems Virtual Server instance from r's cloud instance.
func (r IBMPowerDynamicConfig) deleteServer(ctx context.Context, service *core.BaseService, pvmId string) error {
	// Formulate the HTTP DELETE request
	builder := core.NewRequestBuilder(core.DELETE)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud":           r.pCloudId(),
		"pvm_instance_id": pvmId,
	}
	_, err := builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return fmt.Errorf("failed to encode the request with parameters: %w", err)
	}
	builder.AddQuery("delete_data_volumes", "true")
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the request
	var rawResponse map[string]json.RawMessage
	_, err = service.Request(request, &rawResponse)
	if err != nil {
		err = fmt.Errorf("failed to DELETE Power System VM instance %s: %w", pvmId, err)
	}
	return err
}

// resizeInstanceVolume asynchronously resizes the instance id's volume.
func (r IBMPowerDynamicConfig) resizeInstanceVolume(ctx context.Context, service *core.BaseService, id *string) {
	log := logr.FromContextOrDiscard(ctx)
	sleepTime := 10
	// Iterate for 10 minutes
	timeout := time.Now().Add(time.Minute * 10)
	go func() {
		localCtx := context.WithoutCancel(ctx)
		for {
			// Sleep 10 seconds between each execution
			time.Sleep(time.Duration(sleepTime) * time.Second)
			if timeout.Before(time.Now()) {
				log.Info("Resizing timeout reached")
				return
			}
			instance, err := r.lookupInstance(localCtx, service, *id)
			if err != nil {
				log.Error(err, "failed to get Power Systems VM instance for resize, retrying is %s", sleepTime)
				continue
			}
			// No volumes yet, try again in 10 seconds
			if len(instance.VolumeIDs) == 0 {
				continue
			}

			log.Info("Current volume", "size", *instance.DiskSize, "instance", *id)
			// Nothing to do since the volume size is the same as the specified size
			if *instance.DiskSize == r.Disk {
				return
			}

			// This API is quite unstable and randomly throws conflicts or bad requests. Try multiple times.
			log.Info("Resizing instance volume", "instance", *id, "volumeID", instance.VolumeIDs[0], "size", r.Disk)
			err = r.updateVolume(localCtx, service, instance.VolumeIDs[0])
			if err != nil {
				continue
			}
			log.Info("Successfully resized instance volume", "instance", *id, "volumeID", instance.VolumeIDs[0], "size", r.Disk)
			return
		}
	}()
}

// updateVolume changes volumeID's size to r's disk size via HTTP.
func (r IBMPowerDynamicConfig) updateVolume(ctx context.Context, service *core.BaseService, volumeId string) error {
	log := logr.FromContextOrDiscard(ctx)
	builder := core.NewRequestBuilder(core.PUT)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	// Parameterize PUT request
	pathParamsMap := map[string]string{
		"cloud":  r.pCloudId(),
		"volume": volumeId,
	}
	_, err := builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/volumes/{volume}`, pathParamsMap)
	if err != nil {
		return fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	// Set request body and headers
	body := models.UpdateVolume{
		Size: r.Disk,
	}
	_, err = builder.SetBodyContentJSON(&body)
	if err != nil {
		return err
	}
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Content-Type", "application/json")
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the request
	var vRef models.VolumeReference
	_, err = service.Request(request, &vRef)
	if err != nil {
		log.Error(err, "failed to update pvm volume")
		return fmt.Errorf("failed to update volume %s: %w", volumeId, err)
	}
	log.Info("Volume size updated", "volumeId", vRef.VolumeID, "size", vRef.Size)
	return nil
}
