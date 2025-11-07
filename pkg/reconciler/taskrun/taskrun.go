package taskrun

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"knative.dev/pkg/kmeta"

	mpcmetrics "github.com/konflux-ci/multi-platform-controller/pkg/metrics"

	"github.com/konflux-ci/multi-platform-controller/pkg/aws"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	"github.com/konflux-ci/multi-platform-controller/pkg/config"
	"github.com/konflux-ci/multi-platform-controller/pkg/constant"
	"github.com/konflux-ci/multi-platform-controller/pkg/ibm"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	kubecore "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
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

	FailedHosts            = "build.appstudio.redhat.com/failed-hosts"
	CloudInstanceId        = "build.appstudio.redhat.com/cloud-instance-id"
	CloudFailures          = "build.appstudio.redhat.com/cloud-failure-count"
	CloudAddress           = "build.appstudio.redhat.com/cloud-address"
	CloudDynamicPlatform   = "build.appstudio.redhat.com/cloud-dynamic-platform"
	ProvisionTaskProcessed = "build.appstudio.redhat.com/provision-task-processed"
	// ProvisionTaskFinalizer = "build.appstudio.redhat.com/provision-task-finalizer"

	//AllocationStartTimeAnnotation Some allocations can take multiple calls, we track the actual start time in this annotation
	AllocationStartTimeAnnotation = "build.appstudio.redhat.com/allocation-start-time"
	//BuildStartTimeAnnotation The time the build actually starts
	BuildStartTimeAnnotation = "build.appstudio.redhat.com/build-start-time"

	UserTaskName      = "build.appstudio.redhat.com/user-task-name"
	UserTaskNamespace = "build.appstudio.redhat.com/user-task-namespace"

	FinishedWaitingLabel = "build.appstudio.redhat.com/finished-waiting"
	PipelineFinalizer    = "appstudio.io/multi-platform-finalizer"
	HostConfig           = "host-config"

	TaskTypeLabel     = "build.appstudio.redhat.com/task-type"
	TaskTypeProvision = "provision"
	TaskTypeUpdate    = "update"
	TaskTypeClean     = "clean"

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
		eventRecorder:     mgr.GetEventRecorderFor("MultiPlatformTaskRun"),
		operatorNamespace: operatorNamespace,
		platformConfig:    map[string]PlatformConfig{},
		cloudProviders:    map[string]func(platform string, config map[string]string, systemNamespace string) cloud.CloudProvider{"aws": aws.CreateEc2CloudConfig, "ibmz": ibm.CreateIbmZCloudConfig, "ibmp": ibm.CreateIBMPowerCloudConfig},
	}
}

func (r *ReconcileTaskRun) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	// Set the ctx to be Background, as the top-level context for incoming requests.
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	log := ctrl.Log.WithName("taskrun").WithValues("request", request.NamespacedName)

	tr := tektonapi.TaskRun{}
	if err := r.apiReader.Get(ctx, request.NamespacedName, &tr); err != nil {
		if k8serrors.IsNotFound(err) {
			// gone, no error
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to get a TaskRun: %w", err)
		}
	}

	if tr.Annotations != nil {
		if tr.Annotations[CloudInstanceId] != "" {
			log = log.WithValues(CloudInstanceId, tr.Annotations[CloudInstanceId])
		}
		if tr.Annotations[constant.AssignedHost] != "" {
			log = log.WithValues(constant.AssignedHost, tr.Annotations[constant.AssignedHost])
		}
	}
	ctx = logr.NewContext(ctx, log)

	return r.handleTaskRunReceived(ctx, &tr)
}

