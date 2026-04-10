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
