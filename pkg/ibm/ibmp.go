package ibm

import (
	"context"
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
	if err != nil || volumeSize < 100 { // IMB docs says it is potentially unwanted to downsize the bootable volume
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

func (r IBMPowerDynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, taskRunName string, instanceTag string, _ map[string]string) (cloud.InstanceIdentifier, error) {
	service, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", err
	}

	binary, err := uuid.New().MarshalBinary()
	if err != nil {
		return "", err
	}
	name := instanceTag + "-" + strings.Replace(strings.ToLower(base64.URLEncoding.EncodeToString(md5.New().Sum(binary))[0:20]), "_", "-", -1) + "x" //#nosec
	instance, err := r.createServerInstance(ctx, service, name)
	if err != nil {
		return "", err
	}
	return instance, err

}

func (r IBMPowerDynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	instances, err := r.fetchInstances(ctx, kubeClient)
	if err != nil {
		return 0, err
	}
	count := len(instances.PvmInstances)
	for _, instance := range instances.PvmInstances {
		if !strings.HasPrefix(*instance.ServerName, instanceTag) {
			count--
		}
	}
	return count, nil
}

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

func (r IBMPowerDynamicConfig) GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	log := logr.FromContextOrDiscard(ctx)
	service, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return "", err
	}
	ip, err := r.lookupIp(ctx, service, string(instanceId))
	if err != nil {
		log.Error(err, "Failed to lookup IP", "instanceId", instanceId, "error", err.Error())
		return "", nil //todo: check for permanent errors
	}
	if err = checkAddressLive(ctx, ip); err != nil {
		log.Error(err, "Failed to check address", "instanceId", instanceId, "error", err.Error())
		return "", nil
	}
	return ip, nil
}

func (r IBMPowerDynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Listing ppc instances", "tag", instanceTag)
	instances, err := r.fetchInstances(ctx, kubeClient)
	if err != nil {
		return nil, err
	}

	ret := make([]cloud.CloudVMInstance, 0, len(instances.PvmInstances))
	for _, instance := range instances.PvmInstances {
		if !strings.HasPrefix(*instance.ServerName, instanceTag) {
			continue
		}
		identifier := cloud.InstanceIdentifier(*instance.PvmInstanceID)
		createdAt := time.Time(instance.CreationDate)
		ip, err := r.instanceIP(instance.PvmInstanceID, instance.Networks)
		if err != nil {
			log.Error(err, "not listing instance as address cannot be assigned yet", "instance", identifier)
			continue
		}
		if err = checkAddressLive(ctx, ip); err != nil {
			log.Error(err, "not listing instance as address cannot be accessed yet", "instanceId", identifier, "error", err.Error())
			continue
		}
		ret = append(ret, cloud.CloudVMInstance{InstanceId: identifier, Address: ip, StartTime: createdAt})

	}
	log.Info("Listing ppc instances done.", "count", len(ret))
	return ret, nil

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
func (r IBMPowerDynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("attempting to terminate power server", "instance", instanceId)
	service, err := r.authenticatedService(ctx, kubeClient)
	if err != nil {
		return err
	}
	_ = r.deleteServer(ctx, service, string(instanceId))
	timeout := time.Now().Add(time.Minute * 10)
	go func() {
		localCtx := context.WithoutCancel(ctx)
		service, err := r.authenticatedService(localCtx, kubeClient)
		if err != nil {
			return
		}
		for {
			_, err := r.lookupInstance(localCtx, service, string(instanceId))
			if err != nil {
				//its gone, return
				return
			}
			//we want to make really sure it is gone, delete opts don't really work when the server is starting
			//so we just try in a loop
			err = r.deleteServer(localCtx, service, string(instanceId))
			if err != nil {
				log.Error(err, "failed to delete system power vm instance")
			}
			if timeout.Before(time.Now()) {
				return
			}
			time.Sleep(time.Second * 10)
		}
	}()
	return nil
}

type IBMPowerDynamicConfig struct {
	SystemNamespace string
	Secret          string
	Key             string
	Image           string
	Url             string
	CRN             string
	Network         string
	Cores           float64
	Memory          float64
	Disk            float64
	System          string
	UserData        string
	ProcType        string
}

func (r IBMPowerDynamicConfig) pCloudId() string {
	return strings.Split(strings.Split(r.CRN, "/")[1], ":")[1]
}

func (r IBMPowerDynamicConfig) SshUser() string {
	return "root"
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
				log.Error(err, "failed to get instance for resize")
				return
			}
			// no volumes yet, wait more
			if len(instance.VolumeIDs) == 0 {
				continue
			}
			log.Info("Resizing instance volume", "instance", *id, "volumeID", instance.VolumeIDs[0], "size", r.Disk)
			err = r.updateVolume(localCtx, service, instance.VolumeIDs[0])
			if err != nil {
				log.Error(err, "failed to resize power server volume")
				if err.Error() != "conflict" { // conflicts may happen if volume is not ready yet
					return
				}
			}
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

	var rawResponse []map[string]json.RawMessage
	_, err = service.Request(request, &rawResponse)
	//println(response.String())
	if err != nil {
		log.Error(err, "failed to update pvm volume")
		return err
	}
	return nil
}