func (r *ReconcileTaskRun) handleTaskRunReceived(ctx context.Context, tr *tektonapi.TaskRun) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("taskrun", tr.Name, "namespace", tr.Namespace)

	// Handle internal task types first
	if tr.Labels != nil {
		if taskType := tr.Labels[TaskTypeLabel]; taskType != "" {
			switch taskType {
			case TaskTypeClean:
				log.Info("Reconciling cleanup task")
				return r.handleCleanTask(ctx, tr)
			case TaskTypeProvision:
				log.Info("Reconciling provision task")
				return r.handleProvisionTask(ctx, tr)
			case TaskTypeUpdate:
				log.V(1).Info("Ignoring update task")
				return reconcile.Result{}, nil
			default:
				log.V(1).Info("Unknown task type, ignoring", "taskType", taskType)
				return reconcile.Result{}, nil
			}
		}
	}

	// Early validation for user tasks - consolidated checks
	if tr.Spec.Params == nil || tr.Status.TaskSpec == nil || tr.Status.TaskSpec.Volumes == nil {
		log.V(1).Info("Skipping TaskRun - missing required structure")
		return reconcile.Result{}, nil
	}

	// Check for multi-platform secret
	hasMultiPlatformSecret := false
	for _, volume := range tr.Status.TaskSpec.Volumes {
		if volume.Secret != nil && strings.HasPrefix(volume.Secret.SecretName, SecretPrefix) {
			hasMultiPlatformSecret = true
			break
		}
	}
	if !hasMultiPlatformSecret {
		log.V(1).Info("Skipping TaskRun - no multi-platform secret found")
		return reconcile.Result{}, nil
	}

	// Check for platform parameter
	hasPlatformParam := false
	for _, param := range tr.Spec.Params {
		if param.Name == PlatformParam {
			hasPlatformParam = true
			break
		}
	}
	if !hasPlatformParam {
		log.V(1).Info("Skipping TaskRun - no platform parameter found")
		return reconcile.Result{}, nil
	}

	log.Info("Reconciling user task")
	return r.handleUserTask(ctx, tr)
}

func (r *ReconcileTaskRun) handleCleanTask(ctx context.Context, tr *tektonapi.TaskRun) (reconcile.Result, error) {
	if !tr.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if tr.Status.CompletionTime == nil {
		return reconcile.Result{}, nil
	}
	success := tr.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
	if !success {
		log := logr.FromContextOrDiscard(ctx)
		log.Info("cleanup task failed", "task", tr.Name)
		if tr.Labels != nil && tr.Labels[constant.TargetPlatformLabel] != "" {
			mpcmetrics.HandleMetrics(tr.Labels[constant.TargetPlatformLabel], func(metrics *mpcmetrics.PlatformMetrics) {
				metrics.CleanupFailures.Inc()
			})
		}
	}
	//leave the failed TR for an hour to view logs
	if success || tr.Status.CompletionTime.Add(time.Hour).Before(time.Now()) {
		return reconcile.Result{}, r.client.Delete(ctx, tr)
	}
	return reconcile.Result{RequeueAfter: time.Hour}, nil
}

