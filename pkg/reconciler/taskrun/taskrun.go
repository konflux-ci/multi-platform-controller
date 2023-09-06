package taskrun

import (
	"context"
	"fmt"
	errors2 "github.com/pkg/errors"
	"github.com/stuartwdouglas/multi-arch-host-resolver/pkg/aws"
	"github.com/stuartwdouglas/multi-arch-host-resolver/pkg/cloud"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/strings/slices"
	"knative.dev/pkg/apis"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
)

const (
	//TODO eventually we'll need to decide if we want to make this tuneable
	contextTimeout = 300 * time.Second

	SecretPrefix   = "multi-arch-ssh-"
	ConfigMapLabel = "build.appstudio.redhat.com/multi-arch-config"

	//user level labels that specify a task needs to be executed on a remote host
	MultiArchLabel       = "build.appstudio.redhat.com/multi-arch-required"
	MultiArchSecretLabel = "build.appstudio.redhat.com/multi-arch-secret"

	AssignedHost     = "build.appstudio.redhat.com/assigned-host"
	FailedHosts      = "build.appstudio.redhat.com/failed-hosts"
	CloudInstanceId  = "build.appstudio.redhat.com/cloud-instance-id"
	CloudAddress     = "build.appstudio.redhat.com/cloud-address"
	CloudDynamicArch = "build.appstudio.redhat.com/cloud-dynamic-arch"

	UserTaskName      = "build.appstudio.redhat.com/user-task-name"
	UserTaskNamespace = "build.appstudio.redhat.com/user-task-namespace"

	WaitingForArchLabel = "build.appstudio.redhat.com/waiting-for-arch"
	PipelineFinalizer   = "appstudio.io/multi-arch-finalizer"
	HostConfig          = "host-config"

	TaskTypeLabel     = "build.appstudio.redhat.com/task-type"
	TaskTypeProvision = "provision"
	TaskTypeClean     = "clean"

	ServiceAccountName = "multi-arch-controller"

	ArchParam            = "ARCH"
	DynamicArchitectures = "dynamic.architectures"
)

type ReconcileTaskRun struct {
	client            client.Client
	scheme            *runtime.Scheme
	eventRecorder     record.EventRecorder
	operatorNamespace string

	cloudProviders map[string]func(arch string, config map[string]string) cloud.CloudProvider
}

func newReconciler(mgr ctrl.Manager, operatorNamespace string) reconcile.Reconciler {
	return &ReconcileTaskRun{
		client:            mgr.GetClient(),
		scheme:            mgr.GetScheme(),
		eventRecorder:     mgr.GetEventRecorderFor("ComponentBuild"),
		operatorNamespace: operatorNamespace,
		cloudProviders:    map[string]func(arch string, config map[string]string) cloud.CloudProvider{"aws": aws.Ec2Provider},
	}
}

func (r *ReconcileTaskRun) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	// Set the ctx to be Background, as the top-level context for incoming requests.
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	log := ctrl.Log.WithName("taskrun").WithValues("request", request.NamespacedName)
	log.Info("Reconciling")

	pr := v1beta1.TaskRun{}
	prerr := r.client.Get(ctx, request.NamespacedName, &pr)
	if prerr != nil {
		if !errors.IsNotFound(prerr) {
			log.Error(prerr, "Reconcile key %s as TaskRun unexpected error", request.NamespacedName.String())
			return ctrl.Result{}, prerr
		}
	}
	if prerr != nil {
		msg := "Reconcile key received not found errors for TaskRuns (probably deleted): " + request.NamespacedName.String()
		log.Info(msg)
		return ctrl.Result{}, nil
	}

	switch {
	case prerr == nil:
		return r.handleTaskRunReceived(ctx, &log, &pr)
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileTaskRun) handleTaskRunReceived(ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun) (reconcile.Result, error) {
	if tr.Labels == nil {
		return reconcile.Result{}, nil
	}
	taskType := tr.Labels[TaskTypeLabel]
	if taskType == TaskTypeClean {
		return r.handleCleanTask(ctx, log, tr)
	}
	if taskType == TaskTypeProvision {
		return r.handleProvisionTask(ctx, log, tr)
	}

	if tr.Labels == nil || tr.Labels[MultiArchLabel] == "" {
		//this is not something we need to be concerned with
		return reconcile.Result{}, nil
	}
	return r.handleUserTask(ctx, log, tr)
}

