package taskrun

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	mpcmetrics "github.com/konflux-ci/multi-platform-controller/pkg/metrics"

	"github.com/konflux-ci/multi-platform-controller/pkg/aws"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	"github.com/konflux-ci/multi-platform-controller/pkg/ibm"
	errors2 "github.com/pkg/errors"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	kubecore "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/strings/slices"
	"knative.dev/pkg/apis"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
)

const (
	//TODO eventually we'll need to decide if we want to make this tuneable
	contextTimeout = 300 * time.Second

	SecretPrefix   = "multi-platform-ssh-"
	ConfigMapLabel = "build.appstudio.redhat.com/multi-platform-config"

	MultiPlatformSecretLabel = "build.appstudio.redhat.com/multi-platform-secret"

	AssignedHost           = "build.appstudio.redhat.com/assigned-host"
	FailedHosts            = "build.appstudio.redhat.com/failed-hosts"
	CloudInstanceId        = "build.appstudio.redhat.com/cloud-instance-id"
	CloudFailures          = "build.appstudio.redhat.com/cloud-failure-count"
	CloudAddress           = "build.appstudio.redhat.com/cloud-address"
	CloudDynamicPlatform   = "build.appstudio.redhat.com/cloud-dynamic-platform"
	ProvisionTaskProcessed = "build.appstudio.redhat.com/provision-task-processed"
	ProvisionTaskFinalizer = "build.appstudio.redhat.com/provision-task-finalizer"

	//AllocationStartTimeAnnotation Some allocations can take multiple calls, we track the actual start time in this annotation
	AllocationStartTimeAnnotation = "build.appstudio.redhat.com/allocation-start-time"
	//BuildStartTimeAnnotation The time the build actually starts
	BuildStartTimeAnnotation = "build.appstudio.redhat.com/build-start-time"

	UserTaskName      = "build.appstudio.redhat.com/user-task-name"
	UserTaskNamespace = "build.appstudio.redhat.com/user-task-namespace"

	WaitingForPlatformLabel = "build.appstudio.redhat.com/waiting-for-platform"
	PipelineFinalizer       = "appstudio.io/multi-platform-finalizer"
	HostConfig              = "host-config"

	TaskTypeLabel                = "build.appstudio.redhat.com/task-type"
	TaskTargetPlatformAnnotation = "build.appstudio.redhat.com/task-platform"
	TaskTypeProvision            = "provision"
	TaskTypeUpdate               = "update"
	TaskTypeClean                = "clean"

	ServiceAccountName = "multi-platform-controller-controller-manager"

	PlatformParam          = "PLATFORM"
	LocalPlatforms         = "local-platforms"
	DynamicPlatforms       = "dynamic-platforms"
	DynamicPoolPlatforms   = "dynamic-pool-platforms"
	DefaultInstanceTag     = "instance-tag"
	AdditionalInstanceTags = "additional-instance-tags"
	AllowedNamespaces      = "allowed-namespaces"
	ParamNamespace         = "NAMESPACE"
	ParamTaskrunName       = "TASKRUN_NAME"
	ParamSecretName        = "SECRET_NAME"
	ParamHost              = "HOST"
	ParamUser              = "USER"
	ParamSudoCommands      = "SUDO_COMMANDS"
)

type ReconcileTaskRun struct {
	apiReader                client.Reader
	client                   client.Client
	scheme                   *k8sRuntime.Scheme
	eventRecorder            record.EventRecorder
	operatorNamespace        string
	configMapResourceVersion string
	platformConfig           map[string]PlatformConfig
	cloudProviders           map[string]func(platform string, config map[string]string, systemNamespace string) cloud.CloudProvider
}

