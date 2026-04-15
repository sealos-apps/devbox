package api

import (
	"context"
	"testing"
	"time"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestReconcileLifecyclePauseRunningDevbox(t *testing.T) {
	srv := newTestAPIServer(
		t,
		&devboxv1alpha2.Devbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "db-pause",
				Namespace: "ns-test",
				Labels: map[string]string{
					devboxLifecycleLabelKey: "true",
				},
				Annotations: map[string]string{
					devboxAnnotationPauseAt:               time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
					devboxAnnotationArchiveAfterPauseTime: "1h0m0s",
				},
			},
			Spec: devboxv1alpha2.DevboxSpec{
				State: devboxv1alpha2.DevboxStateRunning,
			},
		},
	)

	if _, err := srv.reconcileLifecycle(context.Background()); err != nil {
		t.Fatalf("reconcile lifecycle failed: %v", err)
	}

	latest := &devboxv1alpha2.Devbox{}
	if err := srv.ctrlClient.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "ns-test", Name: "db-pause"}, latest); err != nil {
		t.Fatalf("get devbox failed: %v", err)
	}
	if latest.Spec.State != devboxv1alpha2.DevboxStatePaused {
		t.Fatalf("expected state Paused, got %s", latest.Spec.State)
	}
	if got := latest.Annotations[devboxAnnotationPausedAt]; got == "" {
		t.Fatalf("expected pausedAt annotation to be set")
	}
}

func TestReconcileLifecycleArchivePausedDevbox(t *testing.T) {
	pausedAt := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	srv := newTestAPIServer(
		t,
		&devboxv1alpha2.Devbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "db-archive",
				Namespace: "ns-test",
				Labels: map[string]string{
					devboxLifecycleLabelKey: "true",
				},
				Annotations: map[string]string{
					devboxAnnotationPausedAt:              pausedAt,
					devboxAnnotationArchiveAfterPauseTime: "1h0m0s",
				},
			},
			Spec: devboxv1alpha2.DevboxSpec{
				State: devboxv1alpha2.DevboxStatePaused,
			},
		},
	)

	if _, err := srv.reconcileLifecycle(context.Background()); err != nil {
		t.Fatalf("reconcile lifecycle failed: %v", err)
	}

	latest := &devboxv1alpha2.Devbox{}
	if err := srv.ctrlClient.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "ns-test", Name: "db-archive"}, latest); err != nil {
		t.Fatalf("get devbox failed: %v", err)
	}
	if latest.Spec.State != devboxv1alpha2.DevboxStateShutdown {
		t.Fatalf("expected state Shutdown, got %s", latest.Spec.State)
	}
	if got := latest.Annotations[devboxAnnotationArchiveTriggeredAt]; got == "" {
		t.Fatalf("expected archiveTriggeredAt annotation to be set")
	}
}

func TestReconcileLifecycleCleanupLabelWithoutSchedule(t *testing.T) {
	srv := newTestAPIServer(
		t,
		&devboxv1alpha2.Devbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "db-cleanup",
				Namespace: "ns-test",
				Labels: map[string]string{
					devboxLifecycleLabelKey: "true",
				},
			},
			Spec: devboxv1alpha2.DevboxSpec{
				State: devboxv1alpha2.DevboxStateRunning,
			},
		},
	)

	if _, err := srv.reconcileLifecycle(context.Background()); err != nil {
		t.Fatalf("reconcile lifecycle failed: %v", err)
	}

	latest := &devboxv1alpha2.Devbox{}
	if err := srv.ctrlClient.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "ns-test", Name: "db-cleanup"}, latest); err != nil {
		t.Fatalf("get devbox failed: %v", err)
	}
	if _, ok := latest.Labels[devboxLifecycleLabelKey]; ok {
		t.Fatalf("expected lifecycle label to be removed")
	}
}
