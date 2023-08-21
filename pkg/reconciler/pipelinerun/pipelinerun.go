package pipelinerun

import (
	"context"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"knative.dev/pkg/apis"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
	"time"
)

const (
	//TODO eventually we'll need to decide if we want to make this tuneable
	contextTimeout = 300 * time.Second

	Produces = "build.appstudio.redhat.com/produces"
	Consumes = "build.appstudio.redhat.com/consumes"
	Sha      = "pipelinesascode.tekton.dev/sha"
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
	log := ctrl.Log.WithName("pipelinerun").WithValues("request", request.NamespacedName)

	pr := v1beta1.PipelineRun{}
	prerr := r.client.Get(ctx, request.NamespacedName, &pr)
	if prerr != nil {
		if !errors.IsNotFound(prerr) {
			log.Error(prerr, "Reconcile key %s as PipelineRun unexpected error", request.NamespacedName.String())
			return ctrl.Result{}, prerr
		}
	}
	if prerr != nil {
		msg := "Reconcile key received not found errors for PipelineRun (probably deleted): " + request.NamespacedName.String()
		log.Info(msg)
		return ctrl.Result{}, nil
	}

	if pr.Labels == nil {
		return reconcile.Result{}, nil
	}

	produces := pr.Labels[Produces]
	consumes := pr.Labels[Consumes]
	sha := pr.Labels[Sha]
	if sha == "" {
		return reconcile.Result{}, nil
	}
	if produces == "" && consumes == "" {
		return reconcile.Result{}, nil
	}

	return reconcile.Result{}, r.processPr(ctx, sha)

}

func (r *ReconcileTaskRun) processPr(ctx context.Context, sha string) error {
	//we might be able to run a downstream task
	//we need all PRs for this commit
	list := v1beta1.PipelineRunList{}
	err := r.client.List(ctx, &list, client.MatchingLabels{Sha: sha})
	if err != nil {
		return err
	}
	produced := map[string]bool{}
	for _, run := range list.Items {
		thisSuccess := run.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
		if thisSuccess {
			p := run.Labels[Produces]
			for _, i := range strings.Split(p, " ") {
				produced[i] = true
			}
		}
	}
	//now we have a map of everything successfully produced
	for _, run := range list.Items {
		if run.Spec.Status == v1beta1.PipelineRunSpecStatusPending {
			consumes := run.Labels[Consumes]
			if consumes != "" {
				okToRun := true
				for _, i := range strings.Split(consumes, " ") {
					if !produced[i] {
						okToRun = false
					}
				}
				if okToRun {
					copy := run
					copy.Spec.Status = ""
					err := r.client.Update(ctx, &copy)
					if err != nil {
						return err
					}
				}

			}

		}

	}
	return nil
}