func (r *ReconcileTaskRun) handleProvisionTask(ctx context.Context, tr *tektonapi.TaskRun) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx)

	if !tr.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if tr.Status.CompletionTime == nil {
		return reconcile.Result{}, nil
	}
	if tr.Annotations == nil {
		tr.Annotations = map[string]string{}
	}
	success := tr.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
	if tr.Annotations[ProvisionTaskProcessed] == "true" {
		retentionTime := time.Hour
		if success || tr.Status.CompletionTime.Add(retentionTime).Before(time.Now()) {
			return reconcile.Result{}, r.client.Delete(ctx, tr)
		}
		log.Info("Keeping failed provision task for log inspection")
		return reconcile.Result{RequeueAfter: retentionTime}, nil
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
	assigned := tr.Labels[constant.AssignedHost]
	targetPlatform := tr.Labels[constant.TargetPlatformLabel]
	log = log.WithValues(
		"success", success,
		"userTask", fmt.Sprintf("%s/%s", userNamespace, userTaskName),
		"assignedHost", assigned,
		"platform", targetPlatform,
	)

	if !success {
		mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.ProvisionFailures.Inc()
		})
		message := fmt.Sprintf("provision task for host %s for user task %s/%s failed", assigned, userNamespace, userTaskName)
		r.eventRecorder.Event(tr, "Error", "ProvisioningFailed", message)
		log.Error(errors.New("provision failed"), message)
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
				delete(userTr.Labels, constant.AssignedHost)
				err = r.client.Update(ctx, &userTr)
				if err != nil {
					return reconcile.Result{}, err
				}
				delete(tr.Labels, constant.AssignedHost)
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
		//verify we ended up with a secret
		secret := kubecore.Secret{}
		err := r.client.Get(ctx, types.NamespacedName{Namespace: userNamespace, Name: secretName}, &secret)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				userTr := tektonapi.TaskRun{}
				err = r.client.Get(ctx, types.NamespacedName{Namespace: userNamespace, Name: userTaskName}, &userTr)
				if err != nil {
					if !k8serrors.IsNotFound(err) {
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
				pod.Annotations[constant.AssignedHost] = assigned
				err = r.client.Update(ctx, &pod)
				if err != nil {
					log.Error(err, "unable to annotate task pod")
				}
			}
		}
	}

	if err := UpdateTaskRunWithRetry(ctx, r.client, r.apiReader, tr); err != nil {
		return reconcile.Result{}, err
	}

	// after a successful provision task, we increment the provisioning_successes metric
	mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
		metrics.ProvisionSuccesses.Inc()
	})

	return reconcile.Result{}, nil
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
		if k8serrors.IsAlreadyExists(err) {
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
	if tr.Labels != nil && tr.Labels[constant.AssignedHost] != "" {
		return r.handleHostAssigned(ctx, tr, secretName)
	}
	//if the TR is done we ignore it
	if tr.Status.CompletionTime != nil || tr.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(tr, PipelineFinalizer) {
			return r.handleHostAssigned(ctx, tr, secretName)
		}
		return reconcile.Result{}, nil
	}

	targetPlatform, err := config.ValidatePlatform(tr)
	if err != nil {
		err := r.createErrorSecret(ctx, tr, "[UNKNOWN]", secretName, err.Error())
		if err != nil {
			log.Error(err, "could not create error secret")
		}
		return reconcile.Result{}, nil
	}

	if tr.Labels == nil {
		tr.Labels = map[string]string{}
	}

	// Set platform label in memory - it will be persisted during allocation
	if tr.Labels[constant.TargetPlatformLabel] == "" {
		tr.Labels[constant.TargetPlatformLabel] = platformLabel(targetPlatform)
	}

	res, err := r.handleHostAllocation(ctx, tr, secretName, targetPlatform)
	if err != nil && !k8serrors.IsConflict(err) {
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

func (r *ReconcileTaskRun) handleHostAllocation(ctx context.Context, tr *tektonapi.TaskRun, secretName string, targetPlatform string) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("platform", targetPlatform, "secretName", secretName)
	log.Info("attempting to allocate host")
	//check the secret does not already exist
	secret := kubecore.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: secretName}, &secret)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
	} else {
		log.Info("error secret already exists, skipping allocation")
		return reconcile.Result{}, nil
	}

	//let's allocate a host, get the map with host info
	hosts, err := r.getPlatformConfig(ctx, targetPlatform, tr.Namespace)
	if err != nil {
		log.Error(err, "failed to read host config")
		mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.HostAllocationFailures.Inc()
		})
		return reconcile.Result{}, r.createErrorSecret(ctx, tr, targetPlatform, secretName, fmt.Sprintf("failed to read host config: %v", err))
	}
	if tr.Annotations == nil {
		tr.Annotations = map[string]string{}
	}

	// Track waiting state and allocation timing
	wasWaiting := tr.Labels[constant.WaitingForPlatformLabel] != ""
	startTime := time.Now().Unix()

	// Parse existing allocation start time if available
	if alternateStartStr := tr.Annotations[AllocationStartTimeAnnotation]; alternateStartStr != "" {
		if alternateStart, parseErr := strconv.ParseInt(alternateStartStr, 10, 64); parseErr == nil {
			startTime = alternateStart
		} else {
			log.Error(parseErr, "failed to parse allocation start time", "value", alternateStartStr)
		}
	}

	ret, err := hosts.Allocate(r, ctx, tr, secretName)
	isWaiting := tr.Labels[constant.WaitingForPlatformLabel] != ""

	if err != nil {
		log.Error(err, "host allocation failed")
		mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.HostAllocationFailures.Inc()
		})
		return ret, err
	}

	log.Info("host allocation completed", "isWaiting", isWaiting, "assignedHost", tr.Labels[constant.AssignedHost])

	// Host successfully assigned
	if assignedHost := tr.Labels[constant.AssignedHost]; assignedHost != "" {
		log.Info("host assigned successfully", "host", assignedHost)
		mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.AllocationTime.Observe(float64(time.Now().Unix() - startTime))
		})
	}

	// Handle waiting state transitions with clear logging
	if wasWaiting && !isWaiting {
		log.Info("task no longer waiting - host allocated")
		mpcmetrics.HandleMetrics(targetPlatform, func(metrics *mpcmetrics.PlatformMetrics) {
			metrics.WaitTime.Observe(float64(time.Now().Unix() - tr.CreationTimestamp.Unix()))
		})
	} else if !wasWaiting && isWaiting {
		log.Info("task now waiting for host")
	} else if wasWaiting && isWaiting {
		log.V(1).Info("task still waiting for host")
	}

	return ret, err
}

