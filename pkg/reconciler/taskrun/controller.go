package taskrun

import (
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func SetupNewReconcilerWithManager(mgr ctrl.Manager) error {
	r := newReconciler(mgr)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.TaskRun{}).Complete(r)
}
