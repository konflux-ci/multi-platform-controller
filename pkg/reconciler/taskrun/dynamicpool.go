package taskrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type DynamicHostPool struct {
	cloudProvider          cloud.CloudProvider
	sshSecret              string
	platform               string
	maxInstances           int
	concurrency            int
	maxAge                 time.Duration
	instanceTag            string
	additionalInstanceTags map[string]string
}

func (a DynamicHostPool) InstanceTag() string {
	return a.instanceTag
}

func (a DynamicHostPool) buildHostPool(r *ReconcileTaskRun, ctx context.Context, instanceTag string) (*HostPool, int, error) {
	log := logr.FromContextOrDiscard(ctx)
	ret := map[string]*Host{}
	instances, err := a.cloudProvider.ListInstances(r.client, ctx, instanceTag)
	if err != nil {
		return nil, 0, err
	}
	oldInstanceCount := 0
	for _, instTmp := range instances {
		inst := instTmp
		if inst.StartTime.Add(a.maxAge).Before(time.Now()) {
			// These are shut down on deallocation
			oldInstanceCount++

			idle, err := a.isHostIdle(r, ctx, string(inst.InstanceId))
			if err == nil {
				if idle {
					log.Info("deallocating old instance", "instance", inst.InstanceId)
					err = a.cloudProvider.TerminateInstance(r.client, ctx, inst.InstanceId)
					if err != nil {
						log.Error(err, "unable to shut down instance", "instance", inst.InstanceId)
					}
				}
			}
		} else {
			log.Info(fmt.Sprintf("found instance %s", inst.InstanceId))
			ret[string(inst.InstanceId)] = &Host{Name: string(inst.InstanceId), Address: inst.Address, User: a.cloudProvider.SshUser(), Concurrency: a.concurrency, Platform: a.platform, Secret: a.sshSecret, StartTime: &inst.StartTime}
		}
	}
	return &HostPool{hosts: ret, targetPlatform: a.platform}, oldInstanceCount, nil
}

func (a DynamicHostPool) Deallocate(r *ReconcileTaskRun, ctx context.Context, tr *v1.TaskRun, secretName string, selectedHost string) error {

	hostPool, oldInstanceCount, err := a.buildHostPool(r, ctx, a.instanceTag)
	if err != nil {
		return err
	}
	err = hostPool.Deallocate(r, ctx, tr, secretName, selectedHost)
	if err != nil {
		return err
	}
	if oldInstanceCount > 0 {
		// Maybe this is an old instance
		if hostPool.hosts[selectedHost] == nil {
			// Old host, check if other tasks are using it
			idle, err := a.isHostIdle(r, ctx, selectedHost)
			if err != nil {
				return err
			}
			if idle {
				log := logr.FromContextOrDiscard(ctx)
				log.Info("deallocating old instance", "instance", selectedHost)
				err = a.cloudProvider.TerminateInstance(r.client, ctx, cloud.InstanceIdentifier(selectedHost))
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

func (a DynamicHostPool) Allocate(r *ReconcileTaskRun, ctx context.Context, tr *v1.TaskRun, secretName string) (reconcile.Result, error) {

	log := logr.FromContextOrDiscard(ctx)
	hostPool, oldInstanceCount, err := a.buildHostPool(r, ctx, a.instanceTag)
	if err != nil {
		return reconcile.Result{}, err
	}

	var allocationErr error
	if len(hostPool.hosts) > 0 {
		_, allocationErr = hostPool.Allocate(r, ctx, tr, secretName)
		if allocationErr != nil && !errors.Is(allocationErr, ErrAllHostsFailed) {
			log.Error(allocationErr, "could not allocate host from pool")
			return reconcile.Result{}, allocationErr
		}
		if allocationErr == nil && (tr.Labels == nil || tr.Labels[WaitingForPlatformLabel] == "") {
			log.Info("successfully allocated host from existing pool")
			return reconcile.Result{}, nil
		}
	}
	log.Info("could not allocate existing host, attempting to start a new one")

	// Count will handle instances that are not ready yet
	count, err := a.cloudProvider.CountInstances(r.client, ctx, a.instanceTag)
	if err != nil {
		return reconcile.Result{}, err
	}
	log.Info(fmt.Sprintf("%d instances running", count))
	// We don't count old instances towards the total, as they will shut down soon
	if count-oldInstanceCount >= a.maxInstances {
		log.Info("cannot provision new instances, pool is at capacity")
		// Pool is full, and we couldn't allocate. Return the original allocation error.
		return reconcile.Result{RequeueAfter: time.Minute}, allocationErr
	}
	name, err := getRandomString(8)
	if err != nil {
		return reconcile.Result{}, err
	}

	delete(tr.Labels, WaitingForPlatformLabel)
	// Counter intuitively we don't need the instance id
	// It will be picked up on the list call
	log.Info(fmt.Sprintf("launching instance %s", name))
	taskRunID := fmt.Sprintf("%s:%s", tr.Namespace, tr.Name)
	inst, err := a.cloudProvider.LaunchInstance(r.client, ctx, taskRunID, a.instanceTag, a.additionalInstanceTags)
	if err != nil {
		return reconcile.Result{}, err
	}

	log.Info("allocated instance", "instance", inst)
	// The operation to launch a new instance was successful, so we return nil.
	// The reconciler will requeue and assign the new instance once it's ready.
	return reconcile.Result{RequeueAfter: time.Minute}, nil
}

func getRandomString(length int) (string, error) {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[0:length], nil
}
