// Package ibm implements methods described in the [cloud] package for interacting with IBM cloud instances.
// Currently System Z and PowerPC cloud instances are supported with System Z running Virtual Private Cloud
// (VPC) instances and PowerPC running Power Systems Virtual Server (PVM) instances.
//
// All methods of the CloudProvider interface are implemented and separated from other helper functions used
// across the methods.
package ibm

import (
	"context" // TODO: clarify comment -> #nosec
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateIbmPowerCfg returns an IBM PowerPC cloud configuration that implements the CloudProvider interface.
func CreateIbmPowerConfig(platform string, config map[string]string, systemNamespace string) cloud.CloudProvider {
	mem, err := strconv.ParseFloat(config["dynamic."+platform+".memory"], 64)
	if err != nil {
		mem = 2
	}
	cores, err := strconv.ParseFloat(config["dynamic."+platform+".cores"], 64)
	if err != nil {
		cores = 0.25
	}
	volumeSize, err := strconv.ParseFloat(config["dynamic."+platform+".disk"], 64)
	// IBM docs say it is potentially undesireable to downsize the bootable volume
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
		ImageId:         config["dynamic."+platform+".image"],
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
func (ibmp IBMPowerDynamicConfig) LaunchInstance(kubeClient client.Client, ctx context.Context, _ string, instanceTag string, _ map[string]string) (cloud.InstanceIdentifier, error) {
	service, err := ibmp.createAuthenticatedBaseService(ctx, kubeClient)
	if err != nil {
		return "", fmt.Errorf("failed to create an authenticated base service: %w", err)
	}

	instanceName, err := createInstanceName(instanceTag)
	if err != nil {
		return "", fmt.Errorf("failed to create an instance name: %w", err)
	}

	instanceId, err := ibmp.createInstance(ctx, service, instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to create a PVM instance: %w", err)
	}
	return instanceId, err

}

// CountInstances returns the number of Power Systems Virtual Server instances whose names start with instanceTag.
func (ibmp IBMPowerDynamicConfig) CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error) {
	instances, err := ibmp.getAllInstances(ctx, kubeClient)
	if err != nil {
		return -1, fmt.Errorf("failed to fetch PVM instances: %w", err)
	}

	count := 0
	for _, instance := range instances.PvmInstances {
		if strings.HasPrefix(*instance.ServerName, instanceTag) {
			count++
		}
	}
	return count, nil
}

// GetInstanceAddress returns the IP Address associated with the instanceID Power Systems Virtual Server instance.
func (ibmp IBMPowerDynamicConfig) GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	log := logr.FromContextOrDiscard(ctx)
	service, err := ibmp.createAuthenticatedBaseService(ctx, kubeClient)
	if err != nil {
		return "", fmt.Errorf("failed to create an authenticated base service: %w", err)
	}

	instance, err := ibmp.getInstance(ctx, service, string(instanceId))
	if err != nil {
		return "", fmt.Errorf("failed to find instance %s: %w", instanceId, err)
	}
	ip, err := retrieveInstanceIP(*instance.PvmInstanceID, instance.Networks)
	if err != nil {
		log.Error(err, "failed to lookup IP", "instanceId", instanceId)
		return "", nil // TODO: find permanent errors
	}

	if err = checkIfIpIsLive(ctx, ip); err != nil {
		log.Error(err, "failed to resolve IP address", "instanceId", instanceId)
		return "", nil // TODO: figure out why an error is not returned here
	}
	return ip, nil
}

