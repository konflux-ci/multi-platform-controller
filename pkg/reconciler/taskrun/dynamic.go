package taskrun

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type DynamicResolver struct {
	cloud.CloudProvider
	sshSecret              string
	platform               string
	maxInstances           int
	instanceTag            string
	timeout                int64
	sudoCommands           string
	additionalInstanceTags map[string]string
	eventRecorder          record.EventRecorder
}

func (r DynamicResolver) Deallocate(taskRun *ReconcileTaskRun, ctx context.Context, tr *v1.TaskRun, secretName string, selectedHost string) error {

	log := logr.FromContextOrDiscard(ctx)
	instance := tr.Annotations[CloudInstanceId]
	log.Info(fmt.Sprintf("terminating cloud instance %s for TaskRun %s", instance, tr.Name))
	err := r.CloudProvider.TerminateInstance(taskRun.client, ctx, cloud.InstanceIdentifier(instance))
	if err != nil {
		log.Error(err, "Failed to terminate EC2 instance")
		r.eventRecorder.Event(tr, "Error", "TerminateFailed", err.Error())
		return err
	}
	delete(tr.Annotations, CloudInstanceId)
	delete(tr.Labels, AssignedHost)
	delete(tr.Labels, CloudDynamicPlatform)
	return nil
}

func (r DynamicResolver) Allocate(taskRun *ReconcileTaskRun, ctx context.Context, tr *v1.TaskRun, secretName string) (reconcile.Result, error) {

	log := logr.FromContextOrDiscard(ctx)
	if tr.Annotations[FailedHosts] != "" {
		return reconcile.Result{}, fmt.Errorf("failed to provision host")
	}

	if tr.Annotations == nil {
		tr.Annotations = map[string]string{}
	}
	if tr.Annotations[AllocationStartTimeAnnotation] != "" && tr.Annotations[CloudInstanceId] != "" {
		allocStart := tr.Annotations[AllocationStartTimeAnnotation]
		startTime, err := strconv.ParseInt(allocStart, 10, 64)
		if err == nil {
			if startTime+r.timeout < time.Now().Unix() {
				err = fmt.Errorf("timed out waiting for instance address")
				log.Error(err, "timed out waiting for instance address")
				//ugh, try and unassign
				terr := r.CloudProvider.TerminateInstance(taskRun.client, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
				if terr != nil {
					log.Error(err, "Failed to terminate instance")
				}
				delete(tr.Labels, AssignedHost)
				delete(tr.Annotations, CloudInstanceId)
				delete(tr.Annotations, CloudDynamicPlatform)
				updateErr := taskRun.client.Update(ctx, tr)
				if updateErr != nil {
					log.Error(updateErr, "Could not unassign task after timeout")
					return reconcile.Result{}, err
				} else {
					return reconcile.Result{}, err
				}
			}
		}
	}
	//this is called multiple times
	//the first time starts the instance
	//then it can be called repeatedly until the instance has an address
	//this lets us avoid blocking the main thread
	if tr.Annotations[CloudInstanceId] != "" {
		log.Info("attempting to get instance address", "instance", tr.Annotations[CloudInstanceId])
		//we already have an instance, get its address
		address, err := r.CloudProvider.GetInstanceAddress(taskRun.client, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
		if err != nil {
			log.Error(err, "Failed to get instance address for cloud host")
			//ugh, try and unassign
			terr := r.CloudProvider.TerminateInstance(taskRun.client, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
			if terr != nil {
				message := fmt.Sprintf("Failed to terminate %s instance for %s", r.instanceTag, tr.Name)
				r.eventRecorder.Event(tr, "Normal", "TerminateFailed", message)
				log.Error(terr, message)
			}
			delete(tr.Labels, AssignedHost)
			delete(tr.Annotations, CloudInstanceId)
			delete(tr.Annotations, CloudDynamicPlatform)
			updateErr := taskRun.client.Update(ctx, tr)
			if updateErr != nil {
				log.Error(updateErr, "Could not unassign task after instance address failure")
				return reconcile.Result{}, err
			} else {
				return reconcile.Result{}, err
			}
		} else if address != "" {
			tr.Labels[AssignedHost] = tr.Annotations[CloudInstanceId]
			tr.Annotations[CloudAddress] = address
			err := taskRun.client.Update(ctx, tr)
			if err != nil {
				return reconcile.Result{}, err
			}
			message := fmt.Sprintf("launching %s provisioning task for %s", r.instanceTag, tr.Name)
			log.Info(message)
			r.eventRecorder.Event(tr, "Normal", "Provisioning", message)
			err = launchProvisioningTask(taskRun, ctx, tr, secretName, r.sshSecret, address, r.CloudProvider.SshUser(), r.platform, r.sudoCommands)
			if err != nil {
				//ugh, try and unassign
				err := r.CloudProvider.TerminateInstance(taskRun.client, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
				if err != nil {
					log.Error(err, "Failed to terminate instance")
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
	instanceCount, err := r.CloudProvider.CountInstances(taskRun.client, ctx, r.instanceTag)
	if instanceCount >= r.maxInstances || err != nil {
		if err != nil {
			log.Error(err, "unable to count running instances, not launching a new instance out of an abundance of caution")
			log.Error(err, "Failed to count existing cloud instances")
			return reconcile.Result{}, err
		}
		message := fmt.Sprintf("%d of %d maxInstances running for %s, waiting for existing tasks to finish before provisioning for ", instanceCount, r.maxInstances, r.instanceTag)
		r.eventRecorder.Event(tr, "Warning", "Pending", message)
		log.Info(message)
		if tr.Labels[WaitingForPlatformLabel] == platformLabel(r.platform) {
			//we are already in a waiting state
			return reconcile.Result{RequeueAfter: time.Minute}, nil
		}
		//no host available
		//add the waiting label
		tr.Labels[WaitingForPlatformLabel] = platformLabel(r.platform)
		if err := taskRun.client.Update(ctx, tr); err != nil {
			log.Error(err, "Failed to update task with waiting label. Will retry.")
		}
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	delete(tr.Labels, WaitingForPlatformLabel)
	startTime := time.Now().Unix()
	tr.Annotations[AllocationStartTimeAnnotation] = strconv.FormatInt(startTime, 10)

	message := fmt.Sprintf("%d instances are running for %s, creating a new instance %s", instanceCount, r.instanceTag, tr.Name)
	r.eventRecorder.Event(tr, "Normal", "Launching", message)
	instance, err := r.CloudProvider.LaunchInstance(taskRun.client, ctx, tr.Name, r.instanceTag, r.additionalInstanceTags)

	if err != nil {
		launchErr := err
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
			return reconcile.Result{}, launchErr
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
	message = fmt.Sprintf("launched %s instance for %s", r.instanceTag, tr.Name)
	r.eventRecorder.Event(tr, "Normal", "Launched", message)

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
			err2 := r.CloudProvider.TerminateInstance(taskRun.client, ctx, instance)
			if err2 != nil {
				log.Error(err2, "failed to delete cloud instance")
			}
			return reconcile.Result{}, err
		} else {
			log.Error(err, "conflict updating, retrying")
			err := taskRun.apiReader.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)
			if err != nil {
				log.Error(err, "failed to update")
				err2 := r.CloudProvider.TerminateInstance(taskRun.client, ctx, instance)
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

	return reconcile.Result{RequeueAfter: 2 * time.Minute}, nil

}
