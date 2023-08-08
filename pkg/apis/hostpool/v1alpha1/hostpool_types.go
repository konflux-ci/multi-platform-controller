package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type HostPoolSpec struct {
}

type HostPoolStatus struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=hostpool,scope=Namespaced
// HostPool TODO
type HostPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HostPoolSpec   `json:"spec"`
	Status HostPoolStatus `json:"status,omitempty"`
}
