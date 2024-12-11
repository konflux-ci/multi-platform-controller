package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/konflux-ci/multi-platform-controller/pkg/reconciler/taskrun"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"

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

const TaskRunLabel = "tekton.dev/taskRun"

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

	if err := pipelinev1.AddToScheme(options.Scheme); err != nil {
		return nil, err
	}
	var mgr ctrl.Manager
	var err error

	configMapSelector := labels.NewSelector()
	configMapLabels, lerr := labels.NewRequirement(taskrun.ConfigMapLabel, selection.Exists, []string{})
	if lerr != nil {
		return nil, lerr
	}
	configMapSelector = configMapSelector.Add(*configMapLabels)

	secretSelector := labels.NewSelector()
	secretLabels, lerr := labels.NewRequirement(taskrun.MultiPlatformSecretLabel, selection.Exists, []string{})
	if lerr != nil {
		return nil, lerr
	}
	secretSelector = secretSelector.Add(*secretLabels)

	podSelector := labels.NewSelector()
	podLabels, lerr := labels.NewRequirement(TaskRunLabel, selection.Exists, []string{})
	if lerr != nil {
		return nil, lerr
	}
	podSelector = podSelector.Add(*podLabels)
	options.Cache = cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&pipelinev1.TaskRun{}: {},
			&v1.Secret{}:          {Label: secretSelector},
			&v1.ConfigMap{}:       {Label: configMapSelector},
			&v1.Pod{}:             {Label: podSelector},
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

	ticker := time.NewTicker(time.Hour * 24)
	go func() {
		for range ticker.C {
			taskrun.UpdateHostPools(operatorNamespace, mgr.GetClient(), &controllerLog)
		}
	}()
	timer := time.NewTimer(time.Minute)
	go func() {
		<-timer.C
		//update the nodes on startup
		taskrun.UpdateHostPools(operatorNamespace, mgr.GetClient(), &controllerLog)
	}()

	return mgr, nil
}
