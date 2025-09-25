package taskrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"knative.dev/pkg/kmeta"

	"github.com/go-logr/logr"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ErrAllHostsFailed is returned when all hosts matching the requested platform have already failed for this TaskRun.
var ErrAllHostsFailed = errors.New("all available hosts for the platform have failed")

type HostPool struct {
	hosts          map[string]*Host
	targetPlatform string
}

func (hp HostPool) Allocate(r *ReconcileTaskRun, ctx context.Context, tr *v1.TaskRun, secretName string) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx)

	if len(hp.hosts) == 0 {
		//no hosts configured
		return reconcile.Result{}, fmt.Errorf("no hosts configured")
	}
	failedString := tr.Annotations[FailedHosts]
	failed := strings.Split(failedString, ",")

	//get all existing runs that are assigned to a host
	taskList := v1.TaskRunList{}
	err := r.client.List(ctx, &taskList, client.HasLabels{AssignedHost})
	if err != nil {
		return reconcile.Result{}, err
	}
	hostCount := map[string]int{}
	for _, tr := range taskList.Items {
		if tr.Labels[TaskTypeLabel] == "" {
			host := tr.Labels[AssignedHost]
			hostCount[host] = hostCount[host] + 1
		}
	}

	//now select the host with the most free spots
	//this algorithm is not very complex

	var selected *Host
	freeSpots := 0

	// We need to track two separate conditions:
	// 1. If any hosts for the platform exist at all.
	// 2. If all hosts that do exist have already failed.
	platformHostsExists := false
	allPlatformHostsFailed := true
	for k, v := range hp.hosts {
		if v.Platform != hp.targetPlatform {
			log.Info("ignoring host with non-matching platform", "host", k, "targetPlatform", hp.targetPlatform, "hostPlatform", v.Platform)
			continue
		}
		// At this point, we know at least one host for the platform exists.
		platformHostsExists = true

		if slices.Contains(failed, k) {
			log.Info("ignoring already failed host", "host", k, "targetPlatform", hp.targetPlatform, "hostPlatform", v.Platform)
			continue
		}

		// If we've gotten this far, we've found a host for our platform that hasn't failed.
		allPlatformHostsFailed = false
		free := v.Concurrency - hostCount[k]

		log.Info("considering host", "host", k, "freeSlots", free)
		if free > freeSpots {
			selected = v
			freeSpots = free
		}
	}
	// If no hosts for the platform were found at all, return the original error.
	if !platformHostsExists {
		log.Info("no hosts with requested platform", "platform", hp.targetPlatform)
		return reconcile.Result{}, fmt.Errorf("no hosts configured for platform %s", hp.targetPlatform)
	}

	// If hosts for the platform exist, but they have all failed, return a more specific error.
	if allPlatformHostsFailed {
		log.Info("all available hosts for the platform have already failed", "platform", hp.targetPlatform, "failedHosts", failedString)
		// By returning a specific error type, we allow the caller (like a dynamic pool)
		// to gracefully handle this specific case instead of treating it as a fatal error.
		return reconcile.Result{}, fmt.Errorf("%w: %s", ErrAllHostsFailed, failedString)
	}
	if selected == nil {
		if tr.Labels[WaitingForPlatformLabel] == platformLabel(hp.targetPlatform) {
			//we are already in a waiting state
			return reconcile.Result{}, nil
		}
		log.Info("no host found, waiting for one to become available")
		//no host available
		//add the waiting label
		//TODO: is the requeue actually a good idea?
		//TODO: timeout
		tr.Labels[WaitingForPlatformLabel] = platformLabel(hp.targetPlatform)
		err = UpdateTaskRunWithRetry(ctx, r.client, r.apiReader, tr)
		if err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}

	log.Info("allocated host", "host", selected.Name)
	tr.Labels[AssignedHost] = selected.Name
	delete(tr.Labels, WaitingForPlatformLabel)
	//add a finalizer to clean up the secret
	controllerutil.AddFinalizer(tr, PipelineFinalizer)
	err = UpdateTaskRunWithRetry(ctx, r.client, r.apiReader, tr)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = launchProvisioningTask(r, ctx, tr, secretName, selected.Secret, selected.Address, selected.User, hp.targetPlatform, "")

	if err != nil {
		//ugh, try and unassign
		log.Error(err, "failed to launch provisioning task, unassigning host")
		delete(tr.Labels, AssignedHost)
		controllerutil.RemoveFinalizer(tr, PipelineFinalizer)
		updateErr := UpdateTaskRunWithRetry(ctx, r.client, r.apiReader, tr)
		if updateErr != nil {
			log.Error(updateErr, "Could not unassign task after provisioning failure")
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, fmt.Errorf("failed to provision host: %v", err)
	}
	return reconcile.Result{}, nil
}

func (hp HostPool) Deallocate(r *ReconcileTaskRun, ctx context.Context, tr *v1.TaskRun, secretName string, selectedHost string) error {
	log := logr.FromContextOrDiscard(ctx)
	selected := hp.hosts[selectedHost]
	if selected != nil {
		labelMap := map[string]string{TaskTypeLabel: TaskTypeClean, UserTaskName: tr.Name, UserTaskNamespace: tr.Namespace, TargetPlatformLabel: platformLabel(hp.targetPlatform)}
		list := v1.TaskRunList{}
		err := r.client.List(ctx, &list, client.MatchingLabels(labelMap))
		if err != nil {
			log.Error(err, "failed to check for existing cleanup task")
		} else {
			if len(list.Items) > 0 {
				log.Info("cleanup task already exists")
				return nil
			}
		}

		log.Info("starting cleanup task")
		//kick off the clean task
		cleanup := v1.TaskRun{}
		cleanup.Name = kmeta.ChildName(tr.Name, "-cleanup")
		cleanup.Namespace = r.operatorNamespace
		cleanup.Labels = labelMap
		cleanup.Spec.TaskRef = &v1.TaskRef{Name: "clean-shared-host"}
		cleanup.Spec.Retries = 3
		compute := map[v12.ResourceName]resource.Quantity{v12.ResourceCPU: resource.MustParse("100m"), v12.ResourceMemory: resource.MustParse("128Mi")}
		cleanup.Spec.ComputeResources = &v12.ResourceRequirements{Requests: compute}
		cleanup.Spec.Workspaces = []v1.WorkspaceBinding{{Name: "ssh", Secret: &v12.SecretVolumeSource{SecretName: selected.Secret}}}
		cleanup.Spec.ServiceAccountName = ServiceAccountName //TODO: special service account for this
		cleanup.Spec.Params = []v1.Param{
			{
				Name:  "SECRET_NAME",
				Value: *v1.NewStructuredValues(secretName),
			},
			{
				Name:  "TASKRUN_NAME",
				Value: *v1.NewStructuredValues(tr.Name),
			},
			{
				Name:  "NAMESPACE",
				Value: *v1.NewStructuredValues(tr.Namespace),
			},
			{
				Name:  "HOST",
				Value: *v1.NewStructuredValues(selected.Address),
			},
			{
				Name:  "USER",
				Value: *v1.NewStructuredValues(selected.User),
			},
		}
		err = r.client.Create(ctx, &cleanup)
		return err
	}
	return nil
}