//+kubebuilder:rbac:groups="tekton.dev",resources=taskruns,verbs=create;delete;deletecollection;get;list;patch;update;watch
//+kubebuilder:rbac:groups="tekton.dev",resources=taskruns/status,verbs=create;delete;deletecollection;get;list;patch;update;watch
//+kubebuilder:rbac:groups="apiextensions.k8s.io",resources=customresourcedefinitions,verbs=get
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func newReconciler(mgr ctrl.Manager, operatorNamespace string) reconcile.Reconciler {
	return &ReconcileTaskRun{
		apiReader:         mgr.GetAPIReader(),
		client:            mgr.GetClient(),
		scheme:            mgr.GetScheme(),
		eventRecorder:     mgr.GetEventRecorderFor("TaskRun"),
		operatorNamespace: operatorNamespace,
		platformConfig:    map[string]PlatformConfig{},
		cloudProviders:    map[string]func(platform string, config map[string]string, systemNamespace string) cloud.CloudProvider{"aws": aws.Ec2Provider, "ibmz": ibm.IBMZProvider, "ibmp": ibm.IBMPowerProvider},
	}
}

func (r *ReconcileTaskRun) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	// Set the ctx to be Background, as the top-level context for incoming requests.
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	log := ctrl.Log.WithName("taskrun").WithValues("request", request.NamespacedName)

	pr := tektonapi.TaskRun{}
	prerr := r.client.Get(ctx, request.NamespacedName, &pr)
	if prerr != nil {
		if !errors.IsNotFound(prerr) {
			log.Error(prerr, "Reconcile key %s as TaskRun unexpected error", request.NamespacedName.String())
			return ctrl.Result{}, prerr
		} else {
			return ctrl.Result{}, nil
		}
	}
	if pr.Annotations != nil {
		if pr.Annotations[CloudInstanceId] != "" {
			log = log.WithValues(CloudInstanceId, pr.Annotations[CloudInstanceId])
		}
		if pr.Annotations[AssignedHost] != "" {
			log = log.WithValues(AssignedHost, pr.Annotations[AssignedHost])
		}
	}
	ctx = logr.NewContext(ctx, log)

	return r.handleTaskRunReceived(ctx, &pr)
}

func (r *ReconcileTaskRun) handleTaskRunReceived(ctx context.Context, tr *tektonapi.TaskRun) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx)
	if tr.Labels != nil {
		taskType := tr.Labels[TaskTypeLabel]
		if taskType == TaskTypeClean {
			log.Info("Reconciling cleanup task")
			return r.handleCleanTask(ctx, tr)
		}
		if taskType == TaskTypeProvision {
			log.Info("Reconciling provision task")
			return r.handleProvisionTask(ctx, tr)
		}
		if taskType == TaskTypeUpdate {
			// We don't care about these
			return reconcile.Result{}, nil
		}
	}
	if tr.Spec.Params == nil {
		return reconcile.Result{}, nil
	}

	//identify tasks by the PLATFORM param and multi-platform-ssh- secret
	if tr.Status.TaskSpec == nil || tr.Status.TaskSpec.Volumes == nil {
		return reconcile.Result{}, nil
	}
	found := false
	for _, i := range tr.Status.TaskSpec.Volumes {
		if i.Secret != nil {
			if strings.HasPrefix(i.Secret.SecretName, SecretPrefix) {
				found = true
			}
		}
	}
	if !found {
		//this is not something we need to be concerned with
		return reconcile.Result{}, nil
	}
	found = false
	for _, i := range tr.Spec.Params {
		if i.Name == PlatformParam {
			found = true
		}
	}
	if !found {
		//this is not something we need to be concerned with
		return reconcile.Result{}, nil
	}
	log.Info("Reconciling user task")

	if tr.Status.TaskSpec == nil || tr.Status.TaskSpec.Volumes == nil {
		return reconcile.Result{}, nil
	}
	return r.handleUserTask(ctx, tr)
}

