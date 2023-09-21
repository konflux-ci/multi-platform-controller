package taskrun

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/redhat-appstudio/multi-platform-controller/pkg/cloud"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

type DynamicResolver struct {
	cloud.CloudProvider
	SshSecret    string
	Platform     string
	MaxInstances int
}

func (a DynamicResolver) Deallocate(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string, selectedHost string) error {

	instance := tr.Annotations[CloudInstanceId]
	log.Info(fmt.Sprintf("terminating cloud instances %s for TaskRun %s", instance, tr.Name))
	err := a.CloudProvider.TerminateInstance(r.client, log, ctx, cloud.InstanceIdentifier(instance))
	if err != nil {
		log.Error(err, "Failed to terminate EC2 instance")
		return err
	}
	delete(tr.Annotations, CloudInstanceId)
	delete(tr.Labels, AssignedHost)
	delete(tr.Labels, CloudDynamicPlatform)
	return nil
}

func (a DynamicResolver) Allocate(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string, instanceTag string) (reconcile.Result, error) {

	if tr.Annotations[FailedHosts] != "" {
		return reconcile.Result{}, r.createErrorSecret(ctx, tr, secretName, "failed to provision host")
	}

	if tr.Annotations == nil {
		tr.Annotations = map[string]string{}
	}
	//this is called multiple times
	//the first time starts the instance
	//then it can be called repeatedly until the instance has an address
	//this lets us avoid blocking the main thread
	if tr.Annotations[CloudInstanceId] != "" {
		log.Info("attempting to get instance address", "instance", tr.Annotations[CloudInstanceId])
		//we already have an instance, get its address
		address, _ := a.CloudProvider.GetInstanceAddress(r.client, log, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
		if address != "" {
			tr.Labels[AssignedHost] = tr.Annotations[CloudInstanceId]
			tr.Annotations[CloudAddress] = address

			err := launchProvisioningTask(r, ctx, log, tr, secretName, a.SshSecret, address, a.CloudProvider.SshUser())
			if err != nil {
				//ugh, try and unassign
				err := a.CloudProvider.TerminateInstance(r.client, log, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
				if err != nil {
					log.Error(err, "Failed to terminate EC2 instance")
				}

				delete(tr.Labels, AssignedHost)
				delete(tr.Annotations, CloudInstanceId)
				delete(tr.Annotations, CloudDynamicPlatform)
				err = r.client.Update(ctx, tr)
				if err != nil {
					log.Error(err, "Could not unassign task after provisioning failure")
					_ = r.createErrorSecret(ctx, tr, secretName, "Could not unassign task after provisioning failure")
				} else {
					log.Error(err, "Failed to provision AWS host")
					_ = r.createErrorSecret(ctx, tr, secretName, "Failed to provision AWS host "+err.Error())

				}
			}

			return reconcile.Result{}, r.client.Update(ctx, tr)
		} else {
			//we are waiting for the instance to come up
			//so just requeue
			return reconcile.Result{RequeueAfter: time.Second * 10}, nil
		}
	}
	//first check this would not exceed the max tasks
	taskList := v1beta1.TaskRunList{}
	err := r.client.List(ctx, &taskList, client.MatchingLabels{CloudDynamicPlatform: platformLabel(a.Platform)})
	if err != nil {
		return reconcile.Result{}, err
	}
	instanceCount, err := a.CloudProvider.CountInstances(r.client, log, ctx, instanceTag)
	if instanceCount >= a.MaxInstances || err != nil {
		if err != nil {
			log.Error(err, "unable to count running instances, not allocating a new instance out of an abundance of caution")
		}
		if tr.Labels[WaitingForPlatformLabel] == platformLabel(a.Platform) {
			//we are already in a waiting state
			return reconcile.Result{}, nil
		}
		log.Info("Too many running cloud tasks, waiting for existing tasks to finish")
		//no host available
		//add the waiting label
		tr.Labels[WaitingForPlatformLabel] = platformLabel(a.Platform)
		return reconcile.Result{RequeueAfter: time.Minute}, r.client.Update(ctx, tr)
	}
	log.Info(fmt.Sprintf("%d instances are running, creating a new instance", instanceCount))
	log.Info("attempting to launch a new host for " + tr.Name)
	instance, err := a.CloudProvider.LaunchInstance(r.client, log, ctx, tr.Name, instanceTag)
	log.Info("allocated instance", "instance", instance)

	if err != nil {
		//launch failed
		log.Error(err, "Failed to create cloud host")
		_ = r.createErrorSecret(ctx, tr, secretName, "Failed to create cloud host "+err.Error())
		return reconcile.Result{}, nil
	}

	//this seems super prone to conflicts
	//we always read a new version direct from the API server on conflict
	for {
		tr.Annotations[CloudInstanceId] = string(instance)
		tr.Labels[CloudDynamicPlatform] = platformLabel(a.Platform)

		log.Info("updating instance id of cloud host", "instance", instance)
		//add a finalizer to clean up
		controllerutil.AddFinalizer(tr, PipelineFinalizer)
		err = r.client.Update(ctx, tr)
		if err == nil {
			break
		} else if !errors.IsConflict(err) {
			log.Error(err, "failed to update")
			err2 := a.CloudProvider.TerminateInstance(r.client, log, ctx, instance)
			if err2 != nil {
				log.Error(err2, "failed to delete cloud instance")
			}
			return reconcile.Result{}, err
		} else {
			log.Error(err, "conflict updating, retrying")
			err := r.apiReader.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)
			if err != nil {
				log.Error(err, "failed to update")
				err2 := a.CloudProvider.TerminateInstance(r.client, log, ctx, instance)
				if err2 != nil {
					log.Error(err2, "failed to delete cloud instance")
				}
				return reconcile.Result{}, err
			}
			if tr.Annotations == nil {
				tr.Annotations = map[string]string{}
			}
		}
	}

	return reconcile.Result{}, nil

}
