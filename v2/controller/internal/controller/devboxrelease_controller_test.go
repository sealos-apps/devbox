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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	devboxv1alpha2 "github.com/sealos-apps/sealos-devbox/v2/controller/api/v1alpha2"
)

var _ = Describe("DevBoxRelease Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		devboxrelease := &devboxv1alpha2.DevBoxRelease{}

		BeforeEach(func() {
			By("creating the referenced Devbox")
			devboxKey := types.NamespacedName{Name: "test-devbox", Namespace: "default"}
			devbox := &devboxv1alpha2.Devbox{}
			err := k8sClient.Get(ctx, devboxKey, devbox)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &devboxv1alpha2.Devbox{
					ObjectMeta: metav1.ObjectMeta{
						Name:      devboxKey.Name,
						Namespace: devboxKey.Namespace,
					},
					Spec: devboxv1alpha2.DevboxSpec{
						State: devboxv1alpha2.DevboxStateStopped,
						Resource: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Image:       "busybox:latest",
						Config:      devboxv1alpha2.Config{},
						NetworkSpec: devboxv1alpha2.NetworkSpec{Type: devboxv1alpha2.NetworkTypeTailnet},
					},
				})).To(Succeed())

				Expect(k8sClient.Get(ctx, devboxKey, devbox)).To(Succeed())
				devbox.Status = devboxv1alpha2.DevboxStatus{
					ContentID: "content-1",
					CommitRecords: devboxv1alpha2.CommitRecordMap{
						"content-1": {
							BaseImage:    "busybox:latest",
							CommitStatus: devboxv1alpha2.CommitStatusSuccess,
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, devbox)).To(Succeed())
			}

			By("creating the custom resource for the Kind DevBoxRelease")
			err = k8sClient.Get(ctx, typeNamespacedName, devboxrelease)
			if err != nil && errors.IsNotFound(err) {
				resource := &devboxv1alpha2.DevBoxRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: devboxv1alpha2.DevBoxReleaseSpec{
						DevboxName: "test-devbox",
						Version:    "v1.0.0",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &devboxv1alpha2.DevBoxRelease{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance DevBoxRelease")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			devbox := &devboxv1alpha2.Devbox{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "test-devbox", Namespace: "default"}, devbox)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the referenced Devbox")
			Expect(k8sClient.Delete(ctx, devbox)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &DevBoxReleaseReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