// called when a task has finished, we look for waiting tasks
// and then potentially requeue one of them
func (r *ReconcileTaskRun) handleWaitingTasks(ctx context.Context, log *logr.Logger, arch string) (reconcile.Result, error) {

	//try and requeue a waiting task if one exists
	taskList := v1beta1.TaskRunList{}

	err := r.client.List(ctx, &taskList, client.MatchingLabels{WaitingForArchLabel: arch})
	if err != nil {
		return reconcile.Result{}, err
	}
	var oldest *v1beta1.TaskRun
	var oldestTs time.Time
	for i := range taskList.Items {
		tr := taskList.Items[i]
		if oldest == nil || oldestTs.After(tr.CreationTimestamp.Time) {
			oldestTs = tr.CreationTimestamp.Time
			oldest = &tr
		}
	}
	if oldest != nil {
		//remove the waiting label, which will trigger a requeue
		delete(oldest.Labels, WaitingForArchLabel)
		return reconcile.Result{}, r.client.Update(ctx, oldest)
	}
	return reconcile.Result{}, nil

}

func (r *ReconcileTaskRun) handleCleanTask(ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun) (reconcile.Result, error) {
	if tr.Status.CompletionTime == nil {
		return reconcile.Result{}, nil
	}
	success := tr.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
	if !success {
		log.Info("cleanup task failed", "task", tr.Name)
	}
	//leave the TR for an hour
	if tr.Status.CompletionTime.Add(time.Hour).Before(time.Now()) {
		return reconcile.Result{}, r.client.Delete(ctx, tr)
	}
	return reconcile.Result{RequeueAfter: time.Hour}, nil
}

func (r *ReconcileTaskRun) handleProvisionTask(ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun) (reconcile.Result, error) {

	if tr.Status.CompletionTime == nil {
		return reconcile.Result{}, nil
	}
	success := tr.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
	secretName := ""
	for _, i := range tr.Spec.Params {
		if i.Name == "SECRET_NAME" {
			secretName = i.Value.StringVal
			break
		}
	}
	if !success {
		assigned := tr.Labels[AssignedHost]
		if assigned != "" {
			userTr := v1beta1.TaskRun{}
			err := r.client.Get(ctx, types.NamespacedName{Namespace: tr.Labels[UserTaskNamespace], Name: tr.Labels[UserTaskName]}, &userTr)
			if err == nil {
				if userTr.Annotations == nil {
					userTr.Annotations = map[string]string{}
				}
				//add to failed hosts and remove assigned
				//this will cause it to try again
				failed := strings.Split(userTr.Annotations[FailedHosts], ",")
				if failed[0] == "" {
					failed = []string{}
				}
				failed = append(failed, assigned)
				userTr.Annotations[FailedHosts] = strings.Join(failed, ",")
				delete(userTr.Labels, AssignedHost)
				err = r.client.Update(ctx, &userTr)
				if err != nil {
					return reconcile.Result{}, err
				}
				delete(tr.Labels, AssignedHost)
				err := r.client.Update(ctx, tr)
				if err != nil {
					return reconcile.Result{}, err
				}
			}
		}

	} else {
		log.Info("provision task succeeded")
		//verify we ended up with a secret
		secret := v12.Secret{}
		err := r.client.Get(ctx, types.NamespacedName{Namespace: tr.Labels[UserTaskNamespace], Name: secretName}, &secret)
		if err != nil {
			if errors.IsNotFound(err) {
				userTr := v1beta1.TaskRun{}
				err := r.client.Get(ctx, types.NamespacedName{Namespace: tr.Labels[UserTaskNamespace], Name: tr.Labels[UserTaskName]}, &userTr)
				if err != nil {
					return reconcile.Result{}, err
				}
				return reconcile.Result{}, r.createErrorSecret(ctx, &userTr, secretName, "provision task failed to create a secret")
			}
			return reconcile.Result{}, err
		}
	}
	//leave the TR for an hour
	if tr.Status.CompletionTime.Add(time.Hour).Before(time.Now()) {
		return reconcile.Result{}, r.client.Delete(ctx, tr)
	}
	return reconcile.Result{RequeueAfter: time.Hour}, nil

}