func (r *ReconcileTaskRun) handleHostAssigned(ctx context.Context, tr *tektonapi.TaskRun, secretName string) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("secretName", secretName)

	// Safe access to assigned host
	assignedHost := tr.Labels[constant.AssignedHost]
	log = log.WithValues("assignedHost", assignedHost)

	// Check if TaskRun is completed or being deleted
	isCompleted := tr.Status.CompletionTime != nil
	isBeingDeleted := tr.GetDeletionTimestamp() != nil

	if !isCompleted && !isBeingDeleted {
		log.V(1).Info("TaskRun not completed, nothing to do")
		return reconcile.Result{}, nil
	}

	log.Info("handling completed/deleted TaskRun", "completed", isCompleted, "beingDeleted", isBeingDeleted)

	// Extract platform with error handling
	platform, err := config.ExtractPlatform(tr)
	if err != nil {
		log.Error(err, "failed to extract platform for deallocation")
		return reconcile.Result{}, fmt.Errorf("failed to extract platform: %w", err)
	}
	log = log.WithValues("platform", platform)

	// Get platform configuration
	platformConfig, err := r.getPlatformConfig(ctx, platform, tr.Namespace)
	if err != nil {
		log.Error(err, "failed to read platform configuration")
		return reconcile.Result{}, fmt.Errorf("failed to read configuration: %w", err)
	}
	if platformConfig == nil {
		log.Error(errors.New("no configuration found"), "no config for platform", "platform", platform)
		return reconcile.Result{}, nil
	}

	// Attempt host deallocation
	log.Info("calling host deallocation")
	err = platformConfig.Deallocate(r, ctx, tr, secretName, assignedHost)
	if err != nil {
		log.Error(err, "failed to deallocate host", "host", assignedHost)
		// Continue with cleanup even if deallocation fails
	} else {
		log.Info("host deallocated successfully")
	}

	// Clean up TaskRun labels and finalizers
	controllerutil.RemoveFinalizer(tr, PipelineFinalizer)
	if tr.Labels != nil {
		delete(tr.Labels, constant.AssignedHost)
	}

	// Update TaskRun with cleanup changes
	err = UpdateTaskRunWithRetry(ctx, r.client, r.apiReader, tr)
	if err != nil {
		log.Error(err, "failed to update TaskRun after cleanup")
		return reconcile.Result{}, fmt.Errorf("failed to update TaskRun: %w", err)
	}
	log.Info("TaskRun updated successfully")

	// Update metrics for task completion after a successful cleanup
	log.Info("updating completion metrics")
	taskRunDuration := time.Since(tr.CreationTimestamp.Time)
	mpcmetrics.HandleMetrics(platform, func(metrics *mpcmetrics.PlatformMetrics) {
		metrics.TaskRunTime.Observe(taskRunDuration.Seconds())
	})

	// Handle secret cleanup
	log.Info("cleaning up secret")
	secret := kubecore.Secret{}
	err = r.client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: secretName}, &secret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("secret not found, already cleaned up")
		} else {
			log.Error(err, "error checking secret existence")
			return reconcile.Result{}, fmt.Errorf("error checking secret: %w", err)
		}
	} else {
		log.Info("deleting secret")
		err := r.client.Delete(ctx, &secret)
		if err != nil {
			log.Error(err, "failed to delete secret")
			// Don't return error - secret cleanup is not critical
		} else {
			log.Info("secret deleted successfully")
		}
	}

	// Handle waiting tasks - try to allocate next waiting task
	log.Info("checking for waiting tasks to requeue")
	result, err := r.handleWaitingTasks(ctx, platform)
	if err != nil {
		log.Error(err, "failed to handle waiting tasks")
		// Don't fail the reconciliation for this
		return reconcile.Result{}, nil
	}

	log.Info("host deallocation completed successfully")
	return result, nil
}