// ListInstances returns a collection of accessible Power Systems Virtual Server instances whose names start with instanceTag.
func (ibmp IBMPowerDynamicConfig) ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Listing PVM instances", "tag", instanceTag)
	pvmInstancesCollection, err := ibmp.getAllInstances(ctx, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PVM instances: %w", err)
	}

	// Ensure all listed instances have a reachable IP address
	vmInstances := make([]cloud.CloudVMInstance, 0, len(pvmInstancesCollection.PvmInstances))
	for _, instance := range pvmInstancesCollection.PvmInstances {
		if !strings.HasPrefix(*instance.ServerName, instanceTag) {
			continue
		}
		ip, err := retrieveInstanceIP(*instance.PvmInstanceID, instance.Networks)
		if err != nil {
			log.Error(err, "not listing instance as an IP address could not be assigned", "instanceId", *instance.PvmInstanceID)
			continue
		}
		if err = checkIfIpIsLive(ctx, ip); err != nil {
			log.Error(
				err,
				"not listing instance as its IP address could not be accessed",
				"instanceId", *instance.PvmInstanceID,
				"ip", ip,
			)
		}
		newVmInstance := cloud.CloudVMInstance{
			InstanceId: cloud.InstanceIdentifier(*instance.PvmInstanceID),
			Address:    ip,
			StartTime:  time.Time(instance.CreationDate),
		}
		vmInstances = append(vmInstances, newVmInstance)

	}
	log.Info("Finished listing PVM instances.", "count", len(vmInstances))
	return vmInstances, nil

}

// TerminateInstance tries to delete a specific Power Systems Virtual Server instance for 10 minutes or until the instance
// is deleted.
func (ibmp IBMPowerDynamicConfig) TerminateInstance(kubeClient client.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("attempting to terminate power server", "instance", instanceId)
	service, err := ibmp.createAuthenticatedBaseService(ctx, kubeClient)
	if err != nil {
		return fmt.Errorf("failed to create an authenticated base service: %w", err)
	}
	_ = ibmp.deleteInstance(ctx, service, string(instanceId))

	// Iterate for 10 minutes
	timeout := time.Now().Add(time.Minute * 10)
	go func() {
		localCtx := context.WithoutCancel(ctx)
		service, err := ibmp.createAuthenticatedBaseService(localCtx, kubeClient)
		if err != nil {
			return // TODO: determine why an error is not returned here
		}

		for {
			_, err := ibmp.getInstance(localCtx, service, string(instanceId))
			// Instance has already been deleted
			if err != nil {
				return
			}
			//TODO: clarify comment ->we want to make really sure it is gone, delete opts don't
			// really work when the server is starting so we just try in a loop
			err = ibmp.deleteInstance(localCtx, service, string(instanceId))
			if err != nil {
				log.Error(err, "failed to delete PVM instance", "instanceId", instanceId)
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

// An IBMPowerDynamicConfig represents a configuration for an IBM PowerPC cloud instance.
// The zero value (where each field will be assigned its type's zero value) is not a
// valid IBMPowerDynamicConfig.
type IBMPowerDynamicConfig struct {
	// SystemNamespace is the name of the Kubernetes namespace where the specified
	// secrets are stored.
	SystemNamespace string

	// Secret is the name of the Kubernetes ExternalSecret resource to use to
	// connect and authenticate with the IBM cloud service.
	Secret string

	// Key is the name of the public SSH key to be used when creating the cloud
	// instance.
	Key string

	// ImageId is the image to use when creating the cloud instance.
	ImageId string

	// Url is the url to use when creating the base service for
	// the cloud instance.
	Url string

	// CRN is the Cloud Resource Name used to uniquely identify the cloud instance.
	CRN string

	// Network is the network ID to use when creating the cloud instance.
	Network string

	// Cores is the number of computer cores to allocate for the cloud instance.
	Cores float64

	// Memory is the amount of memory (in GB) allocated to the cloud instance.
	Memory float64

	// Disk is the amount of permanent storage (in GB) allocated to the instance.
	Disk float64

	// System is the type of system to start in the cloud instance.
	System string

	// TODO: determine what this is for (see commonUserData in ibmp_test.go)
	UserData string

	// ProcessorType is the processor type to be used in the cloud instance.
	// Possible values are "dedicated", "shared", and "capped".
	ProcType string
}
