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

func (r IBMPowerDynamicConfig) authenticatedService(ctx context.Context, kubeClient client.Client) (*core.BaseService, error) {
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
	serviceOptions := &core.ServiceOptions{
		URL: r.Url,
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	}

	baseService, err := core.NewBaseService(serviceOptions)
	if err != nil {
		return nil, err
	}
	return baseService, nil
}

func (r IBMPowerDynamicConfig) fetchInstances(ctx context.Context, kubeClient client.Client) (models.PVMInstances, error) {
	log := logr.FromContextOrDiscard(ctx)
	service, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return models.PVMInstances{}, err
	}
	builder := core.NewRequestBuilder(core.GET).WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud": r.pCloudId(),
	}
	_, err = builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return models.PVMInstances{}, err
	}
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return models.PVMInstances{}, err
	}

	instances := models.PVMInstances{}
	_, err = service.Request(request, &instances)
	if err != nil {
		log.Error(err, "Failed to request instances")
		return models.PVMInstances{}, err
	}
	return instances, nil
}

func (r IBMPowerDynamicConfig) pCloudId() string {
	return strings.Split(strings.Split(r.CRN, "/")[1], ":")[1]
}

func (r IBMPowerDynamicConfig) createServerInstance(ctx context.Context, service *core.BaseService, name string) (cloud.InstanceIdentifier, error) {

	log := logr.FromContextOrDiscard(ctx)
	builder := core.NewRequestBuilder(core.POST)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

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
		return "", err
	}
	_, err = builder.SetBodyContentJSON(&body)
	if err != nil {
		return "", err
	}
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Content-Type", "application/json")
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return "", err
	}

	instances := make([]models.PVMInstance, 1)
	_, err = service.Request(request, &instances)
	//println(response.String())
	if err != nil {
		log.Error(err, "failed to start power server")
		return "", err
	}
	instanceId := instances[0].PvmInstanceID
	log.Info("started power server", "instance", instanceId)
	r.resizeInstanceVolume(ctx, service, instanceId)
	return cloud.InstanceIdentifier(*instanceId), nil
}

func (r IBMPowerDynamicConfig) lookupIp(ctx context.Context, service *core.BaseService, pvmId string) (string, error) {
	instance, err := r.lookupInstance(ctx, service, pvmId)
	if err != nil {
		return "", err
	}
	return r.instanceIP(instance.PvmInstanceID, instance.Networks)
}

func (r IBMPowerDynamicConfig) instanceIP(instanceId *string, networks []*models.PVMInstanceNetwork) (string, error) {
	if len(networks) == 0 {
		return "", fmt.Errorf("no networks found for pvm %s", *instanceId)
	}
	network := networks[0]
	if network.ExternalIP != "" {
		return network.ExternalIP, nil
	} else if network.IPAddress != "" {
		return network.IPAddress, nil
	} else {
		return "", fmt.Errorf("no IP address found for pvm %s", *instanceId)
	}
}

func (r IBMPowerDynamicConfig) lookupInstance(ctx context.Context, service *core.BaseService, pvmId string) (*models.PVMInstance, error) {
	builder := core.NewRequestBuilder(core.GET)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud":           r.pCloudId(),
		"pvm_instance_id": pvmId,
	}
	_, err := builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return nil, err
	}
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return nil, err
	}

	instance := models.PVMInstance{}
	_, err = service.Request(request, &instance)
	//println(response.String())
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

func (r IBMPowerDynamicConfig) deleteServer(ctx context.Context, service *core.BaseService, pvmId string) error {

	builder := core.NewRequestBuilder(core.DELETE)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud":           r.pCloudId(),
		"pvm_instance_id": pvmId,
	}
	_, err := builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return err
	}
	builder.AddQuery("delete_data_volumes", "true")
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return err
	}

	var rawResponse map[string]json.RawMessage
	_, err = service.Request(request, &rawResponse)
	return err
}

// asynchronously resize volume when it's ID become known
func (r IBMPowerDynamicConfig) resizeInstanceVolume(ctx context.Context, service *core.BaseService, id *string) {
	log := logr.FromContextOrDiscard(ctx)
	timeout := time.Now().Add(time.Minute * 10)
	go func() {
		localCtx := context.WithoutCancel(ctx)
		for {
			time.Sleep(10 * time.Second)
			if timeout.Before(time.Now()) {
				log.Info("Resizing timeout reached")
				return
			}
			instance, err := r.lookupInstance(localCtx, service, *id)
			if err != nil {
				log.Error(err, "failed to get instance for resize, retrying is 10s")
				continue
			}
			// no volumes yet, wait more
			if len(instance.VolumeIDs) == 0 {
				continue
			}

			log.Info("Current volume", "size", *instance.DiskSize, "instance", *id)
			if *instance.DiskSize == r.Disk {
				//nothing to do
				return
			}

			log.Info("Resizing instance volume", "instance", *id, "volumeID", instance.VolumeIDs[0], "size", r.Disk)
			err = r.updateVolume(localCtx, service, instance.VolumeIDs[0])
			if err != nil {
				// API is quite unstable, randomly throwing conflicts or bad requests, so makes sense to repeat calls
				continue
			}
			return
		}
	}()
}

func (r IBMPowerDynamicConfig) updateVolume(ctx context.Context, service *core.BaseService, volumeId string) error {

	log := logr.FromContextOrDiscard(ctx)
	builder := core.NewRequestBuilder(core.PUT)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud":  r.pCloudId(),
		"volume": volumeId,
	}

	body := models.UpdateVolume{
		Size: r.Disk,
	}
	_, err := builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/volumes/{volume}`, pathParamsMap)
	if err != nil {
		return err
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
		return err
	}

	var vRef models.VolumeReference
	_, err = service.Request(request, &vRef)
	if err != nil {
		log.Error(err, "failed to update pvm volume")
		return err
	}
	log.Info("Volume size updated", "volumeId", vRef.VolumeID, "size", vRef.Size)
	return nil
}
