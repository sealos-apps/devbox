package helper

import (
	"context"
	"testing"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveRuntimeMetadataByClass(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	reader := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&nodev1.RuntimeClass{
				ObjectMeta: metav1.ObjectMeta{Name: devboxv1alpha2.RuntimeClassDevboxRunc},
				Handler:    devboxv1alpha2.RuntimeHandlerDevboxRunc,
			},
			&nodev1.RuntimeClass{
				ObjectMeta: metav1.ObjectMeta{Name: devboxv1alpha2.RuntimeClassDevboxStargzRunc},
				Handler:    devboxv1alpha2.RuntimeHandlerDevboxStargzRunc,
			},
		).
		Build()

	tests := []struct {
		name               string
		runtimeClassName   string
		wantRuntimeClass   string
		wantRuntimeHandler string
		wantSnapshotter    string
	}{
		{
			name:               "default empty runtime class uses devbox",
			runtimeClassName:   "",
			wantRuntimeClass:   devboxv1alpha2.RuntimeClassDevboxRunc,
			wantRuntimeHandler: devboxv1alpha2.RuntimeHandlerDevboxRunc,
			wantSnapshotter:    devboxv1alpha2.SnapshotterDevbox,
		},
		{
			name:               "stargz runtime class uses stargz snapshotter",
			runtimeClassName:   devboxv1alpha2.RuntimeClassDevboxStargzRunc,
			wantRuntimeClass:   devboxv1alpha2.RuntimeClassDevboxStargzRunc,
			wantRuntimeHandler: devboxv1alpha2.RuntimeHandlerDevboxStargzRunc,
			wantSnapshotter:    devboxv1alpha2.SnapshotterStargz,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveRuntimeClassName(tt.runtimeClassName); got != tt.wantRuntimeClass {
				t.Fatalf("ResolveRuntimeClassName() = %q, want %q", got, tt.wantRuntimeClass)
			}
			got, err := ResolveRuntimeMetadata(context.Background(), reader, tt.runtimeClassName)
			if err != nil {
				t.Fatalf("ResolveRuntimeMetadata() error = %v", err)
			}
			if got.RuntimeClassName != tt.wantRuntimeClass {
				t.Fatalf("RuntimeClassName = %q, want %q", got.RuntimeClassName, tt.wantRuntimeClass)
			}
			if got.RuntimeHandler != tt.wantRuntimeHandler {
				t.Fatalf("RuntimeHandler = %q, want %q", got.RuntimeHandler, tt.wantRuntimeHandler)
			}
			if got.Snapshotter != tt.wantSnapshotter {
				t.Fatalf("Snapshotter = %q, want %q", got.Snapshotter, tt.wantSnapshotter)
			}
		})
	}
}

