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
	TargetArchitectureLabel = "build.appstudio.redhat.com/target-architecture"
	MultiArchLabel          = "build.appstudio.redhat.com/multi-arch-required"
	PipelineFinalizer       = "appstudio.io/build-host-pool-finalizer"
	HardCodedHost           = "hard-coded-host"
)

type ReconcileTaskRun struct {
	client        client.Client
	scheme        *runtime.Scheme
	eventRecorder record.EventRecorder
}

func newReconciler(mgr ctrl.Manager) reconcile.Reconciler {
	return &ReconcileTaskRun{
		client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		eventRecorder: mgr.GetEventRecorderFor("ComponentBuild"),
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
	if tr.Labels == nil || tr.Labels[TargetArchitectureLabel] == "" || tr.Labels[MultiArchLabel] == "" {
		return reconcile.Result{}, nil
	}
	secret := v12.Secret{}
	secretName := "multi-arch-ssl-" + tr.Name
	err := r.client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: secretName}, &secret)
	if err == nil {
		//already exists
		if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
			//PR is done, clean up the secret
			err := r.client.Delete(ctx, &secret)
			if err != nil {
				log.Error(err, "Unable to delete secret")
			}
			controllerutil.RemoveFinalizer(tr, PipelineFinalizer)
			return reconcile.Result{}, r.client.Update(ctx, tr)
		}
	} else if errors.IsNotFound(err) {
		//if the PR is done we ignore it
		if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
			return reconcile.Result{}, nil
		}
		//just copy our hard coded secret
		arch := tr.Labels[TargetArchitectureLabel]
		secret.Labels = map[string]string{TargetArchitectureLabel: arch}
		secret.Namespace = tr.Namespace
		secret.Name = secretName

		hardCoded := v12.Secret{}
		err = r.client.Get(ctx, types.NamespacedName{Namespace: "multi-arch-operator", Name: HardCodedHost}, &hardCoded)
		if err != nil {
			return reconcile.Result{}, err
		}
		secret.Data = hardCoded.Data
		err := r.client.Create(ctx, &secret)
		if err != nil {
			return reconcile.Result{}, err
		}
		//add a finalizer to clean up the secret
		controllerutil.AddFinalizer(tr, PipelineFinalizer)
		return reconcile.Result{}, r.client.Update(ctx, tr)
	} else {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}
