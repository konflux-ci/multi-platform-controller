package ibm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
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

// retrieveInstanceIP returns a string representing the IP address of instance instanceID.
func retrieveInstanceIp(instanceID string, networks []*models.PVMInstanceNetwork) (string, error) {
	if len(networks) == 0 {
		return "", fmt.Errorf("no networks found for Power Systems VM %s", instanceID)
	}
	network := networks[0]
	if network.ExternalIP != "" {
		return network.ExternalIP, nil
	} else if network.IPAddress != "" {
		return network.IPAddress, nil
	} else {
		return "", fmt.Errorf("no IP address found for Power Systems VM %s", instanceID)
	}
}

// createAuthenticatedBaseService generates a base communication service with an API key-based IAM (Identity and Access
// Management) authenticator.
func (pw IBMPowerDynamicConfig) createAuthenticatedBaseService(ctx context.Context, kubeClient client.Client) (*core.BaseService, error) {
	apiKey := ""
	if kubeClient == nil { // Get API key from an environment variable
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
	} else { // Get API key from pw's Kubernetes Secret
		secret := v1.Secret{}
		namespacedSecret := types2.NamespacedName{Name: pw.Secret, Namespace: pw.SystemNamespace}
		err := kubeClient.Get(ctx, namespacedSecret, &secret)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve the secret %s from the Kubernetes client: %w", namespacedSecret, err)
		}

		apiKeyByte, ok := secret.Data["api-key"]
		if !ok {
			return nil, fmt.Errorf("the secret %s did not have an API key field", namespacedSecret)
		}
		apiKey = string(apiKeyByte)
	}

	serviceOptions := &core.ServiceOptions{
		URL: pw.Url,
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	}
	baseService, err := core.NewBaseService(serviceOptions)
	return baseService, err
}

// listInstances returns all of the Power Systems VM instances on the pw cloud instance.
func (pw IBMPowerDynamicConfig) listInstances(ctx context.Context, service *core.BaseService) (models.PVMInstances, error) {
	requestBuilder := core.NewRequestBuilder(core.GET).WithContext(ctx)
	requestBuilder.EnableGzipCompression = service.GetEnableGzipCompression()

	// Parameterize the request
	cloudId, err := pw.parseCRN()
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud": cloudId,
	}
	_, err = requestBuilder.ResolveRequestURL(pw.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	// Add headers and build request
	requestBuilder.AddHeader("CRN", pw.CRN)
	requestBuilder.AddHeader("Accept", "application/json")
	request, err := requestBuilder.Build()
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the GET request
	instances := models.PVMInstances{}
	_, err = service.Request(request, &instances)
	if err != nil {
		return models.PVMInstances{},
			fmt.Errorf("failed to list all Power Systems VM instances on cloud %s: %w", cloudId, err)
	}
	return instances, nil
}

