/*
Copyright 2022 The Kubernetes Authors.

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

package workload

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1alpha2"
	utiltesting "sigs.k8s.io/kueue/pkg/util/testing"
)

func TestPodRequests(t *testing.T) {
	cases := map[string]struct {
		spec         corev1.PodSpec
		wantRequests Requests
	}{
		"core": {
			spec: corev1.PodSpec{
				Containers: containersForRequests(
					map[corev1.ResourceName]string{
						corev1.ResourceCPU:    "10m",
						corev1.ResourceMemory: "1Ki",
					},
					map[corev1.ResourceName]string{
						corev1.ResourceCPU:              "5m",
						corev1.ResourceEphemeralStorage: "1Ki",
					},
				),
				InitContainers: containersForRequests(
					map[corev1.ResourceName]string{
						corev1.ResourceCPU:    "10m",
						corev1.ResourceMemory: "1Ki",
					},
					map[corev1.ResourceName]string{
						corev1.ResourceMemory: "2Ki",
					},
				),
			},
			wantRequests: Requests{
				corev1.ResourceCPU:              15,
				corev1.ResourceMemory:           2048,
				corev1.ResourceEphemeralStorage: 1024,
			},
		},
		"extended": {
			spec: corev1.PodSpec{
				Containers: containersForRequests(
					map[corev1.ResourceName]string{
						"ex.com/gpu": "2",
					},
					map[corev1.ResourceName]string{
						"ex.com/gpu": "1",
					},
				),
				InitContainers: containersForRequests(
					map[corev1.ResourceName]string{
						"ex.com/ssd": "1",
					},
					map[corev1.ResourceName]string{
						"ex.com/gpu": "1",
						"ex.com/ssd": "1",
					},
				),
			},
			wantRequests: Requests{
				"ex.com/gpu": 3,
				"ex.com/ssd": 1,
			},
		},
		"Pod Overhead defined": {
			spec: corev1.PodSpec{
				Containers: containersForRequests(
					map[corev1.ResourceName]string{
						corev1.ResourceCPU:    "10m",
						corev1.ResourceMemory: "1Ki",
					},
					map[corev1.ResourceName]string{
						corev1.ResourceCPU:              "5m",
						corev1.ResourceEphemeralStorage: "1Ki",
					},
				),
				InitContainers: containersForRequests(
					map[corev1.ResourceName]string{
						corev1.ResourceCPU:    "10m",
						corev1.ResourceMemory: "1Ki",
					},
					map[corev1.ResourceName]string{
						corev1.ResourceMemory: "2Ki",
					},
				),
				Overhead: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("0.1"),
				},
			},
			wantRequests: Requests{
				corev1.ResourceCPU:              115,
				corev1.ResourceMemory:           2048,
				corev1.ResourceEphemeralStorage: 1024,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			gotRequests := podRequests(&tc.spec)
			if diff := cmp.Diff(tc.wantRequests, gotRequests); diff != "" {
				t.Errorf("podRequests returned unexpected requests (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestNewInfo(t *testing.T) {
	cases := map[string]struct {
		workload kueue.Workload
		wantInfo Info
	}{
		"pending": {
			workload: kueue.Workload{
				Spec: kueue.WorkloadSpec{
					PodSets: []kueue.PodSet{
						{
							Name: "driver",
							Spec: corev1.PodSpec{
								Containers: containersForRequests(
									map[corev1.ResourceName]string{
										corev1.ResourceCPU:    "10m",
										corev1.ResourceMemory: "512Ki",
									}),
							},
							Count: 1,
						},
					},
				},
			},
			wantInfo: Info{
				TotalRequests: []PodSetResources{
					{
						Name: "driver",
						Requests: Requests{
							corev1.ResourceCPU:    10,
							corev1.ResourceMemory: 512 * 1024,
						},
					},
				},
			},
		},
		"admitted": {
			workload: kueue.Workload{
				Spec: kueue.WorkloadSpec{
					PodSets: []kueue.PodSet{
						{
							Name: "driver",
							Spec: corev1.PodSpec{
								Containers: containersForRequests(
									map[corev1.ResourceName]string{
										corev1.ResourceCPU:    "10m",
										corev1.ResourceMemory: "512Ki",
									}),
							},
							Count: 1,
						},
						{
							Name: "workers",
							Spec: corev1.PodSpec{
								Containers: containersForRequests(
									map[corev1.ResourceName]string{
										corev1.ResourceCPU:    "5m",
										corev1.ResourceMemory: "1Mi",
										"ex.com/gpu":          "1",
									}),
							},
							Count: 3,
						},
					},
					Admission: &kueue.Admission{
						ClusterQueue: "foo",
						PodSetFlavors: []kueue.PodSetFlavors{
							{
								Name: "driver",
								Flavors: map[corev1.ResourceName]string{
									corev1.ResourceCPU: "on-demand",
								},
							},
						},
					},
				},
			},
			wantInfo: Info{
				ClusterQueue: "foo",
				TotalRequests: []PodSetResources{
					{
						Name: "driver",
						Requests: Requests{
							corev1.ResourceCPU:    10,
							corev1.ResourceMemory: 512 * 1024,
						},
						Flavors: map[corev1.ResourceName]string{
							corev1.ResourceCPU: "on-demand",
						},
					},
					{
						Name: "workers",
						Requests: Requests{
							corev1.ResourceCPU:    15,
							corev1.ResourceMemory: 3 * 1024 * 1024,
							"ex.com/gpu":          3,
						},
					},
				},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			info := NewInfo(&tc.workload)
			if diff := cmp.Diff(info, &tc.wantInfo, cmpopts.IgnoreFields(Info{}, "Obj")); diff != "" {
				t.Errorf("NewInfo(_) = (-want,+got):\n%s", diff)
			}
		})
	}
}

var ignoreConditionTimestamps = cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime")

func TestUpdateWorkloadStatus(t *testing.T) {
	cases := map[string]struct {
		oldStatus  kueue.WorkloadStatus
		condType   string
		condStatus metav1.ConditionStatus
		reason     string
		message    string
		wantStatus kueue.WorkloadStatus
	}{
		"initial empty": {
			condType:   kueue.WorkloadAdmitted,
			condStatus: metav1.ConditionFalse,
			reason:     "Pending",
			message:    "didn't fit",
			wantStatus: kueue.WorkloadStatus{
				Conditions: []metav1.Condition{
					{
						Type:    kueue.WorkloadAdmitted,
						Status:  metav1.ConditionFalse,
						Reason:  "Pending",
						Message: "didn't fit",
					},
				},
			},
		},
		"same condition type": {
			oldStatus: kueue.WorkloadStatus{
				Conditions: []metav1.Condition{
					{
						Type:    kueue.WorkloadAdmitted,
						Status:  metav1.ConditionFalse,
						Reason:  "Pending",
						Message: "didn't fit",
					},
				},
			},
			condType:   kueue.WorkloadAdmitted,
			condStatus: metav1.ConditionTrue,
			reason:     "Admitted",
			wantStatus: kueue.WorkloadStatus{
				Conditions: []metav1.Condition{
					{
						Type:   kueue.WorkloadAdmitted,
						Status: metav1.ConditionTrue,
						Reason: "Admitted",
					},
				},
			},
		},
		"different condition type": {
			oldStatus: kueue.WorkloadStatus{
				Conditions: []metav1.Condition{
					{
						Type:   kueue.WorkloadAdmitted,
						Status: metav1.ConditionTrue,
					},
				},
			},
			condType:   kueue.WorkloadFinished,
			condStatus: metav1.ConditionTrue,
			wantStatus: kueue.WorkloadStatus{
				Conditions: []metav1.Condition{
					{
						Type:   kueue.WorkloadAdmitted,
						Status: metav1.ConditionTrue,
					},
					{
						Type:   kueue.WorkloadFinished,
						Status: metav1.ConditionTrue,
					},
				},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := kueue.AddToScheme(scheme); err != nil {
				t.Fatalf("Failed to add kueue scheme: %v", err)
			}
			workload := utiltesting.MakeWorkload("foo", "bar").Obj()
			workload.Status = tc.oldStatus
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(workload).Build()
			ctx := context.Background()
			err := UpdateStatus(ctx, cl, workload, tc.condType, tc.condStatus, tc.reason, tc.message)
			if err != nil {
				t.Fatalf("Failed updating status: %v", err)
			}
			var updatedWl kueue.Workload
			if err := cl.Get(ctx, client.ObjectKeyFromObject(workload), &updatedWl); err != nil {
				t.Fatalf("Failed obtaining updated object: %v", err)
			}
			if diff := cmp.Diff(tc.wantStatus, updatedWl.Status, ignoreConditionTimestamps); diff != "" {
				t.Errorf("Unexpected status after updating (-want,+got):\n%s", diff)
			}
		})
	}
}

func containersForRequests(requests ...map[corev1.ResourceName]string) []corev1.Container {
	containers := make([]corev1.Container, len(requests))
	for i, r := range requests {
		rl := make(corev1.ResourceList, len(r))
		for name, val := range r {
			rl[name] = resource.MustParse(val)
		}
		containers[i].Resources = corev1.ResourceRequirements{
			Requests: rl,
		}
	}
	return containers
}
