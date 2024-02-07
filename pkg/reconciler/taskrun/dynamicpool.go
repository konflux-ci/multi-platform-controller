package taskrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/redhat-appstudio/multi-platform-controller/pkg/cloud"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

type DynamicHostPool struct {
	CloudProvider cloud.CloudProvider
	SshSecret     string
	Platform      string
	MaxInstances  int
	Concurrency   int
	MaxAge        time.Duration
	InstanceTag   string
}

func (a DynamicHostPool) buildHostPool(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, instanceTag string) (*HostPool, int, error) {
	ret := map[string]*Host{}
	instances, err := a.CloudProvider.ListInstances(r.client, log, ctx, instanceTag)
	if err != nil {
		return nil, 0, err
	}
	oldInstanceCount := 0
	for _, instTmp := range instances {
		inst := instTmp
		if inst.StartTime.Add(a.MaxAge).Before(time.Now()) {
			// These are shut down on deallocation
			oldInstanceCount++

			idle, err := a.isHostIdle(r, ctx, string(inst.InstanceId))
			if err == nil {
				if idle {
					log.Info("deallocating old instance", "instance", inst.InstanceId)
					err = a.CloudProvider.TerminateInstance(r.client, log, ctx, inst.InstanceId)
					if err != nil {
						log.Error(err, "unable to shut down instance", "instance", inst.InstanceId)
					}
				}
			}
		} else {
			log.Info(fmt.Sprintf("found instance %s", inst.InstanceId))
			ret[string(inst.InstanceId)] = &Host{Name: string(inst.InstanceId), Address: inst.Address, User: a.CloudProvider.SshUser(), Concurrency: a.Concurrency, Platform: a.Platform, Secret: a.SshSecret, StartTime: &inst.StartTime}
		}
	}
	return &HostPool{hosts: ret, targetPlatform: a.Platform}, oldInstanceCount, nil
}

func (a DynamicHostPool) Deallocate(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1.TaskRun, secretName string, selectedHost string) error {

	hostPool, oldInstanceCount, err := a.buildHostPool(r, ctx, log, a.InstanceTag)
	if err != nil {
		return err
	}
	err = hostPool.Deallocate(r, ctx, log, tr, secretName, selectedHost)
	if err != nil {
		return err
	}
	if oldInstanceCount > 0 {
		// Maybe this is an old instance
		startTime := hostPool.hosts[selectedHost].StartTime
		if startTime.Add(a.MaxAge).Before(time.Now()) {
			// Old host, check if other tasks are using it
			idle, err := a.isHostIdle(r, ctx, selectedHost)
			if err != nil {
				return err
			}
			if idle {
				log.Info("deallocating old instance", "instance", selectedHost)
				err = a.CloudProvider.TerminateInstance(r.client, log, ctx, cloud.InstanceIdentifier(selectedHost))
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (a DynamicHostPool) isHostIdle(r *ReconcileTaskRun, ctx context.Context, selectedHost string) (bool, error) {
	trs := v1.TaskRunList{}
	err := r.client.List(ctx, &trs, client.MatchingLabels{AssignedHost: selectedHost})
	if err != nil {
		return false, err
	}
	return len(trs.Items) == 0, nil
}

func (a DynamicHostPool) Allocate(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1.TaskRun, secretName string, instanceTag string) (reconcile.Result, error) {

	hostPool, oldInstanceCount, err := a.buildHostPool(r, ctx, log, instanceTag)
	if err != nil {
		return reconcile.Result{}, err
	}
	if len(hostPool.hosts) > 0 {
		_, err = hostPool.Allocate(r, ctx, log, tr, secretName, instanceTag)
		if err != nil {
			log.Error(err, "could not allocate host from pool")
		}
		if tr.Labels == nil || tr.Labels[WaitingForPlatformLabel] == "" {

			log.Info("returning, as task is not waiting for a host")
			//We only need to launch an instance if the task run is waiting for a label
			return reconcile.Result{}, err
		}
	}
	log.Info("could not allocate existing host, attempting to start a new one")

	// Count will handle instances that are not ready yet
	count, err := a.CloudProvider.CountInstances(r.client, log, ctx, instanceTag)
	if err != nil {
		return reconcile.Result{}, err
	}
	log.Info(fmt.Sprintf("%d instances running", count))
	// We don't count old instances towards the total, as they will shut down soon
	if count-oldInstanceCount >= a.MaxInstances {
		log.Info("cannot provision new instances")
		// Too many instances, we just have to wait
		return reconcile.Result{RequeueAfter: time.Minute}, err
	}
	name, err := getRandomString(8)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Counter intuitively we don't need the instance id
	// It will be picked up on the list call
	log.Info(fmt.Sprintf("launching instance %s", name))
	inst, err := a.CloudProvider.LaunchInstance(r.client, log, ctx, name, instanceTag)
	if err != nil {
		return reconcile.Result{}, err
	}

	log.Info("allocated instance", "instance", inst)
	return reconcile.Result{RequeueAfter: time.Minute}, err

}

func getRandomString(length int) (string, error) {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[0:length], nil
}