// parseCRN returns the service instance of pw's Cloud Resource Name.
// See the [IBM CRN docs](https://cloud.ibm.com/docs/account?topic=account-crn#service-instance-crn)
// for more information on CRN formatting.
func (pw IBMPowerDynamicConfig) parseCRN() (string, error) {
	locationIndex := 5
	serviceInstanceIndex := 7
	crnSegments := strings.Split(pw.CRN, ":")

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

// launchInstance returns the instance ID of the Power Systems VM instance created with an HTTP
// request on the pw cloud instance.
func (pw IBMPowerDynamicConfig) launchInstance(ctx context.Context, service *core.BaseService, additionalInfo map[string]string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx)
	requestBuilder := core.NewRequestBuilder(core.POST)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = service.GetEnableGzipCompression()

	// Parameterize the request
	cloudId, err := pw.parseCRN()
	if err != nil {
		return "", fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud": cloudId,
	}
	_, err = requestBuilder.ResolveRequestURL(pw.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return "", fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	// Set body content and headers
	name, ok := additionalInfo["name"]
	if !ok {
		return "", fmt.Errorf("failed to find the server instance's name")
	}
	taskRunTag, ok := additionalInfo[cloud.TaskRunTagKey]
	if !ok {
		return "", fmt.Errorf("failed to find the server instance's full TaskRun ID")
	}

	network := strings.Split(pw.Network, ",")
	body := models.PVMInstanceCreate{
		ServerName:  &name,
		ImageID:     &pw.ImageId,
		Processors:  &pw.Cores,
		ProcType:    &pw.ProcType,
		Memory:      &pw.Memory,
		NetworkIDs:  network,
		KeyPairName: pw.Key,
		SysType:     pw.System,
		UserData:    pw.UserData,
		UserTags:    []string{taskRunTag},
	}
	_, err = requestBuilder.SetBodyContentJSON(&body)
	if err != nil {
		return "", fmt.Errorf("failed to set the body of the request: %w", err)
	}
	requestBuilder.AddHeader("CRN", pw.CRN)
	requestBuilder.AddHeader("Content-Type", "application/json")
	requestBuilder.AddHeader("Accept", "application/json")

	// Create and send the POST request
	request, err := requestBuilder.Build()
	if err != nil {
		return "", fmt.Errorf("failed to build the HTTP request: %w", err)
	}
	instances := make([]models.PVMInstance, 1)
	_, err = service.Request(request, &instances)
	if err != nil {
		return "", fmt.Errorf("failed to launch a Power Systems VM instance: %w", err)
	}

	instanceID := instances[0].PvmInstanceID
	log.Info("Launched Power Systems VM instance", "instanceID", instanceID)
	pw.resizeInstanceVolume(ctx, service, instanceID)
	return cloud.InstanceIdentifier(*instanceID), nil
}

// getInstance returns the specified Power Systems VM instance on the pw cloud instance.
func (pw IBMPowerDynamicConfig) getInstance(ctx context.Context, service *core.BaseService, pvmId string) (*models.PVMInstance, error) {
	requestBuilder := core.NewRequestBuilder(core.GET)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = service.GetEnableGzipCompression()

	// Parameterize the request
	cloudId, err := pw.parseCRN()
	if err != nil {
		return &models.PVMInstance{}, fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud":           cloudId,
		"pvm_instance_id": pvmId,
	}
	_, err = requestBuilder.ResolveRequestURL(pw.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	// Add headers and build the request
	requestBuilder.AddHeader("CRN", pw.CRN)
	requestBuilder.AddHeader("Accept", "application/json")
	request, err := requestBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the GET request
	instance := models.PVMInstance{}
	_, err = service.Request(request, &instance)
	if err != nil {
		return nil, fmt.Errorf("failed to get the Power Systems VM instance %s: %w", pvmId, err)
	}
	return &instance, nil
}

// deleteInstance removes the specified Power Systems VM instance from the pw cloud instance.
func (pw IBMPowerDynamicConfig) deleteInstance(ctx context.Context, service *core.BaseService, pvmId string) error {
	requestBuilder := core.NewRequestBuilder(core.DELETE)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = service.GetEnableGzipCompression()

	// Parameterize the request
	cloudId, err := pw.parseCRN()
	if err != nil {
		return fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud":           cloudId,
		"pvm_instance_id": pvmId,
	}
	_, err = requestBuilder.ResolveRequestURL(pw.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	// Add headers/queries and build the request
	requestBuilder.AddQuery("delete_data_volumes", "true")
	requestBuilder.AddHeader("CRN", pw.CRN)
	requestBuilder.AddHeader("Accept", "application/json")
	request, err := requestBuilder.Build()
	if err != nil {
		return fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	// Execute the DELETE request
	var rawResponse map[string]json.RawMessage
	_, err = service.Request(request, &rawResponse)
	if err != nil {
		err = fmt.Errorf("failed to delete the Power System VM instance %s: %w", pvmId, err)
	}
	return err
}

// resizeInstanceVolume asynchronously resizes the id instance's volume.
func (pw IBMPowerDynamicConfig) resizeInstanceVolume(ctx context.Context, service *core.BaseService, id *string) {
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
			instance, err := pw.getInstance(localCtx, service, *id)
			if err != nil {
				log.Info("WARN: failed to get Power Systems VM instance for resize; retrying...",
					"err", err,
					"retry", time.Duration(sleepTime),
				)
				continue
			}
			// No volumes yet, try again in 10 seconds
			if len(instance.VolumeIDs) == 0 {
				continue
			}

			log.Info("Current volume", "size", *instance.DiskSize, "instanceID", *id)
			// Nothing to do since the volume size is the same as the specified size
			if *instance.DiskSize == pw.Disk {
				return
			}

			// This API is quite unstable and randomly throws conflicts or bad requests. Try multiple times.
			log.Info("Resizing instance volume", "instanceID", *id, "volumeID", instance.VolumeIDs[0], "size", pw.Disk)
			err = pw.updateVolume(localCtx, service, instance.VolumeIDs[0])
			if err != nil {
				continue
			}
			log.Info("Successfully resized instance volume", "instanceID", *id, "volumeID", instance.VolumeIDs[0], "size", pw.Disk)
			return
		}
	}()
}

// updateVolume changes volumeID's size to pw's disk size via HTTP.
func (pw IBMPowerDynamicConfig) updateVolume(ctx context.Context, service *core.BaseService, volumeID string) error {
	log := logr.FromContextOrDiscard(ctx)
	requestBuilder := core.NewRequestBuilder(core.PUT)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = service.GetEnableGzipCompression()

	// Parameterize the request
	cloudID, err := pw.parseCRN()
	if err != nil {
		return fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud":  cloudID,
		"volume": volumeID,
	}
	_, err = requestBuilder.ResolveRequestURL(pw.Url, `/pcloud/v1/cloud-instances/{cloud}/volumes/{volume}`, pathParamsMap)
	if err != nil {
		return fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	// Set request body and headers
	body := models.UpdateVolume{
		Size: pw.Disk,
	}
	_, err = requestBuilder.SetBodyContentJSON(&body)
	if err != nil {
		return err
	}
	requestBuilder.AddHeader("CRN", pw.CRN)
	requestBuilder.AddHeader("Content-Type", "application/json")
	requestBuilder.AddHeader("Accept", "application/json")

	// Build and execute PUT request
	request, err := requestBuilder.Build()
	if err != nil {
		return fmt.Errorf("failed to build the HTTP request: %w", err)
	}
	var vRef models.VolumeReference
	_, err = service.Request(request, &vRef)
	if err != nil {
		return fmt.Errorf("failed to update volume %s: %w", volumeID, err)
	}
	log.Info("Volume size updated", "volumeID", vRef.VolumeID, "size", vRef.Size)
	return nil
}

// doesInstanceHaveTaskRun returns whether or not an instance on the pw cloud is associated with an existing
// TaskRun. Each instance should have a tag with the associated TaskRun's namespace and name. This is compared
// to existingTaskRuns, which is a map of namespaces to a list of TaskRuns in that namespace, to determine if
// this instance's TaskRun still exists.
func (pw IBMPowerDynamicConfig) doesInstanceHaveTaskRun(log logr.Logger, instance *models.PVMInstance, existingTaskRuns map[string][]string) bool {
	// Try to find the instance's TaskRun ID tag
	instanceTagIndex := slices.IndexFunc(instance.UserTags, func(tag string) bool {
		return cloud.ValidateTaskRunID(tag) == nil
	})
	if instanceTagIndex == -1 {
		msg := "WARN: failed to find a valid TaskRun ID; counting as having no TaskRun anyway..."
		log.Info(msg, "instanceID", *instance.PvmInstanceID)
		return false
	}
	tag := instance.UserTags[instanceTagIndex]

	// Try to find this instance's TaskRun
	taskRunInfo := strings.Split(tag, ":")
	taskRunNamespace, taskRunName := taskRunInfo[0], taskRunInfo[1]
	taskRuns, ok := existingTaskRuns[taskRunNamespace]
	// Return true if the associated TaskRun exists in a TaskRun namespace
	return ok && slices.Contains(taskRuns, taskRunName)
}
