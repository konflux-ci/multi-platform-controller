package taskrun

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
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
		log.Error(err, "Failed to terminate dynamic instance")
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
				unassignErr := r.removeInstanceFromTask(taskRun, ctx, tr)
				if unassignErr != nil {
					log.Error(unassignErr, "Could not unassign task after timeout")
				}
				return reconcile.Result{}, err
			}
		}
	}
	// This section is called multiple times due to the task being re-queued:
	// The 1st time starts the instance.
	// Any future iterations are for repeatedly attempting to get the instance's IP address.
	// This code structure avoids blocking the main thread.
	if tr.Annotations[CloudInstanceId] != "" {
		log.Info("Attempting to get instance's IP address", "instance", tr.Annotations[CloudInstanceId])
		//An instance already exists, so get its IP address
		address, err := r.CloudProvider.GetInstanceAddress(taskRun.client, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
		if err != nil { // A permanent error occurred when fetching the IP address for the VM
			log.Error(err, "failed to get instance address for cloud host")
			//Try to delete the instance and unassign it from the TaskRun
			terr := r.CloudProvider.TerminateInstance(taskRun.client, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
			if terr != nil {
				message := fmt.Sprintf("failed to terminate %s instance for %s", r.instanceTag, tr.Name)
				r.eventRecorder.Event(tr, "Normal", "TerminateFailed", message)
				log.Error(terr, message)
			}
			unassignErr := r.removeInstanceFromTask(taskRun, ctx, tr)
			if unassignErr != nil {
				log.Error(unassignErr, "failed to unassign instance from task after instance address retrieval failure")
			}
			return reconcile.Result{}, err
		} else if address != "" { // An IP address was successfully retrieved for the the VM
			tr.Labels[AssignedHost] = tr.Annotations[CloudInstanceId]
			tr.Annotations[CloudAddress] = address
			err := UpdateTaskRunWithRetry(ctx, taskRun.client, taskRun.apiReader, tr)
			if err != nil {
				return reconcile.Result{}, err
			}
			message := fmt.Sprintf("starting %s provisioning task for %s", r.instanceTag, tr.Name)
			log.Info(message)
			r.eventRecorder.Event(tr, "Normal", "Provisioning", message)
			err = launchProvisioningTask(taskRun, ctx, tr, secretName, r.sshSecret, address, r.CloudProvider.SshUser(), r.platform, r.sudoCommands)
			if err != nil {
				//Try to delete the instance and unassign it from the TaskRun
				terr := r.CloudProvider.TerminateInstance(taskRun.client, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
				if terr != nil {
					message := fmt.Sprintf("failed to terminate %s instance for %s", r.instanceTag, tr.Name)
					log.Error(terr, message)
				}
				unassignErr := r.removeInstanceFromTask(taskRun, ctx, tr)
				if unassignErr != nil {
					log.Error(unassignErr, "failed to unassign instance from task after provisioning failure")
				} else {
					log.Error(err, "failed to provision cloud host")
				}
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		} else { // A transient error (that wasn't returned) occurred when fetching the IP address for the VM
			state, err := r.CloudProvider.GetState(taskRun.client, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
			requeueTime := time.Minute
			if err != nil { //An error occurred while getting the VM state; re-queue quickly since random API errors are prominent
				log.Error(
					err,
					"failed to get the state for instance",
					"instanceId", cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]),
				)
				requeueTime = time.Second * 10
			} else if state == cloud.FailedState { //VM is in a failed state; try to delete the instance and unassign it from the TaskRun
				log.Info("VM instance is in a failed state; will attempt to terminate, unassign from task")
				terr := r.CloudProvider.TerminateInstance(taskRun.client, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
				if terr != nil {
					message := fmt.Sprintf("failed to terminate %s instance for %s", r.instanceTag, tr.Name)
					r.eventRecorder.Event(tr, "Normal", "TerminateFailed", message)
					log.Error(terr, message)
				}
				unassignErr := r.removeInstanceFromTask(taskRun, ctx, tr)
				if unassignErr != nil {
					msg := fmt.Sprintf("failed to unassign instance %s from task after instance termination", r.instanceTag)
					log.Error(unassignErr, msg)
				}
			}
			//Always try to re-queue the task
			return reconcile.Result{RequeueAfter: requeueTime}, nil
		}
	}
	// First check that creating this VM would not exceed the maximum VM platforms configured
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
		if err := UpdateTaskRunWithRetry(ctx, taskRun.client, taskRun.apiReader, tr); err != nil {
			log.Error(err, "Failed to update task with waiting label. Will retry.")
		}
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	delete(tr.Labels, WaitingForPlatformLabel)
	startTime := time.Now().Unix()
	tr.Annotations[AllocationStartTimeAnnotation] = strconv.FormatInt(startTime, 10)

	message := fmt.Sprintf("%d instances are running for %s, creating a new instance %s", instanceCount, r.instanceTag, tr.Name)
	r.eventRecorder.Event(tr, "Normal", "Launching", message)
	taskRunID := fmt.Sprintf("%s:%s", tr.Namespace, tr.Name)
	instance, err := r.CloudProvider.LaunchInstance(taskRun.client, ctx, taskRunID, r.instanceTag, r.additionalInstanceTags)

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
		err = UpdateTaskRunWithRetry(ctx, taskRun.client, taskRun.apiReader, tr)
		if err != nil {
			//todo: handle conflict properly, for now you get an extra retry
			log.Error(err, "failed to update failure count")
		}

		return reconcile.Result{RequeueAfter: time.Second * 20}, nil
	}
	message = fmt.Sprintf("launched %s instance for %s", r.instanceTag, tr.Name)
	r.eventRecorder.Event(tr, "Normal", "Launched", message)

	// Set the instance ID and platform label, then update with conflict resilience
	tr.Annotations[CloudInstanceId] = string(instance)
	tr.Labels[CloudDynamicPlatform] = platformLabel(r.platform)
	//add a finalizer to clean up
	controllerutil.AddFinalizer(tr, PipelineFinalizer)

	log.Info("updating instance id of cloud host", "instance", instance)
	err = UpdateTaskRunWithRetry(ctx, taskRun.client, taskRun.apiReader, tr)
	if err != nil {
		log.Error(err, "failed to update TaskRun with instance ID after retries")
		err2 := r.CloudProvider.TerminateInstance(taskRun.client, ctx, instance)
		if err2 != nil {
			log.Error(err2, "failed to delete cloud instance")
		}
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: 2 * time.Minute}, nil

}

// Tries to remove the instance information from the task and returns a non-nil error if it was unable to.
func (dr DynamicResolver) removeInstanceFromTask(reconcileTaskRun *ReconcileTaskRun, ctx context.Context, taskRun *v1.TaskRun) error {
	delete(taskRun.Labels, AssignedHost)
	delete(taskRun.Annotations, CloudInstanceId)
	delete(taskRun.Annotations, CloudDynamicPlatform)
	return UpdateTaskRunWithRetry(ctx, reconcileTaskRun.client, reconcileTaskRun.apiReader, taskRun)
}