func TestEnsureCommitRecordRuntimeMetadata(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	reader := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&nodev1.RuntimeClass{
				ObjectMeta: metav1.ObjectMeta{Name: devboxv1alpha2.RuntimeClassDevboxRunc},
				Handler:    devboxv1alpha2.RuntimeHandlerDevboxRunc,
			},
			&nodev1.RuntimeClass{
				ObjectMeta: metav1.ObjectMeta{Name: devboxv1alpha2.RuntimeClassDevboxStargzRunc},
				Handler:    devboxv1alpha2.RuntimeHandlerDevboxStargzRunc,
			},
		).
		Build()

	t.Run("fills from runtime class when record fields are empty", func(t *testing.T) {
		record := &devboxv1alpha2.CommitRecord{}
		changed, err := EnsureCommitRecordRuntimeMetadata(
			context.Background(),
			reader,
			record,
			devboxv1alpha2.RuntimeClassDevboxStargzRunc,
		)
		if err != nil {
			t.Fatalf("EnsureCommitRecordRuntimeMetadata() error = %v", err)
		}
		if !changed {
			t.Fatalf("EnsureCommitRecordRuntimeMetadata() expected changed=true")
		}
		if record.RuntimeClassName != devboxv1alpha2.RuntimeClassDevboxStargzRunc {
			t.Fatalf("RuntimeClassName = %q", record.RuntimeClassName)
		}
		if record.RuntimeHandler != devboxv1alpha2.RuntimeHandlerDevboxStargzRunc {
			t.Fatalf("RuntimeHandler = %q", record.RuntimeHandler)
		}
		if record.Snapshotter != devboxv1alpha2.SnapshotterStargz {
			t.Fatalf("Snapshotter = %q", record.Snapshotter)
		}
	})

	t.Run("keeps existing record runtime class when default changes", func(t *testing.T) {
		record := &devboxv1alpha2.CommitRecord{
			RuntimeClassName: devboxv1alpha2.RuntimeClassDevboxStargzRunc,
		}
		changed, err := EnsureCommitRecordRuntimeMetadata(
			context.Background(),
			reader,
			record,
			devboxv1alpha2.RuntimeClassDevboxRunc,
		)
		if err != nil {
			t.Fatalf("EnsureCommitRecordRuntimeMetadata() error = %v", err)
		}
		if !changed {
			t.Fatalf("EnsureCommitRecordRuntimeMetadata() expected changed=true")
		}
		if record.RuntimeClassName != devboxv1alpha2.RuntimeClassDevboxStargzRunc {
			t.Fatalf("RuntimeClassName = %q", record.RuntimeClassName)
		}
		if record.RuntimeHandler != devboxv1alpha2.RuntimeHandlerDevboxStargzRunc {
			t.Fatalf("RuntimeHandler = %q", record.RuntimeHandler)
		}
		if record.Snapshotter != devboxv1alpha2.SnapshotterStargz {
			t.Fatalf("Snapshotter = %q", record.Snapshotter)
		}
	})
}