// called when a task has finished, we look for waiting tasks
// and then potentially requeue one of them
func (r *ReconcileTaskRun) handleWaitingTasks(ctx context.Context, platform string) (reconcile.Result, error) {

	//try and requeue a waiting task if one exists
	taskList := tektonapi.TaskRunList{}

	err := r.client.List(ctx, &taskList, client.MatchingLabels{WaitingForPlatformLabel: platformLabel(platform)})
	if err != nil {
		return reconcile.Result{}, err
	}
	var oldest *tektonapi.TaskRun
	var oldestTs time.Time
	for i := range taskList.Items {
		tr := taskList.Items[i]
		if oldest == nil || oldestTs.After(tr.CreationTimestamp.Time) {
			oldestTs = tr.CreationTimestamp.Time
			oldest = &tr
		}
	}
	if oldest != nil {
		//remove the waiting label, which will trigger a requeue
		delete(oldest.Labels, WaitingForPlatformLabel)
		return reconcile.Result{}, r.client.Update(ctx, oldest)
	}
	return reconcile.Result{}, nil

}

func (r *ReconcileTaskRun) handleCleanTask(ctx context.Context, tr *tektonapi.TaskRun) (reconcile.Result, error) {
	if tr.Status.CompletionTime == nil {
		return reconcile.Result{}, nil
	}
	success := tr.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
	if !success {
		log := logr.FromContextOrDiscard(ctx)
		log.Info("cleanup task failed", "task", tr.Name)
		mpcmetrics.HandleMetrics(tr.Annotations[TaskTargetPlatformAnnotation], func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.ProvisionFailures.Inc()
		})
	}
	//leave the failed TR for an hour to view logs
	if success || tr.Status.CompletionTime.Add(time.Hour).Before(time.Now()) {
		return reconcile.Result{}, r.client.Delete(ctx, tr)
	}
	return reconcile.Result{RequeueAfter: time.Hour}, nil
}

func (r *ReconcileTaskRun) handleProvisionTask(ctx context.Context, tr *tektonapi.TaskRun) (reconcile.Result, error) {

	if tr.Status.CompletionTime == nil {
		return reconcile.Result{}, nil
	}
	if tr.Annotations == nil {
		tr.Annotations = map[string]string{}
	}
	success := tr.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
	if tr.Annotations[ProvisionTaskProcessed] == "true" {
		//leave the failed TR for an hour so we can view logs
		//TODO: tekton results integration
		if success || tr.Status.CompletionTime.Add(time.Hour).Before(time.Now()) {
			return reconcile.Result{}, r.client.Delete(ctx, tr)
		}
		return reconcile.Result{RequeueAfter: time.Hour}, nil
	}
	tr.Annotations[ProvisionTaskProcessed] = "true"
	secretName := ""
	for _, i := range tr.Spec.Params {
		if i.Name == "SECRET_NAME" {
			secretName = i.Value.StringVal
			break
		}
	}
	userNamespace := tr.Labels[UserTaskNamespace]
	userTaskName := tr.Labels[UserTaskName]
	assigned := tr.Labels[AssignedHost]
	targetPlatform := tr.Annotations[TaskTargetPlatformAnnotation]
	log := logr.FromContextOrDiscard(ctx)
	if !success {
		mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.ProvisionFailures.Inc()
		})
		mpcmetrics.CountAvailabilityError(targetPlatform)
		message := fmt.Sprintf("provision task for host %s for user task %s/%sfailed", assigned, userNamespace, userTaskName)
		r.eventRecorder.Event(tr, "Error", "ProvisioningFailed", message)
		err := errors2.New(message)
		log.Error(err, message)
		if assigned != "" {
			userTr := tektonapi.TaskRun{}
			err := r.client.Get(ctx, types.NamespacedName{Namespace: userNamespace, Name: userTaskName}, &userTr)
			if err == nil {
				if userTr.Annotations == nil {
					userTr.Annotations = map[string]string{}
				}
				//add to failed hosts and remove assigned
				//this will cause it to try again
				failed := strings.Split(userTr.Annotations[FailedHosts], ",")
				if failed[0] == "" {
					failed = []string{}
				}
				failed = append(failed, assigned)
				userTr.Annotations[FailedHosts] = strings.Join(failed, ",")
				delete(userTr.Labels, AssignedHost)
				err = r.client.Update(ctx, &userTr)
				if err != nil {
					return reconcile.Result{}, err
				}
				delete(tr.Labels, AssignedHost)
				err := r.client.Update(ctx, tr)
				if err != nil {
					return reconcile.Result{}, err
				}
			}
		}
	} else {
		message := fmt.Sprintf("provision task for host %s for user task %s/%s succeeded", assigned, userNamespace, userTaskName)
		log.Info(message)
		r.eventRecorder.Event(tr, "Normal", "Provisioned", message)
		mpcmetrics.CountAvailabilitySuccess(targetPlatform)
		//verify we ended up with a secret
		secret := kubecore.Secret{}
		err := r.client.Get(ctx, types.NamespacedName{Namespace: userNamespace, Name: secretName}, &secret)
		if err != nil {
			if errors.IsNotFound(err) {
				userTr := tektonapi.TaskRun{}
				err = r.client.Get(ctx, types.NamespacedName{Namespace: userNamespace, Name: userTaskName}, &userTr)
				if err != nil {
					if !errors.IsNotFound(err) {
						//if the task run is not found then this is just old
						return reconcile.Result{}, err
					}
				} else {
					err = r.createErrorSecret(ctx, &userTr, targetPlatform, secretName, "provision task failed to create a secret")
					if err != nil {
						return reconcile.Result{}, err
					}

				}
			} else {
				return reconcile.Result{}, err
			}
		}
		// Now we 'bump' the pod, by giving it a label
		// This forces a reconcile
		pods := kubecore.PodList{}

		err = r.client.List(ctx, &pods, client.InNamespace(userNamespace))
		if err != nil {
			log.Error(err, "unable to annotate task pod")
		} else {
			for i := range pods.Items {
				pod := pods.Items[i]
				//look for pods owned by the user taskrun
				owned := false
				for _, ref := range pod.OwnerReferences {
					if ref.Name == userTaskName {
						owned = true
						break
					}
				}
				if !owned {
					continue
				}

				if pod.Annotations == nil {
					pod.Annotations = map[string]string{}
				}
				pod.Annotations[AssignedHost] = assigned
				err = r.client.Update(ctx, &pod)
				if err != nil {
					log.Error(err, "unable to annotate task pod")
				}
			}
		}
	}
	return reconcile.Result{}, r.client.Update(ctx, tr)
}