// called when a task has finished, we look for waiting tasks
// and then potentially requeue one of them
func (r *ReconcileTaskRun) handleWaitingTasks(ctx context.Context, platform string) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("platform", platform)
	log.Info("checking for waiting tasks to requeue")

	//try and requeue a waiting task if one exists
	taskList := tektonapi.TaskRunList{}

	err := r.client.List(ctx, &taskList, client.MatchingLabels{constant.WaitingForPlatformLabel: platformLabel(platform)})
	if err != nil {
		log.Error(err, "failed to list waiting tasks")
		return reconcile.Result{}, fmt.Errorf("failed to list waiting tasks: %w", err)
	}

	if len(taskList.Items) == 0 {
		return reconcile.Result{}, nil
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
	if oldest == nil {
		return reconcile.Result{}, nil
	}
	//add the "finished-waiting" label, which will trigger a requeue
	oldest.Labels[FinishedWaitingLabel] = "true"

	// Update the task
	err = r.client.Update(ctx, oldest)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to update waiting task %s/%s: %w", oldest.Namespace, oldest.Name, err)
	}

	log.Info("requeued waiting task", "name", oldest.Name)
	return reconcile.Result{}, nil
}

// getPlatformConfig retrieves and caches platform configuration for a given target platform
// This function is the central configuration resolver for the TaskRun reconciler. It handles
// ConfigMap retrieval, caching, cache invalidation, and delegates to specialized parsing functions
// for each platform type (local, dynamic, dynamic pool, and static).
//
// Caching Strategy:
// Configurations are cached by platform name in r.platformConfig. Cache is invalidated when ConfigMap ResourceVersion
// changes. Subsequent requests for the same platform return cached configuration.
//
// Platform Resolution Order:
// The targetPlatform string is searched for across the configuration file. If found in one of the host lists, a
// resolver for the host kinds if returned and if not, the next kind of host is attempted.
// 1st search targetPlatform in the "local-platforms" list - if found, return Local{}
// 2nd search targetPlatform in "dynamic-platforms" list - if found, return DynamicResolver
// 3rd search targetPlatform in "dynamic-pool-platforms" list - if found, return DynamicHostPool
// 4th Returns HostPool containing all static hosts matching the target platform
//
// Parameters:
// - ctx: Context for the request
// - targetPlatform: The platform to retrieve configuration for (e.g., "linux/arm64", "linux/s390x")
// - targetNamespace: The namespace of the requesting TaskRun (currently unused but reserved for future use)
//
// Returns:
// - PlatformConfig: The platform configuration (Local, DynamicResolver, DynamicHostPool, or HostPool)
// - error: ConfigMap retrieval error, parsing error, or metrics registration error
func (r *ReconcileTaskRun) getPlatformConfig(ctx context.Context, targetPlatform string, targetNamespace string) (PlatformConfig, error) {
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
			if len(parts) >= 2 {
				additionalInstanceTags[parts[0]] = parts[1]
			} else {
				log.Error(errors.New("invalid tag format"), "tag must be key=value", "tag", tag)
			}
		}
	}

	data := cm.Data

	// Is our targetPlatform a local platform? Check local platforms
	localPlatforms, err := config.ParsePlatformList(data[LocalPlatforms], config.PlatformTypeLocal)
	if err != nil {
		return nil, fmt.Errorf("could not parse local platforms: %w", err)
	}
	if slices.Contains(localPlatforms, targetPlatform) {
		return Local{}, nil
	}

	// No match? Check DYNAMIC platforms
	dynamicPlatforms, err := config.ParsePlatformList(data[DynamicPlatforms], config.PlatformTypeDynamic)
	if err != nil {
		return nil, fmt.Errorf("could not parse dynamic platforms: %w", err)
	}
	if slices.Contains(dynamicPlatforms, targetPlatform) {
		dynamicConfig, err := config.ParseDynamicPlatformConfig(data, targetPlatform)
		if err != nil {
			return nil, err
		}
		platformConfigName := strings.ReplaceAll(targetPlatform, "/", "-")
		ret, err := r.buildDynamicResolver(ctx, dynamicConfig, targetPlatform, platformConfigName, additionalInstanceTags, data)
		if err != nil {
			return nil, err
		}
		r.platformConfig[targetPlatform] = ret
		return ret, nil
	}

	// No match? Check DYNAMIC POOL platforms
	dynamicPoolPlatforms, err := config.ParsePlatformList(data[DynamicPoolPlatforms], config.PlatformTypeDynamicPool)
	if err != nil {
		return nil, fmt.Errorf("could not parse dynamic pool platforms: %w", err)
	}
	if slices.Contains(dynamicPoolPlatforms, targetPlatform) {
		poolConfig, err := config.ParseDynamicPoolPlatformConfig(data, targetPlatform)
		if err != nil {
			return nil, err
		}
		platformConfigName := strings.ReplaceAll(targetPlatform, "/", "-")
		ret, err := r.buildDynamicHostPool(ctx, poolConfig, targetPlatform, platformConfigName, additionalInstanceTags, data)
		if err != nil {
			return nil, err
		}
		r.platformConfig[targetPlatform] = ret
		return ret, nil
	}

	// Still no match?? Check STATIC platforms
	// Collect all hosts for this platform
	ret := HostPool{hosts: map[string]*Host{}, targetPlatform: targetPlatform}
	hostNames := make(map[string]bool)

	// First, find all unique host names
	for key := range data {
		if !strings.HasPrefix(key, "host.") {
			continue
		}
		k := key[len("host."):]
		pos := strings.LastIndex(k, ".")
		if pos == -1 {
			continue
		}
		name := k[0:pos]
		hostNames[name] = true
	}

	// Parse each host and add to pool if it matches our platform
	for hostName := range hostNames {
		hostConfig, err := config.ParseStaticHostConfig(data, hostName)
		if err != nil {
			return nil, fmt.Errorf("failed to parse static host '%s': %w", hostName, err)
		}
		// Only add hosts that match our target platform
		if hostConfig.Platform == targetPlatform {
			ret.hosts[hostName] = &Host{
				Name:        hostName,
				Address:     hostConfig.Address,
				User:        hostConfig.User,
				Platform:    hostConfig.Platform,
				Secret:      hostConfig.Secret,
				Concurrency: hostConfig.Concurrency,
			}
		}
	}

	// Calculate platform capacity (will be 0 if no hosts match)
	platformCapacity := 0
	for _, host := range ret.hosts {
		platformCapacity += host.Concurrency
	}

	// Always cache and register metrics, even if no hosts match
	r.platformConfig[targetPlatform] = ret
	err = mpcmetrics.RegisterPlatformMetrics(ctx, targetPlatform, platformCapacity)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// buildDynamicResolver constructs a DynamicResolver from parsed dynamic platform configuration
