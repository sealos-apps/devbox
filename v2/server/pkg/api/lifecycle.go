package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	lifecycleMinWait = time.Second
)

type lifecycleSignal struct {
	full bool
	key  ctrlclient.ObjectKey
}

func (s *apiServer) startLifecycleRunner(ctx context.Context) {
	if s == nil || s.ctrlClient == nil || s.kubeClient == nil {
		return
	}
	go s.runLifecycleLeaderElector(ctx)
}

func (s *apiServer) lifecycleReaderClient() ctrlclient.Reader {
	if s == nil {
		return nil
	}
	if s.lifecycleReader != nil {
		return s.lifecycleReader
	}
	return s.ctrlClient
}

func (s *apiServer) lifecycleResyncInterval() time.Duration {
	if s == nil || s.cfg.LifecycleResyncInterval <= 0 {
		return defaultLifecycleResync
	}
	return s.cfg.LifecycleResyncInterval
}

func (s *apiServer) notifyLifecycleRunner() {
	if s == nil || s.lifecycleNotifyCh == nil {
		return
	}
	select {
	case s.lifecycleNotifyCh <- lifecycleSignal{full: true}:
	default:
	}
}

func (s *apiServer) notifyLifecycleRunnerForKey(namespace string, name string) {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if namespace == "" || name == "" {
		s.notifyLifecycleRunner()
		return
	}
	if s == nil || s.lifecycleNotifyCh == nil {
		return
	}
	select {
	case s.lifecycleNotifyCh <- lifecycleSignal{
		key: ctrlclient.ObjectKey{Namespace: namespace, Name: name},
	}:
	default:
	}
}

func (s *apiServer) runLifecycleRunner(ctx context.Context) {
	resyncInterval := s.lifecycleResyncInterval()
	s.logInfo("lifecycle runner started", "resync_interval", resyncInterval.String())

	nextByKey, err := s.reconcileLifecycleFull(ctx)
	if err != nil {
		s.logError("lifecycle initial full reconcile failed", err)
		nextByKey = map[ctrlclient.ObjectKey]time.Time{}
	}

	timer := time.NewTimer(s.nextLifecycleWait(time.Now().UTC(), nextByKey, resyncInterval))
	defer timer.Stop()
	resyncTicker := time.NewTicker(resyncInterval)
	defer resyncTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logInfo("lifecycle runner stopped")
			return
		case signal := <-s.lifecycleNotifyCh:
			if signal.full {
				fullNextByKey, fullErr := s.reconcileLifecycleFull(ctx)
				if fullErr != nil {
					s.logError("lifecycle full reconcile failed", fullErr)
				} else {
					nextByKey = fullNextByKey
				}
			} else if keyErr := s.reconcileLifecycleByKey(ctx, signal.key, nextByKey); keyErr != nil {
				s.logError("lifecycle key reconcile failed", keyErr, "namespace", signal.key.Namespace, "name", signal.key.Name)
			}
		case <-timer.C:
			s.reconcileDueLifecycle(ctx, time.Now().UTC(), nextByKey)
		case <-resyncTicker.C:
			fullNextByKey, fullErr := s.reconcileLifecycleFull(ctx)
			if fullErr != nil {
				s.logError("lifecycle periodic full reconcile failed", fullErr)
			} else {
				nextByKey = fullNextByKey
			}
		}

		resetLifecycleTimer(timer, s.nextLifecycleWait(time.Now().UTC(), nextByKey, resyncInterval))
	}
}