// This creates an secret with the 'error' field set
// This will result in the pipeline run immediately failing with the message printed in the logs
func (r *ReconcileTaskRun) createErrorSecret(ctx context.Context, tr *tektonapi.TaskRun, targetPlatform, secretName, msg string) error {
	if controllerutil.AddFinalizer(tr, PipelineFinalizer) {
		err := r.client.Update(ctx, tr)
		if err != nil {
			return err
		}
	}
	log := logr.FromContextOrDiscard(ctx)
	log.Info("creating error secret " + msg)

	secret := kubecore.Secret{}
	secret.Labels = map[string]string{MultiPlatformSecretLabel: "true"}
	secret.Namespace = tr.Namespace
	secret.Name = secretName
	err := controllerutil.SetOwnerReference(tr, &secret, r.scheme)
	if err != nil {
		return err
	}
	_, file, line, _ := runtime.Caller(1)
	fullMsg := fmt.Sprintf(
		"%s\n"+
			"\n"+
			"Context info:\n"+
			"  Platform: %s\n"+
			"  File:     %s\n"+
			"  Line:     %d\n"+
			"\n",
		msg, targetPlatform, file, line,
	)

	secret.Data = map[string][]byte{
		"error": []byte(fullMsg),
	}
	err = r.client.Create(ctx, &secret)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			//already exists, ignore
			return nil
		}
		return err
	}
	return nil
}

