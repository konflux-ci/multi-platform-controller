package taskrun

import (
	"context"
	"github.com/go-logr/logr"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

// UpdateHostPools Run the update-host task periodically
func UpdateHostPools(operatorNamespace string, client client.Client, log *logr.Logger) {
	log.Info("running pooled host update")
	cm := corev1.ConfigMap{}
	err := client.Get(context.Background(), types.NamespacedName{Namespace: operatorNamespace, Name: HostConfig}, &cm)
	if err != nil {
		log.Error(err, "Failed to read config to update hosts", "audit", "true")
		return
	}

	hosts := map[string]*Host{}
	for k, v := range cm.Data {
		if !strings.HasPrefix(k, "host.") {
			continue
		}
		k = strings.TrimPrefix(k, "host.")
		pos := strings.LastIndex(k, ".")
		if pos == -1 {
			continue
		}
		name := k[0:pos]
		key := k[pos+1:]
		host := hosts[name]
		if host == nil {
			host = &Host{
				Name: name,
			}
			hosts[name] = host
		}
		switch key {
		case "address":
			host.Address = v
		case "user":
			host.User = v
		case "platform":
			host.Platform = v
		case "secret":
			host.Secret = v
		case "concurrency":
		default:
			log.Info("unknown key", "name", name, "key", key)
		}
	}
	delay := 0
	for hostName := range hosts {
		log.Info("scheduling host update", "host", hostName)
		// We don't want to run all updates at once
		// Stagger all updates by 10 minutes
		timer := time.NewTimer(time.Minute * time.Duration(delay) * 10)
		delay++
		realHostName := hostName
		host := hosts[realHostName]
		go func() {
			<-timer.C

			log.Info("updating host", "host", realHostName)
			provisionTr := v1.TaskRun{}
			provisionTr.GenerateName = "update-task"
			provisionTr.Namespace = operatorNamespace
			provisionTr.Labels = map[string]string{TaskTypeLabel: TaskTypeUpdate, AssignedHost: realHostName}
			provisionTr.Spec.TaskRef = &v1.TaskRef{Name: "update-host"}
			provisionTr.Spec.Workspaces = []v1.WorkspaceBinding{{Name: "ssh", Secret: &corev1.SecretVolumeSource{SecretName: host.Secret}}}
			compute := map[corev1.ResourceName]resource.Quantity{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("256Mi")}
			provisionTr.Spec.ComputeResources = &corev1.ResourceRequirements{Requests: compute, Limits: compute}
			provisionTr.Spec.ServiceAccountName = ServiceAccountName //TODO: special service account for this
			provisionTr.Spec.Params = []v1.Param{
				{
					Name:  "HOST",
					Value: *v1.NewStructuredValues(host.Address),
				},
				{
					Name:  "USER",
					Value: *v1.NewStructuredValues(host.User),
				},
			}
			err = client.Create(context.Background(), &provisionTr)
		}()
	}
}