// This creates an secret with the 'error' field set
// This will result in the pipeline run immediately failing with the message printed in the logs
func (r *ReconcileTaskRun) createErrorSecret(ctx context.Context, tr *v1beta1.TaskRun, secretName string, msg string) error {
	if controllerutil.AddFinalizer(tr, PipelineFinalizer) {
		err := r.client.Update(ctx, tr)
		if err != nil {
			return err
		}
	}

	secret := v12.Secret{}
	secret.Labels = map[string]string{MultiArchSecretLabel: "true"}
	secret.Namespace = tr.Namespace
	secret.Name = secretName
	err := controllerutil.SetOwnerReference(tr, &secret, r.scheme)
	if err != nil {
		return err
	}

	secret.Data = map[string][]byte{
		"error": []byte(msg),
	}
	err = r.client.Create(ctx, &secret)
	if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileTaskRun) handleUserTask(ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun) (reconcile.Result, error) {

	secretName := SecretPrefix + tr.Name
	if tr.Labels[AssignedHost] != "" {
		return r.handleHostAssigned(ctx, log, tr, secretName)
	} else {
		//if the PR is done we ignore it
		if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
			log.Info("task run already finished, not creating secret")
			return r.removeFinalizer(ctx, log, tr)
		}

		return r.handleHostAllocation(ctx, log, tr, secretName)
	}
}

func extractArch(tr *v1beta1.TaskRun) (string, error) {
	for _, p := range tr.Spec.Params {
		if p.Name == ArchParam {
			return p.Value.StringVal, nil
		}
	}
	return "", errors2.New("failed to determine architecture")
}

func (r *ReconcileTaskRun) handleHostAllocation(ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string) (reconcile.Result, error) {
	log.Info("attempting to allocate host")

	targetArch, err := extractArch(tr)
	if err != nil {
		return reconcile.Result{}, r.createErrorSecret(ctx, tr, secretName, "failed to determine architecture, no ARCH param")
	}

	//lets allocate a host, get the map with host info
	hosts, err := r.hostConfig(ctx, log, targetArch)
	if err != nil {
		//no host config means we can't proceed
		//no point retrying
		if errors.IsNotFound(err) {
			return reconcile.Result{}, r.createErrorSecret(ctx, tr, secretName, "failed to read host config")
		}
		return reconcile.Result{}, err
	}
	return hosts.Allocate(r, ctx, log, tr, secretName)

}