// This function is a helper for getPlatformConfig that builds the complete DynamicResolver object
// from the parsed configuration returned by parseDynamicPlatformConfig. It handles cloud provider
// initialization, instance tag resolution, and metrics registration.
//
// Cloud Provider Initialization:
// - Looks up cloud provider constructor function from r.cloudProviders map using config.Type
// - Supported types: "aws", "ibmz", "ibmp"
// - Constructor receives platformConfigName, full ConfigMap data, and operator namespace
// - Returns error if cloud provider type is unknown
//
// Parameters:
// - ctx: Context for metrics registration
// - config: Parsed dynamic platform configuration from parseDynamicPlatformConfig
// - platform: Target platform string (e.g., "linux/arm64")
// - platformConfigName: Platform config name with dashes (e.g., "linux-arm64")
// - additionalInstanceTags: Additional instance tags from ConfigMap
// - data: Full ConfigMap data for cloud provider initialization
//
// Returns:
// - DynamicResolver: Fully initialized dynamic platform resolver
// - error: Cloud provider lookup error or metrics registration error
func (r *ReconcileTaskRun) buildDynamicResolver(ctx context.Context, dynamicConfig config.DynamicPlatformConfig, platform string, platformConfigName string, additionalInstanceTags map[string]string, data map[string]string) (DynamicResolver, error) {
	allocfunc := r.cloudProviders[dynamicConfig.Type]

	// Use instance tag from config, fall back to default if empty
	instanceTag := dynamicConfig.InstanceTag
	if instanceTag == "" {
		instanceTag = data[DefaultInstanceTag]
	}

	ret := DynamicResolver{
		CloudProvider:          allocfunc(platformConfigName, data, r.operatorNamespace),
		sshSecret:              dynamicConfig.SSHSecret,
		platform:               platform,
		maxInstances:           dynamicConfig.MaxInstances,
		instanceTag:            instanceTag,
		timeout:                dynamicConfig.AllocationTimeout,
		sudoCommands:           dynamicConfig.SudoCommands,
		additionalInstanceTags: additionalInstanceTags,
		eventRecorder:          r.eventRecorder,
	}

	err := mpcmetrics.RegisterPlatformMetrics(ctx, platform, dynamicConfig.MaxInstances)
	if err != nil {
		return DynamicResolver{}, err
	}

	return ret, nil
}

