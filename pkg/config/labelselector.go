package config

import (
	"context"

	kubecore "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	HostConfig           string = "host-config"
	TaskRunLabelSelector string = "taskrun-labelselector"
)

// ReadConfigurationTaskRunLabelSelector if present in configuration, it returns the LabelSelector to use to filter TaskRuns when
// reconciling them. Any error produced when retrieving the Configuration or parsing the LabelSelector is propagated to the caller.
func ReadConfigurationTaskRunLabelSelector(ctx context.Context, cli client.Client, namespace string) (labels.Selector, error) {
	cm := kubecore.ConfigMap{}
	err := cli.Get(ctx, types.NamespacedName{Namespace: namespace, Name: HostConfig}, &cm)
	if err != nil {
		return nil, err
	}

	// retrieve TaskRuns LabelSelector from Configuration
	ls, ok := cm.Data[TaskRunLabelSelector]
	if !ok {
		return nil, nil
	}

	// parse LabelSelector
	return labels.Parse(ls)
}
