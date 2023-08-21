package pipelinerun

import (
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func SetupNewReconcilerWithManager(mgr ctrl.Manager, operatorNamespace string) error {
	r := newReconciler(mgr, operatorNamespace)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.PipelineRun{}).Complete(r)
}
