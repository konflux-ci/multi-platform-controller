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

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func IBMPowerProvider(platform string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	mem, err := strconv.Atoi(config["dynamic."+platform+".memory"])
	if err != nil {
		mem = 2
	}
	cores, err := strconv.ParseFloat(config["dynamic."+platform+".cores"], 64)
	if err != nil {
		cores = 0.25
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
		SystemNamespace: systemNamespace,
	}
}

func (r IBMPowerDynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, taskRunName string, instanceTag string) (cloud.InstanceIdentifier, error) {
	service, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return "", err
	}

	binary, err := uuid.New().MarshalBinary()
	if err != nil {
		return "", err
	}
	name := instanceTag + strings.Replace(strings.ToLower(base64.URLEncoding.EncodeToString(md5.New().Sum(binary))[0:20]), "_", "-", -1) + "x" //#nosec
	instance, err := r.createServerInstance(ctx, service, name)
	if err != nil {
		return "", err
	}
	return instance, err

}

func (r IBMPowerDynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	service, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return 0, err
	}
	builder := core.NewRequestBuilder(core.GET)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud": r.pCloudId(),
	}
	_, err = builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances`, pathParamsMap)
	if err != nil {
		return 0, err
	}
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return 0, err
	}

	var rawResponse map[string]json.RawMessage
	_, err = service.Request(request, &rawResponse)
	//println(response.String())
	if err != nil {
		return 0, err
	}
	instancesData := rawResponse["pvmInstances"]
	instances := []json.RawMessage{}
	err = json.Unmarshal(instancesData, &instances)
	if err != nil {
		return 0, err
	}
	return len(rawResponse), nil
}

func (r IBMPowerDynamicConfig) authenticate(kubeClient client.Client, ctx context.Context) (*core.BaseService, error) {
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
	service, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return "", err
	}
	ip, err := r.lookupIp(ctx, service, string(instanceId))
	if err != nil {
		return "", nil //todo: check for permanent errors
	}
	return checkAddressLive(ctx, ip), err
}

func (r IBMPowerDynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	return nil, fmt.Errorf("not impelemented")
}
func (r IBMPowerDynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("attempting to terminate power server %s", "instance", instanceId)
	service, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return err
	}
	_ = r.deleteServer(ctx, service, string(instanceId))
	timeout := time.Now().Add(time.Minute * 10)
	go func() {
		service, err := r.authenticate(kubeClient, context.Background())
		if err != nil {
			return
		}
		for {
			_, err := r.lookupInstance(ctx, service, string(instanceId))
			if err == nil {
				//its gone, return
				return
			}
			//we want to make really sure it is gone, delete opts don't really work when the server is starting
			//so we just try in a loop
			err = r.deleteServer(ctx, service, string(instanceId))
			if err != nil {
				log.Error(err, "failed to delete system z instance")
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
	Memory          int
	System          string
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
	body := struct {
		ServerName  string   `json:"serverName"`
		ImageId     string   `json:"imageId"`
		Processors  float64  `json:"processors"`
		ProcType    string   `json:"procType"`
		Memory      int      `json:"memory"`
		NetworkIDs  []string `json:"networkIDs"`
		KeyPairName string   `json:"keyPairName"`
		SysType     string   `json:"sysType"`
	}{
		ServerName:  name,
		ImageId:     r.Image,
		Processors:  r.Cores,
		ProcType:    "shared",
		Memory:      r.Memory,
		NetworkIDs:  network,
		KeyPairName: r.Key,
		SysType:     r.System,
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

	var rawResponse []map[string]json.RawMessage
	_, err = service.Request(request, &rawResponse)
	//println(response.String())
	if err != nil {
		log.Error(err, "failed to start power server")
		return "", err
	}
	instanceId := string(rawResponse[0]["pvmInstanceID"])
	log.Info("started power server", "instance", instanceId)
	if instanceId[0] == '"' {
		return cloud.InstanceIdentifier(instanceId[1 : len(instanceId)-1]), nil

	}
	return cloud.InstanceIdentifier(instanceId), nil
}

func (r IBMPowerDynamicConfig) lookupIp(ctx context.Context, service *core.BaseService, pvmId string) (string, error) {

	rawResponse, err := r.lookupInstance(ctx, service, pvmId)
	if err != nil {
		return "", err
	}
	info := rawResponse["networks"]
	nwList := []map[string]json.RawMessage{}
	err = json.Unmarshal(info, &nwList)
	if err != nil {
		return "", err
	}
	if len(nwList) == 0 {
		return "", err
	}

	internal := string(nwList[0]["ipAddress"])
	external := string(nwList[0]["externalIP"])

	var ip string

	if external != "" {
		ip = external
	} else if internal != "" {
		ip = internal
	} else {
		return "", err
	}

	if ip[0] == '"' {
		return ip[1 : len(ip)-1], nil
	}
	return ip, nil
}

func (r IBMPowerDynamicConfig) lookupInstance(ctx context.Context, service *core.BaseService, pvmId string) (map[string]json.RawMessage, error) {
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

	var rawResponse map[string]json.RawMessage
	_, err = service.Request(request, &rawResponse)
	//println(response.String())
	if err != nil {
		return nil, err
	}
	return rawResponse, nil
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
