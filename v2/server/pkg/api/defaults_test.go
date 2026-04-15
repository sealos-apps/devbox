package api

import (
	"testing"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestDefaultCreateDevboxSpec(t *testing.T) {
	spec := defaultCreateDevboxSpec(CreateDevboxResourceConfig{
		CPU:          "2000m",
		Memory:       "4096Mi",
		StorageLimit: "20Gi",
		Image:        "registry.example.com/devbox/runtime:stable",
	})

	if spec.State != devboxv1alpha2.DevboxStateRunning {
		t.Fatalf("unexpected state: %s", spec.State)
	}
	if spec.NetworkSpec.Type != devboxv1alpha2.NetworkTypeSSHGate {
		t.Fatalf("unexpected network type: %s", spec.NetworkSpec.Type)
	}
	cpuQuantity := spec.Resource[corev1.ResourceCPU]
	if cpuQuantity.Cmp(resource.MustParse("2000m")) != 0 {
		t.Fatalf("unexpected cpu quantity: %s", cpuQuantity.String())
	}
	memoryQuantity := spec.Resource[corev1.ResourceMemory]
	if memoryQuantity.Cmp(resource.MustParse("4096Mi")) != 0 {
		t.Fatalf("unexpected memory quantity: %s", memoryQuantity.String())
	}
	if spec.TemplateID != "aa117587-7c09-4fab-bee4-97b833d55981" {
		t.Fatalf("unexpected templateID: %s", spec.TemplateID)
	}
	if spec.RuntimeClassName != "devbox-runtime" {
		t.Fatalf("unexpected runtimeClassName: %s", spec.RuntimeClassName)
	}
	if spec.Image != "registry.example.com/devbox/runtime:stable" {
		t.Fatalf("unexpected image: %s", spec.Image)
	}
	if spec.StorageLimit != "20Gi" {
		t.Fatalf("unexpected storageLimit: %s", spec.StorageLimit)
	}
	if len(spec.Config.AppPorts) != 1 || spec.Config.AppPorts[0].Name != "app-port" {
		t.Fatalf("unexpected app ports: %+v", spec.Config.AppPorts)
	}
	if len(spec.Config.ReleaseArgs) != 1 || spec.Config.ReleaseArgs[0] != "/home/devbox/workspace/entrypoint.sh prod" {
		t.Fatalf("unexpected releaseArgs: %+v", spec.Config.ReleaseArgs)
	}
}
