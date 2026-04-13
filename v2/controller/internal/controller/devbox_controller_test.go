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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	"github.com/sealos-apps/devbox/v2/controller/internal/controller/helper"
	"github.com/sealos-apps/devbox/v2/controller/internal/controller/utils/resource"
	"github.com/sealos-apps/devbox/v2/controller/internal/stat"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type stubNodeStatsProvider struct{}

func (stubNodeStatsProvider) ContainerFsStats(context.Context) (stat.FsStats, error) {
	available := uint64(100 << 30)
	capacity := uint64(200 << 30)
	return stat.FsStats{
		AvailableBytes: &available,
		CapacityBytes:  &capacity,
	}, nil
}

var _ = Describe("Devbox Controller", func() {
	const (
		namespace = "default"
		nodeName  = "test-node"
	)

	ctx := context.Background()

	buildReconciler := func() *DevboxReconciler {
		return &DevboxReconciler{
			Client:              k8sClient,
			Scheme:              k8sClient.Scheme(),
			Recorder:            record.NewFakeRecorder(32),
			StateChangeRecorder: record.NewFakeRecorder(32),
			NodeName:            nodeName,
			AcceptanceThreshold: 0,
			RequestRate: resource.RequestRate{
				CPU:    10,
				Memory: 10,
			},
			EphemeralStorage: resource.EphemeralStorage{
				DefaultRequest: apiresource.MustParse("500Mi"),
				DefaultLimit:   apiresource.MustParse("10Gi"),
				MaximumLimit:   apiresource.MustParse("50Gi"),
			},
			NodeStatsProvider: stubNodeStatsProvider{},
		}
	}

	createNode := func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Annotations: map[string]string{
					devboxv1alpha2.AnnotationContainerFSAvailableThreshold: "10",
					devboxv1alpha2.AnnotationCPURequestRatio:               "100",
					devboxv1alpha2.AnnotationCPULimitRatio:                 "100",
					devboxv1alpha2.AnnotationMemoryRequestRatio:            "100",
					devboxv1alpha2.AnnotationMemoryLimitRatio:              "100",
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    apiresource.MustParse("16"),
					corev1.ResourceMemory: apiresource.MustParse("64Gi"),
				},
			},
		}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, node))).To(Succeed())
	}

	BeforeEach(func() {
		createNode()
	})

	AfterEach(func() {
	})

	newDevbox := func(name string, kubeAccess *devboxv1alpha2.KubeAccessSpec) *devboxv1alpha2.Devbox {
		return &devboxv1alpha2.Devbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: devboxv1alpha2.DevboxSpec{
				State: devboxv1alpha2.DevboxStateRunning,
				Resource: corev1.ResourceList{
					corev1.ResourceCPU:    apiresource.MustParse("1"),
					corev1.ResourceMemory: apiresource.MustParse("1Gi"),
				},
				Image: "busybox:latest",
				Config: devboxv1alpha2.Config{
					Command: []string{"/bin/sh", "-c"},
					Args:    []string{"sleep 3600"},
				},
				NetworkSpec: devboxv1alpha2.NetworkSpec{Type: devboxv1alpha2.NetworkTypeTailnet},
				KubeAccess:  kubeAccess,
			},
		}
	}

	reconcileEventually := func(reconciler *DevboxReconciler, key client.ObjectKey) {
		Eventually(func() error {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			return err
		}, 5*time.Second, 200*time.Millisecond).Should(Succeed())
	}

	reconcileAndAssert := func(
		reconciler *DevboxReconciler,
		key client.ObjectKey,
		assertFn func(g Gomega),
	) {
		Eventually(func(g Gomega) {
			for i := 0; i < 6; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
				g.Expect(err).NotTo(HaveOccurred())
			}
			assertFn(g)
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())
	}

	It("creates managed kube access resources and pod wiring when enabled", func() {
		resourceName := fmt.Sprintf("test-resource-enabled-%s", rand.String(5))
		typeNamespacedName := client.ObjectKey{Name: resourceName, Namespace: namespace}
		devbox := newDevbox(resourceName, &devboxv1alpha2.KubeAccessSpec{
			Enabled:      true,
			RoleTemplate: devboxv1alpha2.KubeAccessRoleTemplateEdit,
		})
		Expect(k8sClient.Create(ctx, devbox)).To(Succeed())

		reconciler := buildReconciler()
		reconcileEventually(reconciler, typeNamespacedName)
		reconcileEventually(reconciler, typeNamespacedName)
		reconcileAndAssert(reconciler, typeNamespacedName, func(g Gomega) {
			sa := &corev1.ServiceAccount{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      helper.GenerateManagedKubeAccessServiceAccountName(devbox),
				Namespace: namespace,
			}, sa)).To(Succeed())

			role := &rbacv1.Role{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      helper.GenerateManagedKubeAccessRoleBindingName(devbox),
				Namespace: namespace,
			}, role)).To(Succeed())
			g.Expect(role.Rules).NotTo(BeEmpty())
			g.Expect(role.Rules[0].APIGroups).To(ContainElement("*"))
			g.Expect(role.Rules[0].Resources).To(ContainElement("*"))
			g.Expect(role.Rules[0].Verbs).To(ContainElement("create"))

			roleBinding := &rbacv1.RoleBinding{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      helper.GenerateManagedKubeAccessRoleBindingName(devbox),
				Namespace: namespace,
			}, roleBinding)).To(Succeed())
			g.Expect(roleBinding.RoleRef.Kind).To(Equal("Role"))
			g.Expect(roleBinding.RoleRef.Name).To(Equal(helper.GenerateManagedKubeAccessRoleBindingName(devbox)))
			g.Expect(roleBinding.Subjects).To(HaveLen(1))
			g.Expect(roleBinding.Subjects[0].Name).To(Equal(helper.GenerateManagedKubeAccessServiceAccountName(devbox)))

			secret := &corev1.Secret{}
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, secret)).To(Succeed())
			g.Expect(secret.Data).To(HaveKey(helper.ManagedKubeconfigSecretKey))

			pod := &corev1.Pod{}
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, pod)).To(Succeed())
			g.Expect(pod.Spec.ServiceAccountName).To(Equal(helper.GenerateManagedKubeAccessServiceAccountName(devbox)))
			g.Expect(pod.Spec.AutomountServiceAccountToken).NotTo(BeNil())
			g.Expect(*pod.Spec.AutomountServiceAccountToken).To(BeFalse())
			g.Expect(pod.Spec.Volumes).To(ContainElement(HaveField("Name", helper.ManagedKubeAccessTokenVolumeName)))
			g.Expect(pod.Spec.Volumes).To(ContainElement(HaveField("Name", helper.ManagedKubeconfigVolumeName)))
			g.Expect(pod.Spec.Containers).To(HaveLen(1))
			g.Expect(pod.Spec.Containers[0].Env).To(ContainElement(helper.GenerateManagedKubeconfigEnvVar()))
		})
	})

	It("does not create managed kube access resources when disabled", func() {
		resourceName := fmt.Sprintf("test-resource-disabled-%s", rand.String(5))
		typeNamespacedName := client.ObjectKey{Name: resourceName, Namespace: namespace}
		devbox := newDevbox(resourceName, nil)
		Expect(k8sClient.Create(ctx, devbox)).To(Succeed())

		reconciler := buildReconciler()
		reconcileEventually(reconciler, typeNamespacedName)
		reconcileEventually(reconciler, typeNamespacedName)
		reconcileAndAssert(reconciler, typeNamespacedName, func(g Gomega) {
			sa := &corev1.ServiceAccount{}
			err := k8sClient.Get(ctx, client.ObjectKey{
				Name:      helper.GenerateManagedKubeAccessServiceAccountName(devbox),
				Namespace: namespace,
			}, sa)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

			roleBinding := &rbacv1.RoleBinding{}
			err = k8sClient.Get(ctx, client.ObjectKey{
				Name:      helper.GenerateManagedKubeAccessRoleBindingName(devbox),
				Namespace: namespace,
			}, roleBinding)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

			role := &rbacv1.Role{}
			err = k8sClient.Get(ctx, client.ObjectKey{
				Name:      helper.GenerateManagedKubeAccessRoleBindingName(devbox),
				Namespace: namespace,
			}, role)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

			secret := &corev1.Secret{}
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, secret)).To(Succeed())
			g.Expect(secret.Data).NotTo(HaveKey(helper.ManagedKubeconfigSecretKey))

			pod := &corev1.Pod{}
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, pod)).To(Succeed())
			g.Expect(pod.Spec.ServiceAccountName).To(BeEmpty())
		})
	})
})
