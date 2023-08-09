package taskrun

import (
	"context"
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
		return r.handleExistingSecret(ctx, log, tr, secret)
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
	//lets allocate a host

	cm := v12.ConfigMap{}
	err := r.client.Get(ctx, types.NamespacedName{Namespace: r.operatorNamespace, Name: HostConfig}, &cm)
	if err != nil {
		return reconcile.Result{}, err
	}

	//just copy our hard coded secret
	arch := tr.Labels[TargetArchitectureLabel]
	secret.Labels = map[string]string{TargetArchitectureLabel: arch}
	secret.Namespace = tr.Namespace
	secret.Name = secretName

	hardCoded := v12.Secret{}
	err = r.client.Get(ctx, types.NamespacedName{Namespace: "multi-arch-operator", Name: "tmp"}, &hardCoded)
	if err != nil {
		return reconcile.Result{}, err
	}
	secret.Data = hardCoded.Data
	err = r.client.Create(ctx, &secret)
	if err != nil {
		return reconcile.Result{}, err
	}
	//add a finalizer to clean up the secret
	controllerutil.AddFinalizer(tr, PipelineFinalizer)
	return reconcile.Result{}, r.client.Update(ctx, tr)
}

func (r *ReconcileTaskRun) handleExistingSecret(ctx context.Context, log logr.Logger, tr *v1beta1.TaskRun, secret v12.Secret) (reconcile.Result, error) {
	//already exists
	if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
		//PR is done, clean up the secret
		err := r.client.Delete(ctx, &secret)
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
