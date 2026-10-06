package taskrun

import (
	"context"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/config"
	"github.com/konflux-ci/multi-platform-controller/pkg/constant"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v12 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UpdateHostPools Run the host update task periodically
func UpdateHostPools(operatorNamespace string, c client.Client, scheme *k8sRuntime.Scheme, recorder record.EventRecorder, log *logr.Logger) {
	log.Info("running pooled host update")
	cm := v12.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{Namespace: operatorNamespace, Name: HostConfig}, &cm)
	if err != nil {
		log.Error(err, "Failed to read config to update hosts", "audit", "true")
		return
	}

	hosts := map[string]*Host{}
	// A way to transfer the concurrency configuration data from the ConfigMap value (string) to a Param value (also string) not via host.Concurrency
	// (int) that does not involve strconv.Atoi/Itoi which are expensive.
	hostsConcurrency := make(map[string]string)
	for k, v := range cm.Data {
		if !strings.HasPrefix(k, "host.") {
			continue
		}
		k = k[len("host."):]
		pos := strings.LastIndex(k, ".")
		if pos == -1 {
			continue
		}
		name := k[0:pos]
		key := k[pos+1:]
		host := hosts[name]
		if host == nil {
			host = &Host{}
			hosts[name] = host
			host.Name = name
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
			if v != "" {
				hostsConcurrency[host.Name] = v
			} else {
				continue
			}
		case "ssh-config":
			if sshConfig := strings.TrimSpace(v); sshConfig != "" {
				if err := config.ValidateSSHClientConfig(sshConfig); err != nil {
					log.Error(err, "ignoring ssh-config", "host", host.Name)
					if recorder != nil {
						recorder.Eventf(&cm, v12.EventTypeWarning, "SSHConfigRejected", "host %s ssh-config was rejected and will not be used for host updates: %v", host.Name, err)
					}
					continue
				}
				host.SSHConfig = sshConfig
			}

		default:
			log.Info("unknown key", "key", key)
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
			provision := v1.TaskRun{}
			provision.GenerateName = "update-task"
			provision.Namespace = operatorNamespace
			provision.Labels = map[string]string{TaskTypeLabel: TaskTypeUpdate, constant.AssignedHost: realHostName}
			provision.Spec.TaskRef = &v1.TaskRef{Name: "update-host"}
			provision.Spec.Workspaces = []v1.WorkspaceBinding{{Name: "ssh", Secret: &v12.SecretVolumeSource{SecretName: host.Secret}}}
			var sshConfigCM *v12.ConfigMap
			if host.SSHConfig != "" {
				cm, binding, snapErr := snapshotSSHConfig(context.Background(), c, operatorNamespace, host.SSHConfig)
				if snapErr != nil {
					log.Error(snapErr, "failed to snapshot ssh-config", "host", realHostName)
					return
				}
				sshConfigCM = cm
				provision.Spec.Workspaces = append(provision.Spec.Workspaces, binding)
			}
			compute := map[v12.ResourceName]resource.Quantity{v12.ResourceCPU: resource.MustParse("100m"), v12.ResourceMemory: resource.MustParse("256Mi")}
			provision.Spec.ComputeResources = &v12.ResourceRequirements{Requests: compute, Limits: compute}
			provision.Spec.ServiceAccountName = ServiceAccountName //TODO: special service account for this
			provision.Spec.Params = []v1.Param{
				{
					Name:  "HOST",
					Value: *v1.NewStructuredValues(host.Address),
				},
				{
					Name:  "USER",
					Value: *v1.NewStructuredValues(host.User),
				},
				{
					Name:  "PLATFORM",
					Value: *v1.NewStructuredValues(host.Platform),
				},
				{
					Name:  "CONCURRENCY",
					Value: *v1.NewStructuredValues(hostsConcurrency[host.Name]),
				},
			}
			if err := c.Create(context.Background(), &provision); err != nil {
				if sshConfigCM != nil {
					_ = c.Delete(context.Background(), sshConfigCM)
				}
				log.Error(err, "failed to create host update task", "host", realHostName)
				return
			}
			if sshConfigCM == nil {
				return
			}
			if err := setSSHConfigMapOwner(context.Background(), c, scheme, &provision, sshConfigCM); err != nil {
				log.Error(err, "failed to set ssh-config owner", "host", realHostName)
				// The TaskRun already references this snapshot. Remove the task before the
				// snapshot so it cannot start against a ConfigMap that no longer exists.
				// Keep the snapshot when the task cannot be removed.
				if delErr := c.Delete(context.Background(), &provision); delErr != nil && !k8serrors.IsNotFound(delErr) {
					log.Error(delErr, "failed to delete host update task", "host", realHostName)
					return
				}
				if delErr := c.Delete(context.Background(), sshConfigCM); delErr != nil && !k8serrors.IsNotFound(delErr) {
					log.Error(delErr, "failed to delete ssh-config snapshot", "host", realHostName)
				}
				return
			}
		}()
	}
}
