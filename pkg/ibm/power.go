package ibm

import (
	"context"
	"crypto/md5" //#nosec
	"encoding/base64"
	"encoding/json"
	"github.com/IBM/go-sdk-core/v4/core"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/stuartwdouglas/multi-arch-host-resolver/pkg/cloud"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"strings"
)

func IBMPowerProvider(arch string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	mem, err := strconv.Atoi(config["dynamic."+arch+".memory"])
	if err != nil {
		mem = 2
	}
	cores, err := strconv.ParseFloat(config["dynamic."+arch+".cores"], 64)
	if err != nil {
		cores = 0.25
	}
	return IBMPowerDynamicConfig{
		Key:             config["dynamic."+arch+".key"],
		Image:           config["dynamic."+arch+".image"],
		Secret:          config["dynamic."+arch+".secret"],
		Url:             config["dynamic."+arch+".url"],
		CRN:             config["dynamic."+arch+".crn"],
		Network:         config["dynamic."+arch+".network"],
		System:          config["dynamic."+arch+".system"],
		Cores:           cores,
		Memory:          mem,
		SystemNamespace: systemNamespace,
	}
}

func (r IBMPowerDynamicConfig) LaunchInstance(kubeClient client.Client, log *logr.Logger, ctx context.Context, _ string) (cloud.InstanceIdentifier, error) {
	service, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return "", err
	}

	binary, err := uuid.New().MarshalBinary()
	if err != nil {
		return "", err
	}
	name := "multi-" + strings.Replace(strings.ToLower(base64.URLEncoding.EncodeToString(md5.New().Sum(binary))[0:20]), "_", "-", -1) + "x" //#nosec
	instance, err := r.createServerInstance(ctx, log, service, name)
	if err != nil {
		return "", err
	}
	return instance, err

}

func (r IBMPowerDynamicConfig) authenticate(kubeClient client.Client, ctx context.Context) (*core.BaseService, error) {
	apiKey := ""
	if kubeClient == nil {
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
	} else {
		s := v1.Secret{}
		err := kubeClient.Get(ctx, types2.NamespacedName{Name: r.Secret, Namespace: "multi-arch-controller"}, &s)
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

func (r IBMPowerDynamicConfig) GetInstanceAddress(kubeClient client.Client, log *logr.Logger, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	service, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return "", err
	}
	ip, err := r.lookupIp(ctx, service, string(instanceId))
	if err != nil {
		return "", err
	}
	return checkAddressLive(ip, log)
}

func (r IBMPowerDynamicConfig) TerminateInstance(kubeClient client.Client, log *logr.Logger, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	log.Info("attempting to terminate power server %s", "instance", instanceId)
	service, err := r.authenticate(kubeClient, ctx)
	if err != nil {
		return err
	}
	return r.deleteServer(ctx, service, string(instanceId))
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

func (r IBMPowerDynamicConfig) createServerInstance(ctx context.Context, log *logr.Logger, service *core.BaseService, name string) (cloud.InstanceIdentifier, error) {

	builder := core.NewRequestBuilder(core.POST)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud": r.pCloudId(),
	}
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
		NetworkIDs:  []string{r.Network},
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
	response, err := service.Request(request, &rawResponse)
	println(response.String())
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

	builder := core.NewRequestBuilder(core.GET)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = service.GetEnableGzipCompression()

	pathParamsMap := map[string]string{
		"cloud":           r.pCloudId(),
		"pvm_instance_id": pvmId,
	}
	_, err := builder.ResolveRequestURL(r.Url, `/pcloud/v1/cloud-instances/{cloud}/pvm-instances/{pvm_instance_id}`, pathParamsMap)
	if err != nil {
		return "", err
	}
	builder.AddHeader("CRN", r.CRN)
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return "", err
	}

	var rawResponse map[string]json.RawMessage
	_, err = service.Request(request, &rawResponse)
	//println(response.String())
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
	external := string(nwList[0]["externalIP"])
	if external[0] == '"' {
		return external[1 : len(external)-1], nil
	}
	return external, nil
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