func (r *ReconcileTaskRun) handleUserTask(ctx context.Context, tr *tektonapi.TaskRun) (reconcile.Result, error) {

	log := logr.FromContextOrDiscard(ctx)
	secretName := SecretPrefix + tr.Name
	if tr.Labels[AssignedHost] != "" {
		return r.handleHostAssigned(ctx, tr, secretName)
	} else {
		//if the PR is done we ignore it
		if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
			if controllerutil.ContainsFinalizer(tr, PipelineFinalizer) {
				return r.handleHostAssigned(ctx, tr, secretName)

			}
			return reconcile.Result{}, nil
		}

		targetPlatform, err := extractPlatform(tr)
		if err != nil {
			err := r.createErrorSecret(ctx, tr, "[UNKNOWN]", secretName, err.Error())
			if err != nil {
				log.Error(err, "could not create error secret")
			}
			return reconcile.Result{}, nil
		}
		res, err := r.handleHostAllocation(ctx, tr, secretName, targetPlatform)
		if err != nil && !errors.IsConflict(err) {
			mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
				metrics.HostAllocationFailures.Inc()
			})
			err := r.createErrorSecret(ctx, tr, targetPlatform, secretName, "Error allocating host: "+err.Error())
			if err != nil {
				log.Error(err, "could not create error secret")
			}
		}
		return res, err
	}
}

func extractPlatform(tr *tektonapi.TaskRun) (string, error) {
	for _, p := range tr.Spec.Params {
		if p.Name == PlatformParam {
			return p.Value.StringVal, nil
		}
	}
	return "", errors2.New("failed to determine platform")
}

func (r *ReconcileTaskRun) handleHostAllocation(ctx context.Context, tr *tektonapi.TaskRun, secretName string, targetPlatform string) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("attempting to allocate host", "platform", targetPlatform)
	if tr.Labels == nil {
		tr.Labels = map[string]string{}
	}
	if r.apiReader != nil {
		err := r.apiReader.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)
		if err != nil {
			return reconcile.Result{}, err
		}
	}
	//check the secret does not already exist
	secret := kubecore.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: secretName}, &secret)
	if err != nil {
		if !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
	} else {
		//secret already exists (probably error secret)
		return reconcile.Result{}, nil
	}

	//lets allocate a host, get the map with host info
	hosts, err := r.readConfiguration(ctx, targetPlatform, tr.Namespace)
	if err != nil {
		log.Error(err, "failed to read host config")
		mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.HostAllocationFailures.Inc()
		})
		return reconcile.Result{}, r.createErrorSecret(ctx, tr, targetPlatform, secretName, "failed to read host config "+err.Error())
	}
	if tr.Annotations == nil {
		tr.Annotations = map[string]string{}
	}
	wasWaiting := tr.Labels[WaitingForPlatformLabel] != ""
	startTime := time.Now().Unix()
	ret, err := hosts.Allocate(r, ctx, tr, secretName)
	isWaiting := tr.Labels[WaitingForPlatformLabel] != ""

	if err != nil {
		mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.HostAllocationFailures.Inc()
		})
	} else {
		if tr.Labels[AssignedHost] != "" {
			alternateStart := tr.Annotations[AllocationStartTimeAnnotation]
			if alternateStart != "" {
				startTime, err = strconv.ParseInt(alternateStart, 10, 64)
			}
			mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
				metrics.AllocationTime.Observe(float64(time.Now().Unix() - startTime))
				metrics.RunningTasks.Inc()
			})
		}
		if wasWaiting {
			mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
				metrics.WaitTime.Observe(float64(time.Now().Unix() - tr.CreationTimestamp.Unix()))
			})
		}
		if isWaiting && !wasWaiting {
			mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
				metrics.WaitingTasks.Inc()
			})
		} else if !isWaiting && wasWaiting {
			mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
				metrics.WaitingTasks.Dec()
			})
		}
	}
	return ret, err
}

