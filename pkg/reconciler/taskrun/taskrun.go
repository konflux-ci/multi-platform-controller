package taskrun

import (
	"context"
	"github.com/google/uuid"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
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
	contextTimeout          = 300 * time.Second
	TaskTypeLabel           = "build.appstudio.redhat.com/task-type"
	TargetArchitectureLabel = "build.appstudio.redhat.com/target-architecture"
	MultiArchLabel          = "build.appstudio.redhat.com/multi-arch-required"
	AssignedHost            = "build.appstudio.redhat.com/assigned-host"
	ProvisionTaskName       = "build.appstudio.redhat.com/provision-task-name"
	ProvisionTaskNamespace  = "build.appstudio.redhat.com/provision-task-namespace"
	WaitingForArchLabel     = "build.appstudio.redhat.com/waiting-for-arch"
	PipelineFinalizer       = "appstudio.io/multi-arch-finalizer"
	HostConfig              = "host-config"
	OurNamespace            = "host-config"

	TaskTypeProvision = "provision"
	TaskTypeClean     = "clean"
)

type ReconcileTaskRun struct {
	client            client.Client
	scheme            *runtime.Scheme
	eventRecorder     record.EventRecorder
	operatorNamespace string
}

func newReconciler(mgr ctrl.Manager, operatorNamespace string) reconcile.Reconciler {
	return &ReconcileTaskRun{
		client:            mgr.GetClient(),
		scheme:            mgr.GetScheme(),
		eventRecorder:     mgr.GetEventRecorderFor("ComponentBuild"),
		operatorNamespace: operatorNamespace,
	}
}

func (r *ReconcileTaskRun) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	// Set the ctx to be Background, as the top-level context for incoming requests.
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	log := ctrl.Log.WithName("taskrun").WithValues("request", request.NamespacedName)

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
		return r.handleTaskRunReceived(ctx, log, &pr)
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileTaskRun) handleTaskRunReceived(ctx context.Context, log logr.Logger, tr *v1beta1.TaskRun) (reconcile.Result, error) {
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

	if tr.Labels == nil || tr.Labels[TargetArchitectureLabel] == "" || tr.Labels[MultiArchLabel] == "" {
		//this is not something we need to be concerned with
		return reconcile.Result{}, nil
	}
	return r.handleUserTask(ctx, log, tr)
}

// called when a task has finished, we look for waiting tasks
// and then potentially requeue one of them
func (r *ReconcileTaskRun) handleWaitingTasks(arch string) (reconcile.Result, error) {
	return reconcile.Result{}, nil

}

func (r *ReconcileTaskRun) handleCleanTask(ctx context.Context, log logr.Logger, tr *v1beta1.TaskRun) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (r *ReconcileTaskRun) handleProvisionTask(ctx context.Context, log logr.Logger, tr *v1beta1.TaskRun) (reconcile.Result, error) {
	return reconcile.Result{}, nil

}

func (r *ReconcileTaskRun) handleUserTask(ctx context.Context, log logr.Logger, tr *v1beta1.TaskRun) (reconcile.Result, error) {

	//now check the state
	secret := v12.Secret{}
	secretName := "multi-arch-ssl-" + tr.Name
	err := r.client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: secretName}, &secret)
	if err == nil {
		return r.handleExistingSecret(ctx, log, tr, &secret)
	} else if errors.IsNotFound(err) {
		//if the PR is done we ignore it
		if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
			return reconcile.Result{}, nil
		}

		return r.handleHostAllocation(ctx, tr, secret, secretName)
	} else {
		return reconcile.Result{}, err
	}
}

func (r *ReconcileTaskRun) handleHostAllocation(ctx context.Context, tr *v1beta1.TaskRun, secret v12.Secret, secretName string) (reconcile.Result, error) {
	targetArch := tr.Labels[TargetArchitectureLabel]

	//lets allocate a host, get the map with host info
	hosts, err := r.hostConfig(ctx)
	if err != nil {
		return reconcile.Result{}, err
	}
	//get all existing runs that are assigned to a host
	taskList := v1beta1.TaskRunList{}
	err = r.client.List(ctx, &taskList, client.HasLabels{AssignedHost}, client.MatchingLabels{TargetArchitectureLabel: targetArch})
	if err != nil {
		return reconcile.Result{}, err
	}
	hostCount := map[string]int{}
	for _, tr := range taskList.Items {
		host := tr.Labels[AssignedHost]
		hostCount[host] = hostCount[host] + 1
	}

	//now select the host with the most free spots
	//this algorithm is not very complex

	var selected *Host
	freeSpots := 0
	for k, v := range hosts {
		if v.Arch != targetArch {
			continue
		}
		free := v.Concurrency - hostCount[k]
		if free > freeSpots {
			selected = v
			freeSpots = free
		}
	}
	if selected == nil {
		//no host available
		//add the waiting label
		//TODO: is the requeue actually a good idea?
		tr.Labels[WaitingForArchLabel] = targetArch
		return reconcile.Result{RequeueAfter: time.Minute}, r.client.Update(ctx, tr)
	}
	tr.Labels[AssignedHost] = selected.Name

	//create the host secret
	arch := targetArch
	secret.Labels = map[string]string{TargetArchitectureLabel: arch}
	secret.Namespace = tr.Namespace
	secret.Name = secretName

	hardCoded := v12.Secret{}
	err = r.client.Get(ctx, types.NamespacedName{Namespace: r.operatorNamespace, Name: selected.Secret}, &hardCoded)
	if err != nil {
		return reconcile.Result{}, err
	}
	secret.Data["id_rsa"] = secret.Data["id_rsa"]
	secret.Data["host"] = []byte(selected.User + "@" + selected.Address)
	secret.Data["build-dir"] = []byte("/tmp/" + uuid.New().String())
	err = r.client.Create(ctx, &secret)
	if err != nil {
		return reconcile.Result{}, err
	}
	//add a finalizer to clean up the secret
	controllerutil.AddFinalizer(tr, PipelineFinalizer)
	return reconcile.Result{}, r.client.Update(ctx, tr)
}

func (r *ReconcileTaskRun) handleExistingSecret(ctx context.Context, log logr.Logger, tr *v1beta1.TaskRun, secret *v12.Secret) (reconcile.Result, error) {
	//already exists
	if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
		//PR is done, clean up the secret
		err := r.client.Delete(ctx, secret)
		if err != nil {
			log.Error(err, "Unable to delete secret")
		}
		controllerutil.RemoveFinalizer(tr, PipelineFinalizer)
		delete(tr.Labels, AssignedHost)
		err = r.client.Update(ctx, tr)
		if err != nil {
			return reconcile.Result{}, err
		}
		return r.handleWaitingTasks(tr.Labels[TargetArchitectureLabel])
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileTaskRun) hostConfig(ctx context.Context) (map[string]*Host, error) {
	cm := v12.ConfigMap{}
	err := r.client.Get(ctx, types.NamespacedName{Namespace: r.operatorNamespace, Name: HostConfig}, &cm)
	if err != nil {
		return nil, err
	}
	ret := map[string]*Host{}
	for k, v := range cm.Data {
		pos := strings.LastIndex(k, ".")
		name := k[0:pos]
		key := k[pos+1:]
		host := ret[key]
		if host == nil {
			host = &Host{}
			ret[name] = host
			host.Name = name
		}
		switch name {
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
		}

	}
	return ret, nil
}

type Host struct {
	Address     string
	Name        string
	User        string
	Concurrency int
	Arch        string
	Secret      string
}
