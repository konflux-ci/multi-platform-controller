package taskrun

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/redhat-appstudio/multi-platform-controller/pkg/cloud"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strconv"
	"time"
)

type DynamicResolver struct {
	cloud.CloudProvider
	sshSecret    string
	platform     string
	maxInstances int
	instanceTag  string
}

func (r DynamicResolver) Deallocate(taskRun *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1.TaskRun, secretName string, selectedHost string) error {

	instance := tr.Annotations[CloudInstanceId]
	log.Info(fmt.Sprintf("terminating cloud instances %s for TaskRun %s", instance, tr.Name))
	err := r.CloudProvider.TerminateInstance(taskRun.client, log, ctx, cloud.InstanceIdentifier(instance))
	if err != nil {
		log.Error(err, "Failed to terminate EC2 instance")
		return err
	}
	delete(tr.Annotations, CloudInstanceId)
	delete(tr.Labels, AssignedHost)
	delete(tr.Labels, CloudDynamicPlatform)
	return nil
}

func (r DynamicResolver) Allocate(taskRun *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1.TaskRun, secretName string) (reconcile.Result, error) {

	if tr.Annotations[FailedHosts] != "" {
		return reconcile.Result{}, fmt.Errorf("failed to provision host")
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
		address, _ := r.CloudProvider.GetInstanceAddress(taskRun.client, log, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
		if address != "" {
			tr.Labels[AssignedHost] = tr.Annotations[CloudInstanceId]
			tr.Annotations[CloudAddress] = address
			err := taskRun.client.Update(ctx, tr)
			if err != nil {
				return reconcile.Result{}, err
			}
			err = launchProvisioningTask(taskRun, ctx, log, tr, secretName, r.sshSecret, address, r.CloudProvider.SshUser(), r.platform)
			if err != nil {
				//ugh, try and unassign
				err := r.CloudProvider.TerminateInstance(taskRun.client, log, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
				if err != nil {
					log.Error(err, "Failed to terminate EC2 instance")
				}

				delete(tr.Labels, AssignedHost)
				delete(tr.Annotations, CloudInstanceId)
				delete(tr.Annotations, CloudDynamicPlatform)
				updateErr := taskRun.client.Update(ctx, tr)
				if updateErr != nil {
					log.Error(updateErr, "Could not unassign task after provisioning failure")
					return reconcile.Result{}, err
				} else {
					log.Error(err, "Failed to provision cloud host")
					return reconcile.Result{}, err

				}
			}
			return reconcile.Result{}, nil
		} else {
			//we are waiting for the instance to come up
			//so just requeue
			return reconcile.Result{RequeueAfter: time.Second * 10}, nil
		}
	}
	//first check this would not exceed the max tasks
	instanceCount, err := r.CloudProvider.CountInstances(taskRun.client, log, ctx, r.instanceTag)
	if instanceCount >= r.maxInstances || err != nil {
		if err != nil {
			log.Error(err, "unable to count running instances, not allocating a new instance out of an abundance of caution")
			log.Error(err, "Failed to count existing cloud instances")
			return reconcile.Result{}, err
		}
		if tr.Labels[WaitingForPlatformLabel] == platformLabel(r.platform) {
			//we are already in a waiting state
			return reconcile.Result{}, nil
		}
		log.Info("Too many running cloud tasks, waiting for existing tasks to finish")
		//no host available
		//add the waiting label
		tr.Labels[WaitingForPlatformLabel] = platformLabel(r.platform)
		return reconcile.Result{RequeueAfter: time.Minute}, taskRun.client.Update(ctx, tr)
	}
	delete(tr.Labels, WaitingForPlatformLabel)
	startTime := time.Now().Unix()
	tr.Annotations[AllocationStartTimeAnnotation] = strconv.FormatInt(startTime, 10)
	log.Info(fmt.Sprintf("%d instances are running, creating a new instance", instanceCount))
	log.Info("attempting to launch a new host for " + tr.Name)
	instance, err := r.CloudProvider.LaunchInstance(taskRun.client, log, ctx, tr.Name, r.instanceTag)

	if err != nil {
		//launch failed
		log.Error(err, "Failed to create cloud host")
		failureCount := 0
		existingFailureString := tr.Annotations[CloudFailures]
		if existingFailureString != "" {
			failureCount, err = strconv.Atoi(existingFailureString)
			if err != nil {
				log.Error(err, "failed to parse failure count")
				return reconcile.Result{}, err
			}
		}
		if failureCount == 2 {
			log.Error(err, "failed to create cloud host, retries exceeded ")
			return reconcile.Result{}, err
		}
		failureCount++
		tr.Annotations[CloudFailures] = strconv.Itoa(failureCount)
		err = taskRun.client.Update(ctx, tr)
		if err != nil {
			//todo: handle conflict properly, for now you get an extra retry
			log.Error(err, "failed to update failure count")
		}

		return reconcile.Result{RequeueAfter: time.Second * 20}, nil
	}
	log.Info("allocated instance", "instance", instance)

	//this seems super prone to conflicts
	//we always read a new version direct from the API server on conflict
	for {
		tr.Annotations[CloudInstanceId] = string(instance)
		tr.Labels[CloudDynamicPlatform] = platformLabel(r.platform)

		log.Info("updating instance id of cloud host", "instance", instance)
		//add a finalizer to clean up
		controllerutil.AddFinalizer(tr, PipelineFinalizer)
		err = taskRun.client.Update(ctx, tr)
		if err == nil {
			break
		} else if !errors.IsConflict(err) {
			log.Error(err, "failed to update")
			err2 := r.CloudProvider.TerminateInstance(taskRun.client, log, ctx, instance)
			if err2 != nil {
				log.Error(err2, "failed to delete cloud instance")
			}
			return reconcile.Result{}, err
		} else {
			log.Error(err, "conflict updating, retrying")
			err := taskRun.apiReader.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)
			if err != nil {
				log.Error(err, "failed to update")
				err2 := r.CloudProvider.TerminateInstance(taskRun.client, log, ctx, instance)
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