// buildDynamicHostPool constructs a DynamicHostPool from parsed dynamic pool platform configuration
// This function is a helper for getPlatformConfig that builds the complete DynamicHostPool object
// from the parsed configuration returned by parseDynamicPoolPlatformConfig. It handles cloud provider
// initialization, instance tag resolution, and metrics registration. Dynamic host pools combine
// fixed concurrency with auto-scaling and TTL-based host lifecycle management.
//
// Cloud Provider Initialization:
// - Looks up cloud provider constructor function from r.cloudProviders map using config.Type
// - Supported types: "aws", "ibmz", "ibmp"
// - Constructor receives platformConfigName, full ConfigMap data, and operator namespace
// - Returns error if cloud provider type is unknown
//
// Parameters:
// - ctx: Context for metrics registration
// - config: Parsed dynamic pool platform configuration from parseDynamicPoolPlatformConfig
// - platform: Target platform string (e.g., "linux/arm64")
// - platformConfigName: Platform config name with dashes (e.g., "linux-arm64")
// - additionalInstanceTags: Additional instance tags from ConfigMap
// - data: Full ConfigMap data for cloud provider initialization
//
// Returns:
// - DynamicHostPool: Fully initialized dynamic pool platform resolver
// - error: Cloud provider lookup error or metrics registration error
func (r *ReconcileTaskRun) buildDynamicHostPool(ctx context.Context, poolConfig config.DynamicPoolPlatformConfig, platform string, platformConfigName string, additionalInstanceTags map[string]string, data map[string]string) (DynamicHostPool, error) {
	allocfunc := r.cloudProviders[poolConfig.Type]

	// Use instance tag from config, fall back to default if empty
	instanceTag := poolConfig.InstanceTag
	if instanceTag == "" {
		instanceTag = data[DefaultInstanceTag]
	}

	ret := DynamicHostPool{
		cloudProvider:          allocfunc(platformConfigName, data, r.operatorNamespace),
		sshSecret:              poolConfig.SSHSecret,
		platform:               platform,
		maxInstances:           poolConfig.MaxInstances,
		maxAge:                 time.Minute * time.Duration(poolConfig.MaxAge),
		concurrency:            poolConfig.Concurrency,
		instanceTag:            instanceTag,
		additionalInstanceTags: additionalInstanceTags,
	}

	err := mpcmetrics.RegisterPlatformMetrics(ctx, platform, poolConfig.MaxInstances)
	if err != nil {
		return DynamicHostPool{}, err
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
	log := logr.FromContextOrDiscard(ctx)
	secret := kubecore.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Namespace: r.operatorNamespace, Name: sshSecret}, &secret)
	if err != nil {
		log := logr.FromContextOrDiscard(ctx)
		log.Error(fmt.Errorf("failed to find SSH secret %s", sshSecret), "failed to find SSH secret")
		return r.createErrorSecret(ctx, tr, platform, secretName, "failed to get SSH secret, system may not be configured correctly")
	}

	provision := tektonapi.TaskRun{}
	provision.Name = kmeta.ChildName(tr.Name, "-provision")
	provision.Namespace = r.operatorNamespace
	provision.Labels = map[string]string{TaskTypeLabel: TaskTypeProvision, constant.TargetPlatformLabel: platformLabel(platform), UserTaskNamespace: tr.Namespace, UserTaskName: tr.Name, constant.AssignedHost: tr.Labels[constant.AssignedHost]}
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
	if k8serrors.IsAlreadyExists(err) {
		log.Info("provision task already exists, continuing")
		return nil // Not an error
	}
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

