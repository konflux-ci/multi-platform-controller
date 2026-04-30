package ibm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
)

// powerClient implements powerAPI using the IBM Power Virtual Server REST API
// via core.BaseService.
type powerClient struct {
	service *core.BaseService
	config  IBMPowerDynamicConfig
}

func (pc *powerClient) listInstances(ctx context.Context) (models.PVMInstances, error) {
	requestBuilder := core.NewRequestBuilder(core.GET).WithContext(ctx)
	requestBuilder.EnableGzipCompression = pc.service.GetEnableGzipCompression()

	cloudId, err := pc.config.parseCRN()
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud": cloudId,
	}
	_, err = requestBuilder.ResolveRequestURL(pc.config.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	requestBuilder.AddHeader("CRN", pc.config.CRN)
	requestBuilder.AddHeader("Accept", "application/json")
	request, err := requestBuilder.Build()
	if err != nil {
		return models.PVMInstances{}, fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	instances := models.PVMInstances{}
	_, err = pc.service.Request(request, &instances)
	if err != nil {
		return models.PVMInstances{},
			fmt.Errorf("failed to list all Power Systems VM instances on cloud %s: %w", cloudId, err)
	}
	return instances, nil
}

func (pc *powerClient) launchInstance(ctx context.Context, additionalInfo map[string]string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx)
	requestBuilder := core.NewRequestBuilder(core.POST)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = pc.service.GetEnableGzipCompression()

	cloudId, err := pc.config.parseCRN()
	if err != nil {
		return "", fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud": cloudId,
	}
	_, err = requestBuilder.ResolveRequestURL(pc.config.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return "", fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	name, ok := additionalInfo["name"]
	if !ok {
		return "", errors.New("failed to find the server instance's name")
	}
	taskRunTag, ok := additionalInfo[cloud.TaskRunTagKey]
	if !ok {
		return "", errors.New("failed to find the server instance's full TaskRun ID")
	}

	network := strings.Split(pc.config.Network, ",")
	body := models.PVMInstanceCreate{
		ServerName:  &name,
		ImageID:     &pc.config.ImageId,
		Processors:  &pc.config.Cores,
		ProcType:    &pc.config.ProcType,
		Memory:      &pc.config.Memory,
		NetworkIDs:  network,
		KeyPairName: pc.config.Key,
		SysType:     pc.config.System,
		UserData:    pc.config.UserData,
		UserTags:    []string{taskRunTag},
	}
	_, err = requestBuilder.SetBodyContentJSON(&body)
	if err != nil {
		return "", fmt.Errorf("failed to set the body of the request: %w", err)
	}
	requestBuilder.AddHeader("CRN", pc.config.CRN)
	requestBuilder.AddHeader("Content-Type", "application/json")
	requestBuilder.AddHeader("Accept", "application/json")

	request, err := requestBuilder.Build()
	if err != nil {
		return "", fmt.Errorf("failed to build the HTTP request: %w", err)
	}
	instances := make([]models.PVMInstance, 1)
	_, err = pc.service.Request(request, &instances)
	if err != nil {
		return "", fmt.Errorf("failed to launch a Power Systems VM instance: %w", err)
	}

	instanceID := instances[0].PvmInstanceID
	log.Info("Launched Power Systems VM instance", "instanceID", instanceID)
	pc.resizeInstanceVolume(ctx, instanceID)
	return cloud.InstanceIdentifier(*instanceID), nil
}

func (pc *powerClient) getInstance(ctx context.Context, pvmId string) (*models.PVMInstance, error) {
	requestBuilder := core.NewRequestBuilder(core.GET)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = pc.service.GetEnableGzipCompression()

	cloudId, err := pc.config.parseCRN()
	if err != nil {
		return &models.PVMInstance{}, fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud":           cloudId,
		"pvm_instance_id": pvmId,
	}
	_, err = requestBuilder.ResolveRequestURL(pc.config.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	requestBuilder.AddHeader("CRN", pc.config.CRN)
	requestBuilder.AddHeader("Accept", "application/json")
	request, err := requestBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	instance := models.PVMInstance{}
	_, err = pc.service.Request(request, &instance)
	if err != nil {
		return nil, fmt.Errorf("failed to get the Power Systems VM instance %s: %w", pvmId, err)
	}
	return &instance, nil
}

func (pc *powerClient) deleteInstance(ctx context.Context, pvmId string) error {
	requestBuilder := core.NewRequestBuilder(core.DELETE)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = pc.service.GetEnableGzipCompression()

	cloudId, err := pc.config.parseCRN()
	if err != nil {
		return fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud":           cloudId,
		"pvm_instance_id": pvmId,
	}
	_, err = requestBuilder.ResolveRequestURL(pc.config.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	requestBuilder.AddQuery("delete_data_volumes", "true")
	requestBuilder.AddHeader("CRN", pc.config.CRN)
	requestBuilder.AddHeader("Accept", "application/json")
	request, err := requestBuilder.Build()
	if err != nil {
		return fmt.Errorf("failed to build the HTTP request: %w", err)
	}

	var rawResponse map[string]json.RawMessage
	_, err = pc.service.Request(request, &rawResponse)
	if err != nil {
		err = fmt.Errorf("failed to delete the Power System VM instance %s: %w", pvmId, err)
	}
	return err
}

// resizeInstanceVolume asynchronously resizes the id instance's volume.
func (pc *powerClient) resizeInstanceVolume(ctx context.Context, id *string) {
	log := logr.FromContextOrDiscard(ctx)
	sleepTime := 10
	timeout := time.Now().Add(time.Minute * 10)
	go func() {
		localCtx := context.WithoutCancel(ctx)
		for {
			time.Sleep(time.Duration(sleepTime) * time.Second)
			if timeout.Before(time.Now()) {
				log.Info("Resizing timeout reached")
				return
			}
			instance, err := pc.getInstance(localCtx, *id)
			if err != nil {
				log.Info("WARN: failed to get Power Systems VM instance for resize; retrying...",
					"err", err,
					"retry", time.Duration(sleepTime),
				)
				continue
			}
			if len(instance.VolumeIDs) == 0 {
				continue
			}

			log.Info("Current volume", "size", *instance.DiskSize, "instanceID", *id)
			if *instance.DiskSize == pc.config.Disk {
				return
			}

			// This API is quite unstable and randomly throws conflicts or bad requests. Try multiple times.
			log.Info("Resizing instance volume", "instanceID", *id, "volumeID", instance.VolumeIDs[0], "size", pc.config.Disk)
			err = pc.updateVolume(localCtx, instance.VolumeIDs[0])
			if err != nil {
				continue
			}
			log.Info("Successfully resized instance volume", "instanceID", *id, "volumeID", instance.VolumeIDs[0], "size", pc.config.Disk)
			return
		}
	}()
}

// updateVolume changes volumeID's size to the configured disk size via HTTP.
func (pc *powerClient) updateVolume(ctx context.Context, volumeID string) error {
	log := logr.FromContextOrDiscard(ctx)
	requestBuilder := core.NewRequestBuilder(core.PUT)
	requestBuilder = requestBuilder.WithContext(ctx)
	requestBuilder.EnableGzipCompression = pc.service.GetEnableGzipCompression()

	cloudID, err := pc.config.parseCRN()
	if err != nil {
		return fmt.Errorf("failed to retrieve cloud service instance ID: %w", err)
	}
	pathParamsMap := map[string]string{
		"cloud":  cloudID,
		"volume": volumeID,
	}
	_, err = requestBuilder.ResolveRequestURL(pc.config.Url, `/pcloud/v1/cloud-instances/{cloud}/volumes/{volume}`, pathParamsMap)
	if err != nil {
		return fmt.Errorf("failed to encode the request with parameters: %w", err)
	}

	body := models.UpdateVolume{
		Size: pc.config.Disk,
	}
	_, err = requestBuilder.SetBodyContentJSON(&body)
	if err != nil {
		return err
	}
	requestBuilder.AddHeader("CRN", pc.config.CRN)
	requestBuilder.AddHeader("Content-Type", "application/json")
	requestBuilder.AddHeader("Accept", "application/json")

	request, err := requestBuilder.Build()
	if err != nil {
		return fmt.Errorf("failed to build the HTTP request: %w", err)
	}
	var vRef models.VolumeReference
	_, err = pc.service.Request(request, &vRef)
	if err != nil {
		return fmt.Errorf("failed to update volume %s: %w", volumeID, err)
	}
	log.Info("Volume size updated", "volumeID", vRef.VolumeID, "size", vRef.Size)
	return nil
}
