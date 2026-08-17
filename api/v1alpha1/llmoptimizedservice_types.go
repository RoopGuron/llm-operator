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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/runtime/schema"
	// "sigs.k8s.io/controller-runtime/pkg/scheme"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// LLMOptimizedServiceSpec defines the desired state of LLMOptimizedService
type LLMOptimizedServiceSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// ModelPath represents the model layout name, e.g., "llama3" or "mistral"
	ModelPath string `json:"modelPath"`

	// MinReplicas sets the baseline scaling target floor
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas sets the ceiling target for Week 3 KEDA structures
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`

	// MaxConcurrencyPerPod sets parallel thread processing density per engine
	MaxConcurrencyPerPod int32 `json:"maxConcurrencyPerPod"`

	// ScaleToZero lets KEDA scale the Deployment down to 0 replicas after
	// IdleTimeoutSeconds of no load, instead of holding at MinReplicas. Model
	// weights stay cached on the node's hostPath volume, so scaling back up
	// re-pulls quickly, but there is no automatic wake-on-request: at 0
	// replicas nothing is running to observe new traffic, so bringing
	// replicas back above 0 is a manual step (e.g. kubectl scale).
	ScaleToZero bool `json:"scaleToZero,omitempty"`

	// IdleTimeoutSeconds sets how long the queue-length metric must stay
	// below threshold before KEDA scales to 0. Only used when ScaleToZero is
	// true; defaults to 300 (5 minutes) if unset.
	IdleTimeoutSeconds *int32 `json:"idleTimeoutSeconds,omitempty"`
}

// LLMOptimizedServiceStatus defines the observed state of LLMOptimizedService.
type LLMOptimizedServiceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the LLMOptimizedService resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	AvailableReplicas int32  `json:"availableReplicas"`
	Phase             string `json:"phase"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// LLMOptimizedService is the Schema for the llmoptimizedservices API
type LLMOptimizedService struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of LLMOptimizedService
	// +required
	Spec LLMOptimizedServiceSpec `json:"spec"`

	// status defines the observed state of LLMOptimizedService
	// +optional
	Status LLMOptimizedServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LLMOptimizedServiceList contains a list of LLMOptimizedService
type LLMOptimizedServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LLMOptimizedService `json:"items"`
}
