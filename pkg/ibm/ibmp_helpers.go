package ibm

import (
	"context"
	"encoding/json"
	"errors"
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

// retrieveInstanceIP returns a string representing the IP address of instance instanceId.
func retrieveInstanceIP(instanceId string, networks []*models.PVMInstanceNetwork) (string, error) {
	if len(networks) == 0 {
		return "", fmt.Errorf("no networks found for pvm %s", instanceId)
	}
	network := networks[0]
	if network.ExternalIP != "" {
		return network.ExternalIP, nil
	} else if network.IPAddress != "" {
		return network.IPAddress, nil
	} else {
		return "", fmt.Errorf("no IP address found for pvm %s", instanceId)
	}
}

// parsePowerPcCRN parses ibmp's Cloud Resource Name and returns the service instance.
// See the [IBM CRN docs](https://cloud.ibm.com/docs/account?topic=account-crn#service-instance-crn)
// for more information on CRN formatting.
func (ibmp IBMPowerDynamicConfig) parsePowerPcCRN() (string, error) {
	locationIndex := 5
	serviceInstanceIndex := 7
	crnSegments := strings.Split(ibmp.CRN, ":")

	if crnSegments[locationIndex] == "global" {
		errMsg := "this resource is global and has no service instance"
		return "", errors.New(errMsg)
	}
	serviceInstance := crnSegments[serviceInstanceIndex]
	if serviceInstance == "" {
		errMsg := "the service instance is null"
		return "", errors.New(errMsg)
	}

	return serviceInstance, nil
}

// createAuthenticatedBaseService generates a base service with an API key-based IAM (Identity and Access
// Management) authenticator.
func (ibmp IBMPowerDynamicConfig) createAuthenticatedBaseService(ctx context.Context, kubeClient client.Client) (*core.BaseService, error) {
	apiKey := ""
	if kubeClient == nil { // Get API key from an environment variable
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
		if apiKey == "" {
			return nil, errors.New("the API key from the IBM_CLOUD_API_KEY environment variable was empty")
		}
	} else { // Get API key from the ibmp Kubernetes Secret
		secret := v1.Secret{}
		nameSpacedSecret := types2.NamespacedName{Name: ibmp.Secret, Namespace: ibmp.SystemNamespace}
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

	baseService, err := core.NewBaseService(&core.ServiceOptions{
		URL: ibmp.Url,
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	})
	return baseService, err
}

// getAllInstances returns all of the Power Systems Virtual Server instances on ibmp's cloud instance.
func (ibmp IBMPowerDynamicConfig) getAllInstances(ctx context.Context, kubeClient client.Client) (models.PVMInstances, error) {
	log := logr.FromContextOrDiscard(ctx)
	service, err := ibmp.createAuthenticatedBaseService(ctx, kubeClient)
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to create an authenticated base service: %w", err)
	}

	// Formulate the HTTP GET request
	requestBuilder := core.NewRequestBuilder(core.GET).WithContext(ctx)
	requestBuilder.EnableGzipCompression = service.GetEnableGzipCompression()

	cloudId, err := ibmp.parsePowerPcCRN()
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud": cloudId,
	}
	_, err = requestBuilder.ResolveRequestURL(ibmp.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	requestBuilder.AddHeader("CRN", ibmp.CRN)
	requestBuilder.AddHeader("Accept", "application/json")

	request, err := requestBuilder.Build()
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the GET request
	instances := models.PVMInstances{}
	_, err = service.Request(request, &instances)
	if err != nil {
		log.Error(err, "failed to request all PowerPC instances")
		return models.PVMInstances{}, fmt.Errorf("failed to GET PVM instances: %w", err)
	}
	return instances, nil
}

// createInstance returns the instance ID of the Power Systems Virtual Server Instances created with an HTTP
// request on ibmp's cloud instance.
func (ibmp IBMPowerDynamicConfig) createInstance(ctx context.Context, service *core.BaseService, name string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx)
	requestBuilder := core.NewRequestBuilder(core.POST)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = service.GetEnableGzipCompression()

	// Parameterize request
	cloudId, err := ibmp.parsePowerPcCRN()
	if err != nil {
		return "", fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud": cloudId,
	}
	_, err = requestBuilder.ResolveRequestURL(ibmp.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return "", fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	// Set request body & headers
	network := strings.Split(ibmp.Network, ",")
	body := models.PVMInstanceCreate{
		ServerName:  &name,
		ImageID:     &ibmp.ImageId,
		Processors:  &ibmp.Cores,
		ProcType:    &ibmp.ProcType,
		Memory:      &ibmp.Memory,
		NetworkIDs:  network,
		KeyPairName: ibmp.Key,
		SysType:     ibmp.System,
		UserData:    ibmp.UserData,
	}
	_, err = requestBuilder.SetBodyContentJSON(&body)
	if err != nil {
		return "", fmt.Errorf("failed to set the body of the request: %w", err)
	}
	requestBuilder.AddHeader("CRN", ibmp.CRN)
	requestBuilder.AddHeader("Content-Type", "application/json")
	requestBuilder.AddHeader("Accept", "application/json")

	request, err := requestBuilder.Build()
	if err != nil {
		return "", fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Send the request
	instances := make([]models.PVMInstance, 1)
	_, err = service.Request(request, &instances)
	if err != nil {
		log.Error(err, "failed to create a PowerPC instance")
		return "", fmt.Errorf("failed to POST a PVM instance: %w", err)
	}

	instanceId := instances[0].PvmInstanceID
	log.Info("created PowerPC instance", "instance", instanceId)
	ibmp.resizeInstanceVolume(ctx, service, instanceId)
	return cloud.InstanceIdentifier(*instanceId), nil
}

// getInstance returns the specified Power Systems Virtual Server instance on ibmp's cloud instance.
func (ibmp IBMPowerDynamicConfig) getInstance(ctx context.Context, service *core.BaseService, pvmId string) (*models.PVMInstance, error) {
	// Formulate the HTTP GET request
	requestBuilder := core.NewRequestBuilder(core.GET)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = service.GetEnableGzipCompression()

	cloudId, err := ibmp.parsePowerPcCRN()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud":           cloudId,
		"pvm_instance_id": pvmId,
	}
	_, err = requestBuilder.ResolveRequestURL(ibmp.Url, `/pcloud/v1/cloud-instances/{cloudId}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the request with parameters: %w", err)
	}
	requestBuilder.AddHeader("CRN", ibmp.CRN)
	requestBuilder.AddHeader("Accept", "application/json")

	request, err := requestBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the GET request
	instance := models.PVMInstance{}
	_, err = service.Request(request, &instance)
	if err != nil {
		return nil, fmt.Errorf("failed to GET PVM instance %s: %w", pvmId, err)
	}
	return &instance, nil
}

// deleteInstance removes the specified Power Systems Virtual Server instance from ibmp's cloud instance.
func (ibmp IBMPowerDynamicConfig) deleteInstance(ctx context.Context, service *core.BaseService, pvmId string) error {
	// Formulate the HTTP DELETE request
	requestBuilder := core.NewRequestBuilder(core.DELETE)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = service.GetEnableGzipCompression()

	cloudId, err := ibmp.parsePowerPcCRN()
	if err != nil {
		return fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloudId":         cloudId,
		"pvm_instance_id": pvmId,
	}
	_, err = requestBuilder.ResolveRequestURL(ibmp.Url, `/pcloud/v1/cloud-instances/{cloudId}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return fmt.Errorf("failed to encode the request with parameters: %w", err)
	}
	requestBuilder.AddQuery("delete_data_volumes", "true")
	requestBuilder.AddHeader("CRN", ibmp.CRN)
	requestBuilder.AddHeader("Accept", "application/json")

	request, err := requestBuilder.Build()
	if err != nil {
		return fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the request
	var rawResponse map[string]json.RawMessage
	_, err = service.Request(request, &rawResponse)
	if err != nil {
		err = fmt.Errorf("failed to DELETE PVM instance %s: %w", pvmId, err)
	}
	return err
}

// resizeInstanceVolume asynchronously resizes the instance id's volume.
func (ibmp IBMPowerDynamicConfig) resizeInstanceVolume(ctx context.Context, service *core.BaseService, id *string) {
	log := logr.FromContextOrDiscard(ctx)
	sleepTime := 10
	// Iterate for 10 minutes
	timeout := time.Now().Add(time.Minute * time.Duration(sleepTime))
	go func() {
		localCtx := context.WithoutCancel(ctx)
		for {
			// Sleep 10 seconds between each execution
			time.Sleep(10 * time.Second)
			if timeout.Before(time.Now()) {
				log.Info("Resizing timeout reached")
				return
			}

			instance, err := ibmp.getInstance(localCtx, service, *id)
			if err != nil {
				log.Error(err, fmt.Sprintf("failed to get PVM instance for resize, retrying in %ds", sleepTime))
				continue
			}
			// No volumes yet, try again in 10 seconds
			if len(instance.VolumeIDs) == 0 {
				continue
			}

			log.Info("Current volume", "size", *instance.DiskSize, "instance", *id)
			// Nothing to do since the volume size is the same as the specified size
			if *instance.DiskSize == ibmp.Disk {
				return
			}

			// This API is quite unstable and randomly throws conflicts or bad requests. Try multiple times.
			log.Info("Resizing instance volume", "instanceID", *id, "volumeID", instance.VolumeIDs[0], "size", ibmp.Disk)
			err = ibmp.updateVolume(localCtx, service, instance.VolumeIDs[0])
			if err != nil {
				log.Error(
					err,
					fmt.Sprintf("failed to resize volume, retrying in %ds", sleepTime),
					"instanceID", *id,
					"volumeID", instance.VolumeIDs[0],
				)
				continue
			}
			return
		}
	}()
}

// updateVolume changes volumeID's size to ibmp's disk size via HTTP.
func (ibmp IBMPowerDynamicConfig) updateVolume(ctx context.Context, service *core.BaseService, volumeId string) error {
	log := logr.FromContextOrDiscard(ctx)
	builder := core.NewRequestBuilder(core.PUT)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	// Parameterize PUT request
	cloudId, err := ibmp.parsePowerPcCRN()
	if err != nil {
		return fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloudId": cloudId,
		"volume":  volumeId,
	}
	_, err = builder.ResolveRequestURL(ibmp.Url, `/pcloud/v1/cloud-instances/{cloudId}/volumes/{volume}`, pathParamsMap)
	if err != nil {
		return fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	// Set request body and headers
	body := models.UpdateVolume{
		Size: ibmp.Disk,
	}
	_, err = builder.SetBodyContentJSON(&body)
	if err != nil {
		return fmt.Errorf("failed to set the request body: %w", err)
	}
	builder.AddHeader("CRN", ibmp.CRN)
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
		log.Error(err, "failed to update PVM volume")
		return fmt.Errorf("failed to update volume %s: %w", volumeId, err)
	}
	log.Info("Volume size updated", "volumeId", vRef.VolumeID, "size", vRef.Size)
	return nil
}
