/*
Copyright 2024.

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

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SecretRef struct {
	// +required
	SecretName string `json:"secretName"`
	// +required
	SecretKey string `json:"secretKey"`
}

type ResourcesSpec struct {
	// +required
	Limits corev1.ResourceList `json:"limits,omitempty"`
}

type SnapshotSpec struct {
	// +optional
	// +default=false
	Enabled bool `json:"enabled"`

	// +optional
	// +default="0 * * * *"
	Schedule string `json:"schedule"`
}

type StorageSpec struct {
	// +optional
	// +default=false
	Enabled bool `json:"enabled"`
	// +optional
	// +default=s3
	// +kubebuilder:validation:Pattern="(s3|.{0})"
	Type string `json:"type"`
	// +optional
	Config map[string]string `json:"config"`
}

type ImageSpec struct {
	// +default=ghcr.io/orderly-queue/orderly
	Repository string `json:"repository"`
	// +default=latest
	Tag string `json:"tag"`
}

type IngressSpec struct {
	// +optional
	Enabled bool `json:"enabled"`
	// +optional
	Host string `json:"host"`
	// +optional
	// +default="nginx"
	IngressClass string `json:"ingressClass"`
}

// QueueSpec defines the desired state of Queue
type QueueSpec struct {
	// +optional
	Image ImageSpec `json:"image"`
	// +required
	Resources ResourcesSpec `json:"resources"`

	// +required
	EncryptionKey SecretRef `json:"encryptionKey"`
	// +required
	JwtSecret SecretRef `json:"jwtSecret"`

	// +optional
	Snapshots SnapshotSpec `json:"snapshots"`

	// +optional
	Storage StorageSpec `json:"storage"`

	// +optional
	Ingress IngressSpec `json:"ingress"`
}

// QueueStatus defines the observed state of Queue
type QueueStatus struct {
	// +optional
	DeploymentRevision string `json:"deploymentRevision"`
	// +optional
	ConfigRevision string `json:"configRevision"`
	// +optional
	IngressRevision string `json:"ingressRevision"`
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors={"urn:alm:descriptor:io.kubernetes.conditions"}
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (q *QueueStatus) SetCondition(condition metav1.Condition) {
	if q.Conditions == nil {
		q.Conditions = make([]metav1.Condition, 0)
	}
	meta.SetStatusCondition(&q.Conditions, condition)
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Config",type="string",JSONPath=".status.conditions[?(@.type==\"Config\")].status"
// +kubebuilder:printcolumn:name="Deployment",type="string",JSONPath=".status.conditions[?(@.type==\"Deployment\")].status"
// +kubebuilder:printcolumn:name="Ingress",type="string",JSONPath=".status.conditions[?(@.type==\"Ingress\")].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Queue is the Schema for the queues API
type Queue struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   QueueSpec   `json:"spec,omitempty"`
	Status QueueStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// QueueList contains a list of Queue
type QueueList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Queue `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Queue{}, &QueueList{})
}
