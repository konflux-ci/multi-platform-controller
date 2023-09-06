package controller

import (
	"context"
	"fmt"
	"github.com/stuartwdouglas/multi-arch-host-resolver/pkg/reconciler/taskrun"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"

	jvmbs "github.com/redhat-appstudio/jvm-build-service/pkg/apis/jvmbuildservice/v1alpha1"
	pipelinev1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

var (
	controllerLog = ctrl.Log.WithName("controller")
)

func NewManager(cfg *rest.Config, options ctrl.Options) (ctrl.Manager, error) {
	// do not check tekton in kcp
	// we have seen in e2e testing that this path can get invoked prior to the TaskRun CRD getting generated,
	// and controller-runtime does not retry on missing CRDs.
	// so we are going to wait on the CRDs existing before moving forward.
	apiextensionsClient := apiextensionsclient.NewForConfigOrDie(cfg)
	if err := wait.PollUntilContextTimeout(context.Background(), time.Second*5, time.Minute*5, true, func(ctx context.Context) (done bool, err error) {
		_, err = apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), "taskruns.tekton.dev", metav1.GetOptions{})
		if err != nil {
			controllerLog.Info(fmt.Sprintf("get of taskrun CRD failed with: %s", err.Error()))
			return false, nil
		}
		controllerLog.Info("get of taskrun CRD returned successfully")
		return true, nil
	}); err != nil {
		controllerLog.Error(err, "timed out waiting for taskrun CRD to be created")
		return nil, err
	}
	options.Scheme = runtime.NewScheme()

	// pretty sure this is there by default but we will be explicit like build-service
	if err := k8sscheme.AddToScheme(options.Scheme); err != nil {
		return nil, err
	}

	if err := jvmbs.AddToScheme(options.Scheme); err != nil {
		return nil, err
	}

	if err := pipelinev1beta1.AddToScheme(options.Scheme); err != nil {
		return nil, err
	}
	var mgr ctrl.Manager
	var err error

	//if we are running this locally on the same cluster as the ckcp we want to ignore any synced pipeline runs
	multiArchPipelines := labels.NewSelector()
	requirement, lerr := labels.NewRequirement(taskrun.MultiArchLabel, selection.Exists, nil)
	if lerr != nil {
		return nil, lerr
	}
	multiArchPipelines = multiArchPipelines.Add(*requirement)

	configMapSelector := labels.NewSelector()
	configMapLabels, lerr := labels.NewRequirement(taskrun.ConfigMapLabel, selection.Exists, []string{})
	if lerr != nil {
		return nil, lerr
	}
	configMapSelector = configMapSelector.Add(*configMapLabels)

	secretSelector := labels.NewSelector()
	secretLabels, lerr := labels.NewRequirement(taskrun.MultiArchSecretLabel, selection.Exists, []string{})
	if lerr != nil {
		return nil, lerr
	}
	secretSelector = secretSelector.Add(*secretLabels)

	options.Cache = cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&pipelinev1beta1.TaskRun{}: {Label: multiArchPipelines},
			&v1.Secret{}:               {Label: secretSelector},
			&v1.ConfigMap{}:            {Label: configMapSelector},
		},
	}
	operatorNamespace := os.Getenv("POD_NAMESPACE")
	mgr, err = ctrl.NewManager(cfg, options)

	if err != nil {
		return nil, err
	}
	controllerLog.Info("deployed in namespace", "namespace", operatorNamespace)
	if err := taskrun.SetupNewReconcilerWithManager(mgr, operatorNamespace); err != nil {
		return nil, err
	}

	return mgr, nil
}
