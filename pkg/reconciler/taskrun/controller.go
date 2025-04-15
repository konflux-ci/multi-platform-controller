package taskrun

import (
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func SetupNewReconcilerWithManager(mgr ctrl.Manager, operatorNamespace string, options controller.Options) error {
	r := newReconciler(mgr, operatorNamespace)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.TaskRun{}).
		WithOptions(options).
		Complete(r)
}