func (r *ReconcileTaskRun) handleHostAssigned(ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string) (reconcile.Result, error) {
	//already exists
	if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
		log.Info("unassigning host from task")

		selectedHost := tr.Labels[AssignedHost]
		arch, err := extractArch(tr)
		if err != nil {
			return reconcile.Result{}, err
		}
		config, err := r.hostConfig(ctx, log, arch)
		if err != nil {
			return reconcile.Result{}, err
		}
		err = config.Deallocate(r, ctx, log, tr, secretName, selectedHost)
		if err != nil {
			log.Error(err, "Failed to deallocate host "+selectedHost)
		}
		controllerutil.RemoveFinalizer(tr, PipelineFinalizer)
		delete(tr.Labels, AssignedHost)
		err = r.client.Update(ctx, tr)
		if err != nil {
			return reconcile.Result{}, err
		}

		secret := v12.Secret{}
		//delete the secret
		err = r.client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: secretName}, &secret)
		if err == nil {
			log.Info("deleting secret from task")
			//PR is done, clean up the secret
			err := r.client.Delete(ctx, &secret)
			if err != nil {
				log.Error(err, "unable to delete secret")
			}
		} else if !errors.IsNotFound(err) {
			log.Error(err, "error deleting secret", "secret", secretName)
			return reconcile.Result{}, err
		} else {
			log.Info("could not find secret", "secret", secretName)
		}
		return r.handleWaitingTasks(ctx, log, arch)
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileTaskRun) hostConfig(ctx context.Context, log *logr.Logger, targetArch string) (ArchitectureConfig, error) {
	cm := v12.ConfigMap{}
	err := r.client.Get(ctx, types.NamespacedName{Namespace: r.operatorNamespace, Name: HostConfig}, &cm)
	if err != nil {
		return nil, err
	}

	dynamic := cm.Data[DynamicArchitectures]
	for _, arch := range strings.Split(dynamic, ",") {
		if arch == targetArch {

			typeName := cm.Data["dynamic."+arch+".type"]
			allocfunc := r.cloudProviders[typeName]
			if allocfunc == nil {
				return nil, errors2.New("unknown dynamic provisioning type " + typeName)
			}
			maxInstances, err := strconv.Atoi(cm.Data["dynamic."+arch+".max-instances"])
			if err != nil {
				return nil, err
			}
			return DynamicResolver{
				CloudProvider: allocfunc(arch, cm.Data),
				SshSecret:     cm.Data["dynamic."+arch+".ssh-secret"],
				Arch:          arch,
				MaxInstances:  maxInstances,
			}, nil

		}
	}

	ret := HostPool{hosts: map[string]*Host{}, targetArch: targetArch}
	for k, v := range cm.Data {
		pos := strings.LastIndex(k, ".")
		if pos == -1 {
			continue
		}
		name := k[0:pos]
		key := k[pos+1:]
		host := ret.hosts[name]
		if host == nil {
			host = &Host{}
			ret.hosts[name] = host
			host.Name = name
		}
		switch key {
		case "address":
			host.Address = v
		case "user":
			host.User = v
		case "arch":
			host.Arch = v
		case "secret":
			host.Secret = v
		case "concurrency":
			atoi, err := strconv.Atoi(v)
			if err != nil {
				return nil, err
			}
			host.Concurrency = atoi
		default:
			log.Info("unknown key", "key", key)
		}

	}
	return ret, nil
}

func (r *ReconcileTaskRun) removeFinalizer(ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun) (reconcile.Result, error) {
	if controllerutil.ContainsFinalizer(tr, PipelineFinalizer) {
		controllerutil.RemoveFinalizer(tr, PipelineFinalizer)
		log.Info("removing finalizer")
		return reconcile.Result{}, r.client.Update(ctx, tr)
	}
	return reconcile.Result{}, nil
}

type HostPool struct {
	hosts      map[string]*Host
	targetArch string
}

type ArchitectureConfig interface {
	Allocate(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string) (reconcile.Result, error)
	Deallocate(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string, selectedHost string) error
}

