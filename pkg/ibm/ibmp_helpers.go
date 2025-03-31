package ibm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
		ImageID:     &r.ImageId,
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
				log.Error(err, fmt.Sprintf("failed to get Power Systems VM instance for resize, retrying in %d s", sleepTime))
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