// UpdateTaskRunWithRetry performs a conflict-resilient update of a TaskRun object.
// On conflict, it fetches the latest version and merges labels, annotations, and finalizers from the incoming TaskRun.
func UpdateTaskRunWithRetry(ctx context.Context, cli client.Client, apiReader client.Reader, tr *tektonapi.TaskRun) error {
	err := cli.Update(ctx, tr)

	// if no error or a not a conflict error happened
	// we need to return here
	if err == nil || !k8serrors.IsConflict(err) {
		return err
	}

	// if a conflict happened we retry
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return updateTaskRun(ctx, cli, apiReader, tr)
	})
}

var (
	managedLabels = []string{
		constant.AssignedHost,
		UserTaskName,
		UserTaskNamespace,
		constant.TargetPlatformLabel,
		constant.WaitingForPlatformLabel,
		TaskTypeLabel,
	}

	managedAnnotations = []string{
		FailedHosts,
		CloudInstanceId,
		ProvisionTaskProcessed,
		AllocationStartTimeAnnotation,
	}
)

func updateTaskRun(ctx context.Context, cli client.Client, apiReader client.Reader, tr *tektonapi.TaskRun) error {
	latest := &tektonapi.TaskRun{}
	if err := apiReader.Get(ctx, client.ObjectKeyFromObject(tr), latest); err != nil {
		return err
	}

	// merge annotations and labels
	latest.Annotations = mergeKeysInMaps(latest.GetAnnotations(), tr.GetAnnotations(), managedAnnotations)
	latest.Labels = mergeKeysInMaps(latest.GetLabels(), tr.GetLabels(), managedLabels)

	// update finalizers
	ensureFinalizerIsUpdated(latest, tr, PipelineFinalizer)

	// update the resource
	return cli.Update(ctx, latest)
}

func ensureFinalizerIsUpdated(latest, mutated *tektonapi.TaskRun, finalizer string) {
	// ensure finalizer is set correctly
	of := controllerutil.ContainsFinalizer(latest, finalizer)
	mf := controllerutil.ContainsFinalizer(mutated, finalizer)

	// if both are present or absent we are fine
	if mf == of {
		return
	}

	if !mf {
		controllerutil.RemoveFinalizer(latest, finalizer)
	} else {
		controllerutil.AddFinalizer(latest, finalizer)
	}
}

func mergeKeysInMaps(latest, mutation map[string]string, keys []string) map[string]string {
	if latest == nil {
		latest = map[string]string{}
	}
	if mutation == nil {
		mutation = map[string]string{}
	}

	for _, k := range keys {
		if a, ok := mutation[k]; ok {
			latest[k] = a
		} else {
			delete(latest, k)
		}
	}

	return latest
}
