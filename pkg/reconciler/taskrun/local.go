package taskrun

import (
	"context"

	"github.com/go-logr/logr"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Local struct{}

func (l Local) Allocate(r *ReconcileTaskRun, ctx context.Context, tr *pipelinev1.TaskRun, secretName string) (reconcile.Result, error) {
	var err error
	log := logr.FromContextOrDiscard(ctx)

	log.Info("Task set to run locally in the cluster", "task", tr.Name)
	tr.Labels[AssignedHost] = "localhost"
	controllerutil.AddFinalizer(tr, PipelineFinalizer)
	if err = r.client.Update(ctx, tr); err != nil {
		return reconcile.Result{}, err
	}

	err = createUserTaskSecret(r, ctx, tr, secretName, map[string][]byte{
		"host": []byte("localhost"),
	})
	return reconcile.Result{}, err
}

func (l Local) Deallocate(r *ReconcileTaskRun, ctx context.Context, tr *pipelinev1.TaskRun, secretName string, selectedHost string) error {
	return nil
}

func createUserTaskSecret(r *ReconcileTaskRun, ctx context.Context, tr *pipelinev1.TaskRun, secretName string, secretData map[string][]byte) error {
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: tr.Namespace,
			Labels:    map[string]string{MultiPlatformSecretLabel: "true"},
		},
		Data: secretData,
	}
	if err := controllerutil.SetOwnerReference(tr, &secret, r.scheme); err != nil {
		return err
	}
	if err := r.client.Create(ctx, &secret); err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}