func TestGenerateEnvProfile(t *testing.T) {
	tests := []struct {
		name string
		envs []corev1.EnvVar
		want string
	}{
		{
			name: "no env variables",
			envs: nil,
			want: "# Generated by Sealos Devbox\n" +
				"export DEVBOX_JWT_SECRET=\"test-jwt-secret\"\n",
		},
		{
			name: "multiple env variables",
			envs: []corev1.EnvVar{
				{Name: "FOO", Value: "bar"},
				{Name: "HELLO", Value: "world"},
			},
			want: "# Generated by Sealos Devbox\n" +
				"export FOO=\"bar\"\n" +
				"export HELLO=\"world\"\n" +
				"export DEVBOX_JWT_SECRET=\"test-jwt-secret\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			devbox := &devboxv1alpha2.Devbox{
				Spec: devboxv1alpha2.DevboxSpec{
					Config: devboxv1alpha2.Config{
						Env: tt.envs,
					},
				},
			}
			got := string(GenerateEnvProfile(devbox, []byte("test-jwt-secret")))
			if got != tt.want {
				t.Fatalf("GenerateEnvProfile() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateManagedKubeAccessNames(t *testing.T) {
	devbox := &devboxv1alpha2.Devbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "demo",
		},
	}
	if got, want := GenerateManagedKubeAccessServiceAccountName(devbox), "demo-kubeaccess"; got != want {
		t.Fatalf("GenerateManagedKubeAccessServiceAccountName() = %q, want %q", got, want)
	}
	if got, want := GenerateManagedKubeAccessRoleBindingName(devbox), "demo-kubeaccess"; got != want {
		t.Fatalf("GenerateManagedKubeAccessRoleBindingName() = %q, want %q", got, want)
	}
}

func TestRenderManagedKubeconfig_Defaults(t *testing.T) {
	got := string(RenderManagedKubeconfig("", "", "", ""))
	want := `apiVersion: v1
kind: Config
clusters:
- name: in-cluster
  cluster:
    server: https://kubernetes.default.svc
    certificate-authority: /var/run/sealos/kube-api-access/ca.crt
contexts:
- name: devbox-context
  context:
    cluster: in-cluster
    user: devbox-user
    namespace: default
current-context: devbox-context
users:
- name: devbox-user
  user:
    tokenFile: /var/run/sealos/kube-api-access/token
`
	if got != want {
		t.Fatalf("RenderManagedKubeconfig() = %q, want %q", got, want)
	}
}

func TestRenderManagedKubeconfig_CustomValues(t *testing.T) {
	got := string(RenderManagedKubeconfig("team-a", "https://10.0.0.1:6443", "/ca/custom.crt", "/token/custom"))
	want := `apiVersion: v1
kind: Config
clusters:
- name: in-cluster
  cluster:
    server: https://10.0.0.1:6443
    certificate-authority: /ca/custom.crt
contexts:
- name: devbox-context
  context:
    cluster: in-cluster
    user: devbox-user
    namespace: team-a
current-context: devbox-context
users:
- name: devbox-user
  user:
    tokenFile: /token/custom
`
	if got != want {
		t.Fatalf("RenderManagedKubeconfig() = %q, want %q", got, want)
	}
}

func TestGenerateManagedKubeAccessTokenVolume(t *testing.T) {
	volume := GenerateManagedKubeAccessTokenVolume()
	if volume.Name != ManagedKubeAccessTokenVolumeName {
		t.Fatalf("volume.Name = %q, want %q", volume.Name, ManagedKubeAccessTokenVolumeName)
	}
	if volume.Projected == nil {
		t.Fatalf("volume.Projected = nil")
	}
	if got, want := len(volume.Projected.Sources), 3; got != want {
		t.Fatalf("len(volume.Projected.Sources) = %d, want %d", got, want)
	}

	token := volume.Projected.Sources[0].ServiceAccountToken
	if token == nil {
		t.Fatalf("ServiceAccountTokenProjection = nil")
	}
	if got, want := token.Path, "token"; got != want {
		t.Fatalf("token.Path = %q, want %q", got, want)
	}
	if token.ExpirationSeconds == nil || *token.ExpirationSeconds != ManagedKubeAccessDefaultTokenDuration {
		t.Fatalf("token.ExpirationSeconds = %v, want %d", token.ExpirationSeconds, ManagedKubeAccessDefaultTokenDuration)
	}

	cm := volume.Projected.Sources[1].ConfigMap
	if cm == nil {
		t.Fatalf("ConfigMapProjection = nil")
	}
	if got, want := cm.Name, KubeRootCAConfigMapName; got != want {
		t.Fatalf("cm.Name = %q, want %q", got, want)
	}
	if got, want := len(cm.Items), 1; got != want {
		t.Fatalf("len(cm.Items) = %d, want %d", got, want)
	}
	if got, want := cm.Items[0].Key, KubeRootCAConfigMapKey; got != want {
		t.Fatalf("cm.Items[0].Key = %q, want %q", got, want)
	}
	if got, want := cm.Items[0].Path, "ca.crt"; got != want {
		t.Fatalf("cm.Items[0].Path = %q, want %q", got, want)
	}

	downward := volume.Projected.Sources[2].DownwardAPI
	if downward == nil {
		t.Fatalf("DownwardAPIProjection = nil")
	}
	if got, want := len(downward.Items), 1; got != want {
		t.Fatalf("len(downward.Items) = %d, want %d", got, want)
	}
	if got, want := downward.Items[0].Path, "namespace"; got != want {
		t.Fatalf("downward.Items[0].Path = %q, want %q", got, want)
	}
	if downward.Items[0].FieldRef == nil {
		t.Fatalf("downward.Items[0].FieldRef = nil")
	}
	if got, want := downward.Items[0].FieldRef.FieldPath, "metadata.namespace"; got != want {
		t.Fatalf("FieldRef.FieldPath = %q, want %q", got, want)
	}
}

func TestGenerateManagedKubeAccessTokenVolumeMount(t *testing.T) {
	mounts := GenerateManagedKubeAccessTokenVolumeMount()
	if got, want := len(mounts), 1; got != want {
		t.Fatalf("len(mounts) = %d, want %d", got, want)
	}
	if got, want := mounts[0].Name, ManagedKubeAccessTokenVolumeName; got != want {
		t.Fatalf("mount.Name = %q, want %q", got, want)
	}
	if got, want := mounts[0].MountPath, ManagedKubeAccessTokenMountPath; got != want {
		t.Fatalf("mount.MountPath = %q, want %q", got, want)
	}
	if !mounts[0].ReadOnly {
		t.Fatalf("mount.ReadOnly = false, want true")
	}
}

func TestGenerateManagedKubeconfigVolume(t *testing.T) {
	devbox := &devboxv1alpha2.Devbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "demo",
		},
	}
	volume := GenerateManagedKubeconfigVolume(devbox)
	if got, want := volume.Name, ManagedKubeconfigVolumeName; got != want {
		t.Fatalf("volume.Name = %q, want %q", got, want)
	}
	if volume.Secret == nil {
		t.Fatalf("volume.Secret = nil")
	}
	if got, want := volume.Secret.SecretName, "demo"; got != want {
		t.Fatalf("SecretName = %q, want %q", got, want)
	}
	if got, want := len(volume.Secret.Items), 1; got != want {
		t.Fatalf("len(Secret.Items) = %d, want %d", got, want)
	}
	if got, want := volume.Secret.Items[0].Key, ManagedKubeconfigSecretKey; got != want {
		t.Fatalf("Secret.Items[0].Key = %q, want %q", got, want)
	}
	if got, want := volume.Secret.Items[0].Path, "config"; got != want {
		t.Fatalf("Secret.Items[0].Path = %q, want %q", got, want)
	}
}