func (hp HostPool) Allocate(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string) (reconcile.Result, error) {
	if len(hp.hosts) == 0 {
		//no hosts configured
		return reconcile.Result{}, r.createErrorSecret(ctx, tr, secretName, "no hosts configured")
	}
	failed := strings.Split(tr.Annotations[FailedHosts], ",")

	//get all existing runs that are assigned to a host
	taskList := v1beta1.TaskRunList{}
	err := r.client.List(ctx, &taskList, client.HasLabels{AssignedHost})
	if err != nil {
		return reconcile.Result{}, err
	}
	hostCount := map[string]int{}
	for _, tr := range taskList.Items {
		host := tr.Labels[AssignedHost]
		hostCount[host] = hostCount[host] + 1
	}
	for k, v := range hostCount {
		log.Info("host count", "host", k, "count", v)
	}

	//now select the host with the most free spots
	//this algorithm is not very complex

	var selected *Host
	freeSpots := 0
	hostWithOurArch := false
	for k, v := range hp.hosts {
		if slices.Contains(failed, k) {
			log.Info("ignoring already failed host", "host", k, "targetArch", hp.targetArch, "hostArch", v.Arch)
			continue
		}
		if v.Arch != hp.targetArch {
			log.Info("ignoring host", "host", k, "targetArch", hp.targetArch, "hostArch", v.Arch)
			continue
		}
		hostWithOurArch = true
		free := v.Concurrency - hostCount[k]

		log.Info("considering host", "host", k, "freeSlots", free)
		if free > freeSpots {
			selected = v
			freeSpots = free
		}
	}
	if !hostWithOurArch {
		log.Info("no hosts with requested arch", "arch", hp.targetArch)
		return reconcile.Result{}, r.createErrorSecret(ctx, tr, secretName, "no hosts configured for arch "+hp.targetArch)
	}
	if selected == nil {
		if tr.Labels[WaitingForArchLabel] == hp.targetArch {
			//we are already in a waiting state
			return reconcile.Result{}, nil
		}
		log.Info("no host found, waiting for one to become available")
		//no host available
		//add the waiting label
		//TODO: is the requeue actually a good idea?
		//TODO: timeout
		tr.Labels[WaitingForArchLabel] = hp.targetArch
		return reconcile.Result{RequeueAfter: time.Minute}, r.client.Update(ctx, tr)
	}

	log.Info("allocated host", "host", selected.Name)
	tr.Labels[AssignedHost] = selected.Name
	delete(tr.Labels, WaitingForArchLabel)
	//add a finalizer to clean up the secret
	controllerutil.AddFinalizer(tr, PipelineFinalizer)
	err = r.client.Update(ctx, tr)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = launchProvisioningTask(r, ctx, log, tr, secretName, selected.Secret, selected.Address, selected.User)

	if err != nil {
		//ugh, try and unassign
		delete(tr.Labels, AssignedHost)
		updateErr := r.client.Update(ctx, tr)
		if updateErr != nil {
			log.Error(updateErr, "Could not unassign task after provisioning failure")
			_ = r.createErrorSecret(ctx, tr, secretName, "Could not unassign task after provisioning failure")
		} else {
			log.Error(err, "Failed to provision host from pool")
			_ = r.createErrorSecret(ctx, tr, secretName, "Failed to provision host from pool "+err.Error())
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func launchProvisioningTask(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string, sshSecret string, address string, user string) error {
	//kick off the provisioning task
	//note that we can't use owner refs here because this task runs in a different namespace
	provision := v1beta1.TaskRun{}
	provision.GenerateName = "provision-task"
	provision.Namespace = r.operatorNamespace
	provision.Labels = map[string]string{TaskTypeLabel: TaskTypeProvision, UserTaskNamespace: tr.Namespace, UserTaskName: tr.Name, MultiArchLabel: "true", AssignedHost: tr.Labels[AssignedHost]}
	provision.Spec.TaskRef = &v1beta1.TaskRef{Name: "provision-shared-host"}
	provision.Spec.Workspaces = []v1beta1.WorkspaceBinding{{Name: "ssh", Secret: &v12.SecretVolumeSource{SecretName: sshSecret}}}
	provision.Spec.ServiceAccountName = ServiceAccountName //TODO: special service account for this
	provision.Spec.Params = []v1beta1.Param{
		{
			Name:  "SECRET_NAME",
			Value: *v1beta1.NewStructuredValues(secretName),
		},
		{
			Name:  "TASKRUN_NAME",
			Value: *v1beta1.NewStructuredValues(tr.Name),
		},
		{
			Name:  "NAMESPACE",
			Value: *v1beta1.NewStructuredValues(tr.Namespace),
		},
		{
			Name:  "HOST",
			Value: *v1beta1.NewStructuredValues(address),
		},
		{
			Name:  "USER",
			Value: *v1beta1.NewStructuredValues(user),
		},
	}

	err := r.client.Create(ctx, &provision)
	return err
}

func (hp HostPool) Deallocate(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string, selectedHost string) error {
	selected := hp.hosts[selectedHost]
	if selected != nil {
		log.Info("starting cleanup task")
		//kick off the clean task
		//kick off the provisioning task
		provision := v1beta1.TaskRun{}
		provision.GenerateName = "cleanup-task"
		provision.Namespace = r.operatorNamespace
		provision.Labels = map[string]string{TaskTypeLabel: TaskTypeClean, UserTaskName: tr.Name, UserTaskNamespace: tr.Namespace, MultiArchLabel: "true"}
		provision.Spec.TaskRef = &v1beta1.TaskRef{Name: "clean-shared-host"}
		provision.Spec.Workspaces = []v1beta1.WorkspaceBinding{{Name: "ssh", Secret: &v12.SecretVolumeSource{SecretName: selected.Secret}}}
		provision.Spec.ServiceAccountName = ServiceAccountName //TODO: special service account for this
		provision.Spec.Params = []v1beta1.Param{
			{
				Name:  "SECRET_NAME",
				Value: *v1beta1.NewStructuredValues(secretName),
			},
			{
				Name:  "TASKRUN_NAME",
				Value: *v1beta1.NewStructuredValues(tr.Name),
			},
			{
				Name:  "NAMESPACE",
				Value: *v1beta1.NewStructuredValues(tr.Namespace),
			},
			{
				Name:  "HOST",
				Value: *v1beta1.NewStructuredValues(selected.Address),
			},
			{
				Name:  "USER",
				Value: *v1beta1.NewStructuredValues(selected.User),
			},
		}
		err := r.client.Create(ctx, &provision)
		return err
	}
	return nil

}

type Host struct {
	Address     string
	Name        string
	User        string
	Concurrency int
	Arch        string
	Secret      string
}
type DynamicResolver struct {
	cloud.CloudProvider
	SshSecret    string
	Arch         string
	MaxInstances int
}

func (a DynamicResolver) Allocate(r *ReconcileTaskRun, ctx context.Context, log *logr.Logger, tr *v1beta1.TaskRun, secretName string) (reconcile.Result, error) {

	if tr.Annotations == nil {
		tr.Annotations = map[string]string{}
	}
	//this is called multiple times
	//the first time starts the instance
	//then it can be called repeatedly until the instance has an address
	//this lets us avoid blocking the main thread
	if tr.Annotations[CloudInstanceId] != "" {
		//we already have an instance, get its address
		address, err := a.CloudProvider.GetInstanceAddress(r.client, log, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
		if address != "" {
			tr.Labels[AssignedHost] = tr.Annotations[CloudInstanceId]
			tr.Annotations[CloudAddress] = address

			err = launchProvisioningTask(r, ctx, log, tr, secretName, a.SshSecret, address, "ec2-user")
			if err != nil {
				//ugh, try and unassign
				err := a.CloudProvider.TerminateInstance(r.client, log, ctx, cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId]))
				if err != nil {
					log.Error(err, "Failed to terminate EC2 instance")
				}

				delete(tr.Labels, AssignedHost)
				delete(tr.Annotations, CloudInstanceId)
				delete(tr.Annotations, CloudDynamicArch)
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
			return reconcile.Result{RequeueAfter: time.Second * 10}, err
		}
	}
	//first check this would not exceed the max tasks
	taskList := v1beta1.TaskRunList{}
	err := r.client.List(ctx, &taskList, client.MatchingLabels{CloudDynamicArch: a.Arch})
	if err != nil {
		return reconcile.Result{}, err
	}
	if len(taskList.Items) >= a.MaxInstances {
		if tr.Labels[WaitingForArchLabel] == a.Arch {
			//we are already in a waiting state
			return reconcile.Result{}, nil
		}
		log.Info("Too many running AWS tasks, waiting for existing tasks to finish")
		//no host available
		//add the waiting label
		tr.Labels[WaitingForArchLabel] = a.Arch
		return reconcile.Result{}, r.client.Update(ctx, tr)
	}

	instance, err := a.CloudProvider.LaunchInstance(r.client, log, ctx, "multi-arch-builder-"+tr.Name)

	if err != nil {
		//launch failed
		log.Error(err, "Failed to create AWS host")
		_ = r.createErrorSecret(ctx, tr, secretName, "Failed to create AWS host "+err.Error())
		return reconcile.Result{}, nil
	}
	tr.Annotations[CloudInstanceId] = string(instance)
	tr.Labels[CloudDynamicArch] = a.Arch

	log.Info("created AWS host", "instance", instance)
	//add a finalizer to clean up
	controllerutil.AddFinalizer(tr, PipelineFinalizer)
	err = r.client.Update(ctx, tr)
	if err != nil {
		err2 := a.CloudProvider.TerminateInstance(r.client, log, ctx, instance)
		if err2 != nil {
			log.Error(err, "failed to delete AWS instance")
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
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
	delete(tr.Labels, CloudDynamicArch)
	return nil
}