func resetLifecycleTimer(timer *time.Timer, wait time.Duration) {
	if timer == nil {
		return
	}
	if wait < lifecycleMinWait {
		wait = lifecycleMinWait
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(wait)
}

func (s *apiServer) nextLifecycleWait(now time.Time, nextByKey map[ctrlclient.ObjectKey]time.Time, resyncInterval time.Duration) time.Duration {
	if resyncInterval <= 0 {
		resyncInterval = defaultLifecycleResync
	}
	if len(nextByKey) == 0 {
		return resyncInterval
	}
	var nextAt time.Time
	for _, candidate := range nextByKey {
		if candidate.IsZero() {
			continue
		}
		if nextAt.IsZero() || candidate.Before(nextAt) {
			nextAt = candidate
		}
	}
	if nextAt.IsZero() {
		return resyncInterval
	}
	if !nextAt.After(now) {
		return lifecycleMinWait
	}
	wait := time.Until(nextAt)
	if wait > resyncInterval {
		return resyncInterval
	}
	return wait
}

func (s *apiServer) reconcileDueLifecycle(ctx context.Context, now time.Time, nextByKey map[ctrlclient.ObjectKey]time.Time) {
	due := make([]ctrlclient.ObjectKey, 0, len(nextByKey))
	for key, nextAt := range nextByKey {
		if !nextAt.After(now) {
			due = append(due, key)
		}
	}
	for _, key := range due {
		if err := s.reconcileLifecycleByKey(ctx, key, nextByKey); err != nil {
			s.logError("lifecycle due reconcile failed", err, "namespace", key.Namespace, "name", key.Name)
		}
	}
}

func (s *apiServer) reconcileLifecycleFull(ctx context.Context) (map[ctrlclient.ObjectKey]time.Time, error) {
	reader := s.lifecycleReaderClient()
	if reader == nil {
		return map[ctrlclient.ObjectKey]time.Time{}, nil
	}
	devboxList := &devboxv1alpha2.DevboxList{}
	if err := reader.List(ctx, devboxList, ctrlclient.MatchingLabels{
		devboxLifecycleLabelKey: "true",
	}); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	nextByKey := make(map[ctrlclient.ObjectKey]time.Time, len(devboxList.Items))
	for i := range devboxList.Items {
		devbox := &devboxList.Items[i]
		nextAt, err := s.reconcileDevboxLifecycle(ctx, now, devbox)
		if err != nil {
			s.logError("reconcile devbox lifecycle failed", err, "namespace", devbox.Namespace, "name", devbox.Name)
			continue
		}
		if nextAt.IsZero() {
			continue
		}
		nextByKey[ctrlclient.ObjectKeyFromObject(devbox)] = nextAt
	}
	return nextByKey, nil
}

func (s *apiServer) reconcileLifecycleByKey(ctx context.Context, key ctrlclient.ObjectKey, nextByKey map[ctrlclient.ObjectKey]time.Time) error {
	if key.Namespace == "" || key.Name == "" {
		return nil
	}
	if nextByKey == nil {
		return nil
	}

	reader := s.lifecycleReaderClient()
	if reader == nil {
		delete(nextByKey, key)
		return nil
	}
	devbox := &devboxv1alpha2.Devbox{}
	if err := reader.Get(ctx, key, devbox); err != nil {
		if apierrors.IsNotFound(err) {
			delete(nextByKey, key)
			return nil
		}
		return err
	}

	nextAt, err := s.reconcileDevboxLifecycle(ctx, time.Now().UTC(), devbox)
	if err != nil {
		return err
	}
	if nextAt.IsZero() {
		delete(nextByKey, key)
		return nil
	}
	nextByKey[key] = nextAt
	return nil
}

func (s *apiServer) reconcileLifecycle(ctx context.Context) (map[ctrlclient.ObjectKey]time.Time, error) {
	return s.reconcileLifecycleFull(ctx)
}

func (s *apiServer) reconcileDevboxLifecycle(ctx context.Context, now time.Time, devbox *devboxv1alpha2.Devbox) (time.Time, error) {
	if devbox == nil {
		return time.Time{}, nil
	}
	if devbox.DeletionTimestamp != nil && !devbox.DeletionTimestamp.IsZero() {
		return time.Time{}, nil
	}

	annotations := devbox.GetAnnotations()
	pauseAt, hasPauseAt, err := parseLifecycleTime(annotations[devboxAnnotationPauseAt])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s failed: %w", devboxAnnotationPauseAt, err)
	}
	archiveAfterPause, hasArchiveAfterPause, err := parseArchiveAfterPauseDuration(annotations[devboxAnnotationArchiveAfterPauseTime])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s failed: %w", devboxAnnotationArchiveAfterPauseTime, err)
	}

	if !hasPauseAt && !hasArchiveAfterPause {
		if err := s.patchDevboxMetadata(ctx, ctrlclient.ObjectKeyFromObject(devbox), func(latest *devboxv1alpha2.Devbox) bool {
			labels := latest.GetLabels()
			if len(labels) == 0 {
				return false
			}
			if _, ok := labels[devboxLifecycleLabelKey]; !ok {
				return false
			}
			delete(labels, devboxLifecycleLabelKey)
			latest.SetLabels(labels)
			return true
		}); err != nil {
			if !apierrors.IsNotFound(err) {
				return time.Time{}, err
			}
		}
		return time.Time{}, nil
	}

	if devbox.Spec.State == devboxv1alpha2.DevboxStateShutdown {
		return time.Time{}, nil
	}

	if devbox.Spec.State == devboxv1alpha2.DevboxStateRunning && hasPauseAt {
		if !pauseAt.After(now) {
			if err := s.patchDevboxMetadataAndState(
				ctx,
				ctrlclient.ObjectKeyFromObject(devbox),
				devboxv1alpha2.DevboxStateRunning,
				devboxv1alpha2.DevboxStatePaused,
				func(latest *devboxv1alpha2.Devbox) bool {
					annotations := latest.GetAnnotations()
					if annotations == nil {
						annotations = make(map[string]string, 2)
					}
					changed := false
					if _, ok := annotations[devboxAnnotationPauseAt]; ok {
						delete(annotations, devboxAnnotationPauseAt)
						changed = true
					}
					if _, ok := annotations[devboxAnnotationPausedAt]; !ok {
						annotations[devboxAnnotationPausedAt] = now.UTC().Format(time.RFC3339)
						changed = true
					}
					if changed {
						latest.SetAnnotations(annotations)
					}
					return changed
				},
			); err != nil {
				return time.Time{}, err
			}
			if hasArchiveAfterPause {
				return now.Add(archiveAfterPause), nil
			}
			return time.Time{}, nil
		}
		return pauseAt, nil
	}

	if devbox.Spec.State == devboxv1alpha2.DevboxStatePaused {
		pausedAt, hasPausedAt, err := parseLifecycleTime(annotations[devboxAnnotationPausedAt])
		if err != nil {
			return time.Time{}, fmt.Errorf("parse %s failed: %w", devboxAnnotationPausedAt, err)
		}
		if !hasPausedAt {
			if err := s.patchDevboxMetadata(ctx, ctrlclient.ObjectKeyFromObject(devbox), func(latest *devboxv1alpha2.Devbox) bool {
				annotations := latest.GetAnnotations()
				if annotations == nil {
					annotations = make(map[string]string, 1)
				}
				if _, ok := annotations[devboxAnnotationPausedAt]; ok {
					return false
				}
				annotations[devboxAnnotationPausedAt] = now.UTC().Format(time.RFC3339)
				latest.SetAnnotations(annotations)
				return true
			}); err != nil {
				return time.Time{}, err
			}
			pausedAt = now
		}
		if hasArchiveAfterPause {
			archiveAt := pausedAt.UTC().Add(archiveAfterPause)
			if !archiveAt.After(now) {
				if err := s.patchDevboxMetadataAndState(
					ctx,
					ctrlclient.ObjectKeyFromObject(devbox),
					devboxv1alpha2.DevboxStatePaused,
					devboxv1alpha2.DevboxStateShutdown,
					func(latest *devboxv1alpha2.Devbox) bool {
						annotations := latest.GetAnnotations()
						if annotations == nil {
							annotations = make(map[string]string, 1)
						}
						if _, ok := annotations[devboxAnnotationArchiveTriggeredAt]; ok {
							return false
						}
						annotations[devboxAnnotationArchiveTriggeredAt] = now.UTC().Format(time.RFC3339)
						latest.SetAnnotations(annotations)
						return true
					},
				); err != nil {
					return time.Time{}, err
				}
				return time.Time{}, nil
			}
			return archiveAt, nil
		}
	}

	return time.Time{}, nil
}

func (s *apiServer) patchDevboxMetadata(
	ctx context.Context,
	key ctrlclient.ObjectKey,
	mutate func(*devboxv1alpha2.Devbox) bool,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &devboxv1alpha2.Devbox{}
		if err := s.ctrlClient.Get(ctx, key, latest); err != nil {
			return err
		}
		changed := mutate(latest)
		if !changed {
			return nil
		}
		return s.ctrlClient.Update(ctx, latest)
	})
}

func (s *apiServer) patchDevboxMetadataAndState(
	ctx context.Context,
	key ctrlclient.ObjectKey,
	expected devboxv1alpha2.DevboxState,
	target devboxv1alpha2.DevboxState,
	mutateMetadata func(*devboxv1alpha2.Devbox) bool,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &devboxv1alpha2.Devbox{}
		if err := s.ctrlClient.Get(ctx, key, latest); err != nil {
			return err
		}
		changed := false
		if latest.Spec.State == expected && latest.Spec.State != target {
			latest.Spec.State = target
			changed = true
		}
		if mutateMetadata != nil && mutateMetadata(latest) {
			changed = true
		}
		if !changed {
			return nil
		}
		return s.ctrlClient.Update(ctx, latest)
	})
}
