/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// PVCAutoscalerSpec defines the desired state of PVCAutoscaler
type PVCAutoscalerSpec struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	PVCName string `json:"pvcName"`
	// +required
	IncreaseBy resource.Quantity `json:"increaseBy"`

	// +optional
	// +kubebuilder:default=85
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=99
	ThresholdPercent *int64 `json:"thresholdPercent,omitempty"`
	// +optional
	// +kubebuilder:default="5m"
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	Cooldown *metav1.Duration `json:"cooldown,omitempty"`

	// +required
	MaxSize resource.Quantity `json:"maxSize"`
}

// PVCAutoscalerStatus defines the observed state of PVCAutoscaler.
type PVCAutoscalerStatus struct {
	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the PVCAutoscaler resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	Used *resource.Quantity `json:"used,omitempty"`
	// +optional
	UsedPercent *int64 `json:"usedPercent,omitempty"`
	// +optional
	Capacity *resource.Quantity `json:"capacity,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	LastResizeTime *metav1.Time `json:"lastResizeTime,omitempty"`
}

// PVCAutoscaler is the Schema for the pvcautoscalers API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=pvca
// +kubebuilder:printcolumn:name="PVC",type=string,JSONPath=`.spec.pvcName`
// +kubebuilder:printcolumn:name="Used",type=string,JSONPath=`.status.used`
// +kubebuilder:printcolumn:name="UsedPercent",type=string,JSONPath=`.status.usedPercent`
// +kubebuilder:printcolumn:name="Capacity",type=string,JSONPath=`.status.capacity`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`,priority=1
type PVCAutoscaler struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of PVCAutoscaler
	// +required
	Spec PVCAutoscalerSpec `json:"spec"`

	// status defines the observed state of PVCAutoscaler
	// +optional
	Status PVCAutoscalerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PVCAutoscalerList contains a list of PVCAutoscaler
type PVCAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PVCAutoscaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PVCAutoscaler{}, &PVCAutoscalerList{})
		return nil
	})
}