func TestGenerateManagedKubeconfigVolumeMount(t *testing.T) {
	mounts := GenerateManagedKubeconfigVolumeMount()
	if got, want := len(mounts), 1; got != want {
		t.Fatalf("len(mounts) = %d, want %d", got, want)
	}
	mount := mounts[0]
	if got, want := mount.Name, ManagedKubeconfigVolumeName; got != want {
		t.Fatalf("mount.Name = %q, want %q", got, want)
	}
	if got, want := mount.MountPath, ManagedKubeconfigMountPath; got != want {
		t.Fatalf("mount.MountPath = %q, want %q", got, want)
	}
	if got, want := mount.SubPath, "config"; got != want {
		t.Fatalf("mount.SubPath = %q, want %q", got, want)
	}
	if !mount.ReadOnly {
		t.Fatalf("mount.ReadOnly = false, want true")
	}
}

func TestGenerateManagedKubeconfigEnvVar(t *testing.T) {
	env := GenerateManagedKubeconfigEnvVar()
	if got, want := env.Name, ManagedKubeconfigEnvName; got != want {
		t.Fatalf("env.Name = %q, want %q", got, want)
	}
	if got, want := env.Value, ManagedKubeconfigMountPath; got != want {
		t.Fatalf("env.Value = %q, want %q", got, want)
	}
}

func TestWithPodServiceAccountName(t *testing.T) {
	pod := &corev1.Pod{}
	WithPodServiceAccountName(" demo-sa ")(pod)
	if got, want := pod.Spec.ServiceAccountName, "demo-sa"; got != want {
		t.Fatalf("pod.Spec.ServiceAccountName = %q, want %q", got, want)
	}
}

func TestWithPodKubeconfigEnv(t *testing.T) {
	t.Run("append env when missing", func(t *testing.T) {
		pod := &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{},
				},
			},
		}
		WithPodKubeconfigEnv()(pod)
		if got, want := len(pod.Spec.Containers[0].Env), 1; got != want {
			t.Fatalf("len(env) = %d, want %d", got, want)
		}
		if got, want := pod.Spec.Containers[0].Env[0].Name, ManagedKubeconfigEnvName; got != want {
			t.Fatalf("env[0].Name = %q, want %q", got, want)
		}
		if got, want := pod.Spec.Containers[0].Env[0].Value, ManagedKubeconfigMountPath; got != want {
			t.Fatalf("env[0].Value = %q, want %q", got, want)
		}
	})

	t.Run("replace existing env", func(t *testing.T) {
		pod := &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: []corev1.EnvVar{
							{
								Name:  ManagedKubeconfigEnvName,
								Value: "/tmp/other",
							},
						},
					},
				},
			},
		}
		WithPodKubeconfigEnv()(pod)
		if got, want := len(pod.Spec.Containers[0].Env), 1; got != want {
			t.Fatalf("len(env) = %d, want %d", got, want)
		}
		if got, want := pod.Spec.Containers[0].Env[0].Value, ManagedKubeconfigMountPath; got != want {
			t.Fatalf("env[0].Value = %q, want %q", got, want)
		}
	})
}