func (r *ReconcileTaskRun) handleHostAssigned(ctx context.Context, tr *tektonapi.TaskRun, secretName string) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx)
	//already exists
	if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
		selectedHost := tr.Labels[AssignedHost]
		log.Info(fmt.Sprintf("unassigning host %s from task", selectedHost))

		platform, err := extractPlatform(tr)
		if err != nil {
			return reconcile.Result{}, err
		}
		config, err := r.readConfiguration(ctx, platform, tr.Namespace)
		if err != nil {
			return reconcile.Result{}, err
		}
		if config == nil {
			log.Error(fmt.Errorf("could not find config for platform %s", platform), "could not find config")
			return reconcile.Result{}, nil
		}
		mpcmetrics.HandleMetrics(platform, func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.TaskRunTime.Observe(float64(time.Now().Unix() - tr.CreationTimestamp.Unix()))
			metrics.RunningTasks.Dec()
		})
		err = config.Deallocate(r, ctx, tr, secretName, selectedHost)
		if err != nil {
			log.Error(err, "Failed to deallocate host "+selectedHost)
		}
		controllerutil.RemoveFinalizer(tr, PipelineFinalizer)
		delete(tr.Labels, AssignedHost)
		err = r.client.Update(ctx, tr)
		if err != nil {
			return reconcile.Result{}, err
		}

		secret := kubecore.Secret{}
		//delete the secret
		err = r.client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: secretName}, &secret)
		if err == nil {
			log.Info("deleting secret from task")
			//PR is done, clean up the secret
			err := r.client.Delete(ctx, &secret)
			if err != nil {
				log.Error(err, "unable to delete secret")
			}
		} else if !errors.IsNotFound(err) {
			log.Error(err, "error deleting secret", "secret", secretName)
			return reconcile.Result{}, err
		} else {
			log.Info("could not find secret", "secret", secretName)
		}
		return r.handleWaitingTasks(ctx, platform)
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileTaskRun) readConfiguration(ctx context.Context, targetPlatform string, targetNamespace string) (PlatformConfig, error) {

	cm := kubecore.ConfigMap{}
	err := r.client.Get(ctx, types.NamespacedName{Namespace: r.operatorNamespace, Name: HostConfig}, &cm)
	if err != nil {
		return nil, err
	}
	log := logr.FromContextOrDiscard(ctx)
	if r.configMapResourceVersion != cm.ResourceVersion {
		//if the config map has changes then dump the cached config
		//metrics are fine, as they don't depend on the config anyway
		r.configMapResourceVersion = cm.ResourceVersion
		r.platformConfig = map[string]PlatformConfig{}
	}

	namespaces := cm.Data[AllowedNamespaces]
	if namespaces != "" {
		parts := strings.Split(namespaces, ",")
		ok := false
		for _, i := range parts {
			matchString, err := regexp.MatchString(i, targetNamespace)
			if err != nil {
				log.Error(err, "invalid allowed-namespace regex")
				continue
			}
			if matchString {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("namespace %s does not match any namespace defined in allowed namespaces, ask an administrator to enable multi platform builds for your namespace", targetNamespace)
		}
	}
	existing := r.platformConfig[targetPlatform]
	if existing != nil {
		return existing, nil
	}

	var additionalInstanceTags map[string]string
	if val, ok := cm.Data[AdditionalInstanceTags]; !ok {
		additionalInstanceTags = map[string]string{}
	} else {
		additionalTagsArray := strings.Split(val, ",")
		additionalInstanceTags = make(map[string]string, len(additionalTagsArray))
		for _, tag := range additionalTagsArray {
			parts := strings.Split(tag, "=")
			additionalInstanceTags[parts[0]] = parts[1]
		}
	}

	local := strings.Split(cm.Data[LocalPlatforms], ",")
	if slices.Contains(local, targetPlatform) {
		return Local{}, nil
	}

	dynamic := cm.Data[DynamicPlatforms]
	for _, platform := range strings.Split(dynamic, ",") {
		platformConfigName := strings.ReplaceAll(platform, "/", "-")
		if platform == targetPlatform {

			typeName := cm.Data["dynamic."+platformConfigName+".type"]
			allocfunc := r.cloudProviders[typeName]
			if allocfunc == nil {
				return nil, errors2.New("unknown dynamic provisioning type " + typeName)
			}
			maxInstances, err := strconv.Atoi(cm.Data["dynamic."+platformConfigName+".max-instances"])
			if err != nil {
				return nil, err
			}
			instanceTag := cm.Data["dynamic."+platformConfigName+".instance-tag"]
			if instanceTag == "" {
				instanceTag = cm.Data[DefaultInstanceTag]
			}
			timeoutSeconds := cm.Data["dynamic."+platformConfigName+".allocation-timeout"]
			timeout := int64(600) //default to 10 minutes
			if timeoutSeconds != "" {
				timeoutInt, err := strconv.Atoi(timeoutSeconds)
				if err != nil {
					log.Error(err, "unable to parse allocation timeout")
				} else {
					timeout = int64(timeoutInt)
				}
			}
			ret := DynamicResolver{
				CloudProvider:          allocfunc(platformConfigName, cm.Data, r.operatorNamespace),
				sshSecret:              cm.Data["dynamic."+platformConfigName+".ssh-secret"],
				platform:               platform,
				maxInstances:           maxInstances,
				instanceTag:            instanceTag,
				timeout:                timeout,
				sudoCommands:           cm.Data["dynamic."+platformConfigName+".sudo-commands"],
				additionalInstanceTags: additionalInstanceTags,
				eventRecorder:          r.eventRecorder,
			}
			r.platformConfig[targetPlatform] = ret
			err = mpcmetrics.RegisterPlatformMetrics(ctx, targetPlatform)
			if err != nil {
				return nil, err
			}
			return ret, nil
		}
	}

	dynamicPool := cm.Data[DynamicPoolPlatforms]
	for _, platform := range strings.Split(dynamicPool, ",") {
		platformConfigName := strings.ReplaceAll(platform, "/", "-")
		if platform == targetPlatform {

			typeName := cm.Data["dynamic."+platformConfigName+".type"]
			allocfunc := r.cloudProviders[typeName]
			if allocfunc == nil {
				return nil, errors2.New("unknown dynamic provisioning type " + typeName)
			}
			maxInstances, err := strconv.Atoi(cm.Data["dynamic."+platformConfigName+".max-instances"])
			if err != nil {
				return nil, err
			}
			concurrency, err := strconv.Atoi(cm.Data["dynamic."+platformConfigName+".concurrency"])
			if err != nil {
				return nil, err
			}
			maxAge, err := strconv.Atoi(cm.Data["dynamic."+platformConfigName+".max-age"]) // Minutes
			if err != nil {
				return nil, err
			}

			instanceTag := cm.Data["dynamic."+platformConfigName+".instance-tag"]
			if instanceTag == "" {
				instanceTag = cm.Data["instance-tag"]
			}
			ret := DynamicHostPool{
				cloudProvider:          allocfunc(platformConfigName, cm.Data, r.operatorNamespace),
				sshSecret:              cm.Data["dynamic."+platformConfigName+".ssh-secret"],
				platform:               platform,
				maxInstances:           maxInstances,
				maxAge:                 time.Minute * time.Duration(maxAge),
				concurrency:            concurrency,
				instanceTag:            instanceTag,
				additionalInstanceTags: additionalInstanceTags,
			}
			r.platformConfig[targetPlatform] = ret
			err = mpcmetrics.RegisterPlatformMetrics(ctx, targetPlatform)
			if err != nil {
				return nil, err
			}
			return ret, nil
		}
	}

	ret := HostPool{hosts: map[string]*Host{}, targetPlatform: targetPlatform}
	for k, v := range cm.Data {
		if !strings.HasPrefix(k, "host.") {
			continue
		}
		k = k[len("host."):]
		pos := strings.LastIndex(k, ".")
		if pos == -1 {
			continue
		}
		name := k[0:pos]
		key := k[pos+1:]
		host := ret.hosts[name]
		if host == nil {
			host = &Host{}
			ret.hosts[name] = host
			host.Name = name
		}
		switch key {
		case "address":
			host.Address = v
		case "user":
			host.User = v
		case "platform":
			host.Platform = v
		case "secret":
			host.Secret = v
		case "concurrency":
			atoi, err := strconv.Atoi(v)
			if err != nil {
				return nil, err
			}
			host.Concurrency = atoi
		default:
			log.Info("unknown key", "key", key)
		}

	}
	r.platformConfig[targetPlatform] = ret
	err = mpcmetrics.RegisterPlatformMetrics(ctx, targetPlatform)
	if err != nil {
		return nil, err
	}
	return ret, nil
}

type PlatformConfig interface {
	Allocate(r *ReconcileTaskRun, ctx context.Context, tr *tektonapi.TaskRun, secretName string) (reconcile.Result, error)
	Deallocate(r *ReconcileTaskRun, ctx context.Context, tr *tektonapi.TaskRun, secretName string, selectedHost string) error
}

func launchProvisioningTask(r *ReconcileTaskRun, ctx context.Context, tr *tektonapi.TaskRun, secretName string, sshSecret string, address string, user string, platform string, sudoCommands string) error {
	//kick off the provisioning task
	//note that we can't use owner refs here because this task runs in a different namespace

	//first verify the secret exists, so we don't hang if it is missing
	secret := kubecore.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Namespace: r.operatorNamespace, Name: sshSecret}, &secret)
	if err != nil {
		log := logr.FromContextOrDiscard(ctx)
		log.Error(fmt.Errorf("failed to find SSH secret %s", sshSecret), "failed to find SSH secret")
		return r.createErrorSecret(ctx, tr, platform, secretName, "failed to get SSH secret, system may not be configured correctly")
	}

	provision := tektonapi.TaskRun{}
	provision.GenerateName = "provision-task"
	provision.Namespace = r.operatorNamespace
	provision.Labels = map[string]string{TaskTypeLabel: TaskTypeProvision, UserTaskNamespace: tr.Namespace, UserTaskName: tr.Name, AssignedHost: tr.Labels[AssignedHost]}
	provision.Annotations = map[string]string{TaskTargetPlatformAnnotation: platformLabel(platform)}
	provision.Spec.TaskRef = &tektonapi.TaskRef{Name: "provision-shared-host"}
	provision.Spec.Workspaces = []tektonapi.WorkspaceBinding{{Name: "ssh", Secret: &kubecore.SecretVolumeSource{SecretName: sshSecret}}}
	computeRequests := map[kubecore.ResourceName]resource.Quantity{kubecore.ResourceCPU: resource.MustParse("100m"), kubecore.ResourceMemory: resource.MustParse("256Mi")}
	computeLimits := map[kubecore.ResourceName]resource.Quantity{kubecore.ResourceCPU: resource.MustParse("100m"), kubecore.ResourceMemory: resource.MustParse("512Mi")}
	provision.Spec.ComputeResources = &kubecore.ResourceRequirements{Requests: computeRequests, Limits: computeLimits}
	provision.Spec.ServiceAccountName = ServiceAccountName //TODO: special service account for this

	provision.Spec.Params = []tektonapi.Param{
		{
			Name:  ParamSecretName,
			Value: *tektonapi.NewStructuredValues(secretName),
		},
		{
			Name:  ParamTaskrunName,
			Value: *tektonapi.NewStructuredValues(tr.Name),
		},
		{
			Name:  ParamNamespace,
			Value: *tektonapi.NewStructuredValues(tr.Namespace),
		},
		{
			Name:  ParamHost,
			Value: *tektonapi.NewStructuredValues(address),
		},
		{
			Name:  ParamUser,
			Value: *tektonapi.NewStructuredValues(user),
		},
		{
			Name:  ParamSudoCommands,
			Value: *tektonapi.NewStructuredValues(sudoCommands),
		},
	}

	err = r.client.Create(ctx, &provision)
	return err
}

type Host struct {
	Address     string
	Name        string
	User        string
	Concurrency int
	Platform    string
	Secret      string
	StartTime   *time.Time // Only used for the dynamic pool
}

func platformLabel(platform string) string {
	return strings.ReplaceAll(platform, "/", "-")
}
