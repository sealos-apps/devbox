package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	"github.com/sealos-apps/devbox/v2/controller/internal/commit"
	"github.com/sealos-apps/devbox/v2/controller/internal/controller/helper"
	"github.com/sealos-apps/devbox/v2/controller/internal/controller/utils/events"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	commitMap = sync.Map{}
	deleteMap = sync.Map{}
)

type EventHandler struct {
	Committers          map[string]commit.Committer
	CommitImageRegistry string
	NodeName            string
	DefaultBaseImage    string

	Logger   logr.Logger
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (h *EventHandler) getCommitterBySnapshotter(snapshotter string) (commit.Committer, error) {
	selected := strings.TrimSpace(snapshotter)
	if selected == "" {
		selected = commit.DefaultDevboxSnapshotter
	}
	committer, ok := h.Committers[selected]
	if ok && committer != nil {
		return committer, nil
	}
	if fallback, ok := h.Committers[commit.DefaultDevboxSnapshotter]; ok && fallback != nil {
		h.Logger.Info(
			"snapshotter committer not found, fallback to devbox snapshotter",
			"requestedSnapshotter",
			selected,
			"fallbackSnapshotter",
			commit.DefaultDevboxSnapshotter,
		)
		return fallback, nil
	}
	return nil, fmt.Errorf("committer for snapshotter %q not found", selected)
}

// todo: handle state change event
func (h *EventHandler) Handle(ctx context.Context, event *corev1.Event) error {
	h.Logger.Info("StateChangeHandler.Handle called",
		"event", event.Name,
		"eventSourceHost", event.Source.Host,
		"handlerNodeName", h.NodeName,
		"eventType", event.Type,
		"eventReason", event.Reason,
		"eventMessage", event.Message)

	if event.Source.Host != h.NodeName {
		h.Logger.Info("event source host is not the node name, skip", "event", event)
		return nil
	}

	switch event.Reason {
	// handle storage cleanup
	case events.ReasonStorageCleanupRequested:
		return h.handleStorageCleanup(ctx, event)

	// handle state change
	case events.ReasonDevboxStateChanged:
		return h.handleDevboxStateChange(ctx, event)

	default:
		return errors.New("invalid event")
	}
}

// handleDevboxStateChange handle new structured state change event
func (h *EventHandler) handleDevboxStateChange(ctx context.Context, event *corev1.Event) error {
	h.Logger.Info(
		"Devbox state change event detected",
		"event",
		event.Name,
		"message",
		event.Message,
	)
	devbox := &devboxv1alpha2.Devbox{}
	if err := h.Client.Get(
		ctx,
		types.NamespacedName{Namespace: event.Namespace, Name: event.InvolvedObject.Name},
		devbox,
	); err != nil {
		h.Logger.Error(err, "failed to get devbox", "devbox", event.InvolvedObject.Name)
		return err
	}

	// Check if state transition is valid and handle accordingly
	currentState := devbox.Status.State
	targetState := devbox.Spec.State

	// Handle invalid state transition
	if currentState == devboxv1alpha2.DevboxStateShutdown &&
		targetState == devboxv1alpha2.DevboxStateStopped {
		h.Recorder.Eventf(
			devbox,
			corev1.EventTypeWarning,
			"Shutdown state is not allowed to be changed to stopped state",
			"Shutdown state is not allowed to be changed to stopped state",
		)
		h.Logger.Error(
			errors.New("shutdown state is not allowed to be changed to stopped state"),
			"shutdown state is not allowed to be changed to stopped state",
			"devbox",
			devbox.Name,
		)
		return errors.New("shutdown state is not allowed to be changed to stopped state")
	}

	// Handle state transitions that require commit, only running and paused devbox can be shutdown or stopped
	needsCommit := (targetState == devboxv1alpha2.DevboxStateShutdown || targetState == devboxv1alpha2.DevboxStateStopped) &&
		(currentState == devboxv1alpha2.DevboxStateRunning || currentState == devboxv1alpha2.DevboxStatePaused)

	if needsCommit {
		// Keep the lock held across the whole retry loop to prevent concurrent commits during backoff windows.
		commitKey := devbox.Status.ContentID
		if commitKey == "" {
			err := errors.New("empty contentID, cannot start commit")
			h.Logger.Error(err, "invalid devbox for commit", "devbox", devbox.Name)
			return err
		}

		// Check if commit is already in progress to prevent duplicate requests
		if _, loaded := commitMap.LoadOrStore(commitKey, true); loaded {
			h.Logger.Info(
				"commit already in progress, skipping duplicate request",
				"devbox",
				devbox.Name,
				"contentID",
				commitKey,
			)
			return nil
		}
		defer commitMap.Delete(commitKey)

		start := time.Now()
		h.Logger.Info(
			"start commit devbox",
			"devbox",
			devbox.Name,
			"contentID",
			devbox.Status.ContentID,
			"time",
			start,
		)

		// retry commit devbox with retry logic
		// backoff: fixed 10s, up to 30 steps (~5min)
		err := retry.OnError(wait.Backoff{
			Duration: 10 * time.Second,
			Factor:   1.0,
			Jitter:   0.1,
			Steps:    30,
		}, func(err error) bool {
			// Don't retry if the context is cancelled/timed out, or if devbox is not found
			// Controller will handle storage cleanup when devbox is not found
			return !errors.Is(err, context.Canceled) &&
				!errors.Is(err, context.DeadlineExceeded) &&
				!apierrors.IsNotFound(err)
		}, func() error {
			err := h.commitDevbox(ctx, devbox, targetState)
			if err != nil {
				h.Logger.Error(err, "failed to commit devbox in retry", "devbox", devbox.Name)
				return err
			}
			return nil
		})
		if err != nil {
			h.Logger.Error(err, "failed to commit devbox after retries", "devbox", devbox.Name)
			return err
		}

		h.Logger.Info(
			"commit devbox success",
			"devbox",
			devbox.Name,
			"contentID",
			devbox.Status.ContentID,
			"time",
			time.Since(start),
		)
	} else if currentState != targetState {
		// Handle simple state transitions without commit with retry
		h.Logger.Info(
			"update devbox status",
			"devbox",
			devbox.Name,
			"from",
			currentState,
			"to",
			targetState,
		)
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latestDevbox := &devboxv1alpha2.Devbox{}
			if err := h.Client.Get(
				ctx,
				types.NamespacedName{Namespace: devbox.Namespace, Name: devbox.Name},
				latestDevbox,
			); err != nil {
				return err
			}
			latestDevbox.Status.State = targetState
			// Transition synced; clear pending and advance observedGeneration.
			if latestDevbox.Spec.State == latestDevbox.Status.State {
				latestDevbox.Status.ObservedGeneration = latestDevbox.Generation
				latestDevbox.SetCondition(metav1.Condition{
					Type:               devboxv1alpha2.DevboxConditionStateTransitionPending,
					Status:             metav1.ConditionFalse,
					ObservedGeneration: latestDevbox.Generation,
					Reason:             devboxv1alpha2.DevboxReasonStateTransitionSynced,
					Message:            "spec.state matches status.state",
					LastTransitionTime: metav1.Now(),
				})
			}
			latestDevbox.SetCondition(metav1.Condition{
				Type:               devboxv1alpha2.DevboxConditionCommitInProgress,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: latestDevbox.Generation,
				Reason:             devboxv1alpha2.DevboxReasonCommitNotInProgress,
				Message:            "no commit workflow in progress",
				LastTransitionTime: metav1.Now(),
			})
			return h.Client.Status().Update(ctx, latestDevbox)
		})
		if err != nil {
			h.Logger.Error(err, "failed to update devbox status", "devbox", devbox.Name)
			return err
		}
	}
	return nil
}

func (h *EventHandler) handleStorageCleanup(ctx context.Context, event *corev1.Event) error {
	h.Logger.Info("Storage cleanup event detected", "event", event.Name, "message", event.Message)
	if _, loaded := deleteMap.LoadOrStore(event.InvolvedObject.Name, true); loaded {
		h.Logger.Info(
			"delete devbox already in progress, skipping duplicate request",
			"devbox",
			event.InvolvedObject.Name,
		)
		return nil
	}
	defer func() {
		deleteMap.Delete(event.InvolvedObject.Name)
	}()
	if err := h.removeStorage(ctx, event); err != nil {
		h.Logger.Error(err, "failed to clean up storage during delete devbox", "devbox", event.Name)
		h.Recorder.Eventf(&corev1.ObjectReference{
			Kind:      event.InvolvedObject.Kind,
			Name:      event.InvolvedObject.Name,
			Namespace: event.InvolvedObject.Namespace,
		}, corev1.EventTypeWarning, "Storage cleanup failed",
			"Failed to cleanup Storage: %v", err)
	} else {
		h.Logger.Info("Successfully requested storage cleanup during deletion", "devbox", event.Name)
		h.Recorder.Eventf(&corev1.ObjectReference{
			Kind:      event.InvolvedObject.Kind,
			Name:      event.InvolvedObject.Name,
			Namespace: event.InvolvedObject.Namespace,
		}, corev1.EventTypeNormal, "Storage cleanup succeeded",
			"Successfully requested storage cleanup for devbox %s", event.Name)
	}
	return nil
}

func (h *EventHandler) commitDevbox(
	ctx context.Context,
	devbox *devboxv1alpha2.Devbox,
	targetState devboxv1alpha2.DevboxState,
) error {
	if err := h.Client.Get(
		ctx,
		types.NamespacedName{Namespace: devbox.Namespace, Name: devbox.Name},
		devbox,
	); err != nil {
		if apierrors.IsNotFound(err) {
			h.Logger.Info("devbox not found at start of commit", "devbox", devbox.Name)
			return err
		}
		h.Logger.Error(err, "failed to get devbox", "devbox", devbox.Name)
		return err
	}
	// do commit, update devbox commit record, update devbox status state to shutdown, add a new commit record for the new content id
	// step 0: set commit status to committing to prevent duplicate requests with retry
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latestDevbox := &devboxv1alpha2.Devbox{}
		if err := h.Client.Get(
			ctx,
			types.NamespacedName{Namespace: devbox.Namespace, Name: devbox.Name},
			latestDevbox,
		); err != nil {
			// If devbox is not found, return the error to stop retrying
			if apierrors.IsNotFound(err) {
				return err
			}
			return err
		}
		currentRecord, err := getCurrentCommitRecord(latestDevbox)
		if err != nil {
			return err
		}
		currentRecord.CommitStatus = devboxv1alpha2.CommitStatusCommitting
		currentRecord.UpdateTime = metav1.Now()
		latestDevbox.SetCondition(metav1.Condition{
			Type:               devboxv1alpha2.DevboxConditionCommitInProgress,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: latestDevbox.Generation,
			Reason:             devboxv1alpha2.DevboxReasonCommitStarted,
			Message:            "commit workflow in progress",
			LastTransitionTime: metav1.Now(),
		})
		return h.Client.Status().Update(ctx, latestDevbox)
	}); err != nil {
		if apierrors.IsNotFound(err) {
			h.Logger.Info("devbox not found when setting commit status", "devbox", devbox.Name)
			return err
		}
		h.Logger.Error(err, "failed to update commit status to committing", "devbox", devbox.Name)
		return err
	}
	h.Logger.Info(
		"set commit status to committing",
		"devbox",
		devbox.Name,
		"contentID",
		devbox.Status.ContentID,
	)

	if err := h.Client.Get(
		ctx,
		types.NamespacedName{Namespace: devbox.Namespace, Name: devbox.Name},
		devbox,
	); err != nil {
		if apierrors.IsNotFound(err) {
			h.Logger.Info("devbox not found before commit", "devbox", devbox.Name)
			return err
		}
		h.Logger.Error(err, "failed to get devbox", "devbox", devbox.Name)
		return err
	}
	// step 1: do commit, push image, remove container whether commit success or not
	currentRecord, err := getCurrentCommitRecord(devbox)
	if err != nil {
		h.Logger.Error(err, "failed to get current commit record", "devbox", devbox.Name)
		return err
	}
	if _, err := helper.EnsureCommitRecordRuntimeMetadata(
		ctx,
		h.Client,
		currentRecord,
		devbox.Spec.RuntimeClassName,
	); err != nil {
		h.Logger.Error(err, "failed to resolve runtime metadata", "devbox", devbox.Name)
		return err
	}
	committer, err := h.getCommitterBySnapshotter(currentRecord.Snapshotter)
	if err != nil {
		h.Logger.Error(
			err,
			"failed to select committer by snapshotter",
			"devbox",
			devbox.Name,
			"snapshotter",
			currentRecord.Snapshotter,
		)
		return err
	}
	baseImage := currentRecord.BaseImage
	commitImage := currentRecord.CommitImage
	oldContentID := devbox.Status.ContentID
	h.Logger.Info(
		"commit devbox",
		"devbox",
		devbox.Name,
		"baseImage",
		baseImage,
		"commitImage",
		commitImage,
	)
	var containerID string
	var commitErr error
	removeImageNames := make([]string, 0, 2)
	defer func() {
		// remove container whether commit success or not
		if strings.TrimSpace(containerID) != "" {
			if err := committer.RemoveContainers(ctx, []string{containerID}); err != nil {
				h.Logger.Error(err, "failed to remove container", "containerID", containerID)
			}
		}
		// remove after push image whether push success
		if len(removeImageNames) > 0 {
			if err := committer.RemoveImages(
				ctx,
				removeImageNames,
				commit.DefaultRemoveImageForce,
				commit.DefaultRemoveImageAsync,
			); err != nil {
				h.Logger.Error(err, "failed to remove image", "removeImageNames", removeImageNames)
			}
		}
	}()
	previousContainerID := normalizeContainerRuntimeID(devbox.Status.LastContainerStatus.ContainerID)
	if previousContainerID != "" {
		h.Logger.Info(
			"waiting for previous devbox container to stop before commit",
			"devbox",
			devbox.Name,
			"containerID",
			previousContainerID,
		)
		if err := committer.WaitContainerStopped(ctx, previousContainerID, 30*time.Second); err != nil {
			h.Logger.Error(
				err,
				"failed waiting for previous devbox container to stop before commit",
				"devbox",
				devbox.Name,
				"containerID",
				previousContainerID,
			)
			return err
		}
		if err := committer.UnmountSnapshot(ctx, previousContainerID); err != nil {
			h.Logger.Error(
				err,
				"failed to detach previous devbox snapshot before commit",
				"devbox",
				devbox.Name,
				"containerID",
				previousContainerID,
			)
			return err
		}
	}

	imageExists, err := committer.ImageExists(ctx, commitImage)
	if err != nil {
		h.Logger.Error(err, "failed to check local commit image", "commitImage", commitImage)
		return err
	}
	if imageExists {
		h.Logger.Info(
			"commit image already exists locally, skip commit and push directly",
			"devbox",
			devbox.Name,
			"commitImage",
			commitImage,
		)
	} else {
		if containerID, commitErr = committer.Commit(
			ctx,
			devbox.Name,
			devbox.Status.ContentID,
			baseImage,
			commitImage,
		); commitErr != nil {
			h.Logger.Error(commitErr, "failed to commit devbox", "devbox", devbox.Name)
			// Update commit status to failed on commit error with retry
			updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				latestDevbox := &devboxv1alpha2.Devbox{}
				if err := h.Client.Get(
					ctx,
					types.NamespacedName{Namespace: devbox.Namespace, Name: devbox.Name},
					latestDevbox,
				); err != nil {
					// If devbox is not found, return the error
					// RetryOnConflict will return this error immediately without retrying
					if apierrors.IsNotFound(err) {
						return err
					}
					return err
				}
				currentRecord, err := getCurrentCommitRecord(latestDevbox)
				if err != nil {
					return err
				}
				currentRecord.CommitStatus = devboxv1alpha2.CommitStatusFailed
				currentRecord.UpdateTime = metav1.Now()
				latestDevbox.SetCondition(metav1.Condition{
					Type:               devboxv1alpha2.DevboxConditionCommitInProgress,
					Status:             metav1.ConditionFalse,
					ObservedGeneration: latestDevbox.Generation,
					Reason:             devboxv1alpha2.DevboxReasonCommitFailed,
					Message:            "commit workflow failed",
					LastTransitionTime: metav1.Now(),
				})
				return h.Client.Status().Update(ctx, latestDevbox)
			})
			if updateErr != nil {
				if apierrors.IsNotFound(updateErr) {
					h.Logger.Info(
						"devbox not found when updating commit status to failed",
						"devbox",
						devbox.Name,
					)
					return updateErr
				}
				h.Logger.Error(
					updateErr,
					"failed to update commit status to failed",
					"devbox",
					devbox.Name,
				)
			}
			return commitErr
		}
	}
	if err := h.Client.Get(
		ctx,
		types.NamespacedName{Namespace: devbox.Namespace, Name: devbox.Name},
		devbox,
	); err != nil {
		if apierrors.IsNotFound(err) {
			h.Logger.Info("devbox not found before push", "devbox", devbox.Name)
			return err
		}
		h.Logger.Error(err, "failed to get devbox", "devbox", devbox.Name)
		return err
	}
	if err := committer.Push(ctx, commitImage); err != nil {
		h.Logger.Error(err, "failed to push commit image", "commitImage", commitImage)
		// Update commit status to failed on push error with retry
		updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latestDevbox := &devboxv1alpha2.Devbox{}
			if err := h.Client.Get(
				ctx,
				types.NamespacedName{Namespace: devbox.Namespace, Name: devbox.Name},
				latestDevbox,
			); err != nil {
				// If devbox is not found, return the error
				if apierrors.IsNotFound(err) {
					return err
				}
				return err
			}
			currentRecord, err := getCurrentCommitRecord(latestDevbox)
			if err != nil {
				return err
			}
			currentRecord.CommitStatus = devboxv1alpha2.CommitStatusFailed
			currentRecord.UpdateTime = metav1.Now()
			latestDevbox.SetCondition(metav1.Condition{
				Type:               devboxv1alpha2.DevboxConditionCommitInProgress,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: latestDevbox.Generation,
				Reason:             devboxv1alpha2.DevboxReasonCommitFailed,
				Message:            "commit workflow failed (push error)",
				LastTransitionTime: metav1.Now(),
			})
			return h.Client.Status().Update(ctx, latestDevbox)
		})
		if updateErr != nil {
			if apierrors.IsNotFound(updateErr) {
				h.Logger.Info(
					"devbox not found when updating commit status to failed after push error",
					"devbox",
					devbox.Name,
				)
				return updateErr
			}
			h.Logger.Error(
				updateErr,
				"failed to update commit status to failed",
				"devbox",
				devbox.Name,
			)
		}
		return err
	}
	h.Logger.Info("push commit image success", "commitImage", commitImage)
	removeImageNames = append(removeImageNames, commitImage, baseImage)
	// step 2: update devbox commit record
	// step 3: update devbox status state to shutdown
	// step 4: add a new commit record for the new content id
	// make sure that always have a new commit record for shutdown state
	newContentID := uuid.New().String()
	newCommitImage := h.generateImageName(devbox)
	h.Logger.Info("update devbox status to shutdown", "devbox", devbox.Name)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latestDevbox := &devboxv1alpha2.Devbox{}
		if err := h.Client.Get(
			ctx,
			types.NamespacedName{Namespace: devbox.Namespace, Name: devbox.Name},
			latestDevbox,
		); err != nil {
			// If devbox is not found, return the error to stop retrying
			if apierrors.IsNotFound(err) {
				return err
			}
			return err
		}
		currentRecord, err := getCurrentCommitRecord(latestDevbox)
		if err != nil {
			return err
		}
		if _, err := helper.EnsureCommitRecordRuntimeMetadata(
			ctx,
			h.Client,
			currentRecord,
			latestDevbox.Spec.RuntimeClassName,
		); err != nil {
			return err
		}
		currentRecord.CommitStatus = devboxv1alpha2.CommitStatusSuccess
		currentRecord.CommitTime = metav1.Now()
		latestDevbox.Status.State = targetState
		latestDevbox.Status.ContentID = newContentID
		if latestDevbox.Status.CommitRecords == nil {
			latestDevbox.Status.CommitRecords = make(devboxv1alpha2.CommitRecordMap)
		}
		latestDevbox.Status.CommitRecords[newContentID] = &devboxv1alpha2.CommitRecord{
			CommitStatus:     devboxv1alpha2.CommitStatusPending,
			Node:             "",
			BaseImage:        commitImage,
			CommitImage:      newCommitImage,
			GenerateTime:     metav1.Now(),
			RuntimeClassName: currentRecord.RuntimeClassName,
			RuntimeHandler:   currentRecord.RuntimeHandler,
			Snapshotter:      currentRecord.Snapshotter,
		}
		latestDevbox.Status.Node = ""
		// Commit succeeded; clear in-progress, and clear pending transition if synced.
		latestDevbox.SetCondition(metav1.Condition{
			Type:               devboxv1alpha2.DevboxConditionCommitInProgress,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: latestDevbox.Generation,
			Reason:             devboxv1alpha2.DevboxReasonCommitSucceeded,
			Message:            "commit workflow succeeded",
			LastTransitionTime: metav1.Now(),
		})
		if latestDevbox.Spec.State == latestDevbox.Status.State {
			latestDevbox.Status.ObservedGeneration = latestDevbox.Generation
			latestDevbox.SetCondition(metav1.Condition{
				Type:               devboxv1alpha2.DevboxConditionStateTransitionPending,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: latestDevbox.Generation,
				Reason:             devboxv1alpha2.DevboxReasonStateTransitionSynced,
				Message:            "spec.state matches status.state",
				LastTransitionTime: metav1.Now(),
			})
		}
		return h.Client.Status().Update(ctx, latestDevbox)
	}); err != nil {
		if apierrors.IsNotFound(err) {
			h.Logger.Info("devbox not found when updating status", "devbox", devbox.Name)
			return err
		}
		h.Logger.Error(err, "failed to update devbox status", "devbox", devbox.Name)
		return err
	}
	// step 5: set LV removable
	if containerID != "" {
		if err := committer.SetLvRemovable(ctx, containerID, oldContentID); err != nil {
			h.Logger.Error(
				err,
				"failed to set LV removable",
				"containerID",
				containerID,
				"contentID",
				oldContentID,
			)
		}
	} else {
		h.Logger.Info(
			"skip set LV removable because commit container was not created in this round",
			"devbox",
			devbox.Name,
			"contentID",
			oldContentID,
			"commitImage",
			commitImage,
		)
	}
	return nil
}

func (h *EventHandler) generateImageName(devbox *devboxv1alpha2.Devbox) string {
	now := time.Now()
	return fmt.Sprintf(
		"%s/%s/%s:%s-%s",
		h.CommitImageRegistry,
		devbox.Namespace,
		devbox.Name,
		rand.String(5),
		now.Format("2006-01-02-150405"),
	)
}

func (h *EventHandler) removeStorage(ctx context.Context, event *corev1.Event) error {
	h.Logger.Info(
		"Starting devbox deletion Storage cleanup",
		"devbox",
		event.Name,
		"message",
		event.Message,
	)
	devboxName, contentID, baseImage, snapshotter, runtimeClass, runtimeHandler := h.parseStorageCleanupAnno(event.Annotations)

	// Use k8s.io/client-go/util/retry for robust retry logic
	err := retry.OnError(
		wait.Backoff{
			Duration: 10 * time.Second,
			Factor:   1.0,
			Jitter:   0.1,
			Steps:    30,
		},
		func(err error) bool { return true },
		func() error {
			return h.cleanupStorage(ctx, devboxName, contentID, baseImage, snapshotter, runtimeClass, runtimeHandler)
		},
	)
	if err != nil {
		h.Logger.Error(err, "Failed to cleanup storage after all retries", "devbox", devboxName)
		return fmt.Errorf(
			"failed to cleanup storage for devbox %s after retries: %w",
			devboxName,
			err,
		)
	}
	h.Logger.Info("Successfully requested storage cleanup", "devbox", devboxName)
	return nil
}

func (h *EventHandler) cleanupStorage(
	ctx context.Context,
	devboxName, contentID, baseImage, snapshotter, runtimeClass, runtimeHandler string,
) error {
	committer, err := h.getCommitterBySnapshotter(snapshotter)
	if err != nil {
		h.Logger.Error(
			err,
			"failed to select committer for cleanup",
			"devbox",
			devboxName,
			"contentID",
			contentID,
			"snapshotter",
			snapshotter,
			"runtimeClass",
			runtimeClass,
			"runtimeHandler",
			runtimeHandler,
		)
		return err
	}
	h.Logger.Info(
		"Starting Storage cleanup",
		"devbox",
		devboxName,
		"contentID",
		contentID,
		"baseImage",
		baseImage,
		"snapshotter",
		snapshotter,
		"runtimeClass",
		runtimeClass,
		"runtimeHandler",
		runtimeHandler,
		"defaultBaseImage",
		h.DefaultBaseImage,
	)

	// create temp container
	containerID, err := committer.CreateContainer(
		ctx,
		fmt.Sprintf("temp-%s-%d", devboxName, time.Now().UnixMicro()),
		contentID,
		h.DefaultBaseImage,
	)
	if err != nil {
		h.Logger.Error(
			err,
			"failed to create temp container",
			"devbox",
			devboxName,
			"contentID",
			contentID,
			"defaultBaseImage",
			h.DefaultBaseImage,
		)
		return err
	}

	// make sure remove container
	defer func() {
		if cleanupErr := committer.RemoveContainers(
			ctx,
			[]string{containerID},
		); cleanupErr != nil {
			h.Logger.Error(
				cleanupErr,
				"failed to remove temporary container",
				"devbox",
				devboxName,
				"containerID",
				containerID,
			)
		} else {
			h.Logger.Info(
				"Successfully removed temporary container",
				"devbox",
				devboxName,
				"containerID",
				containerID,
			)
		}
	}()

	// remove storage
	if err := committer.SetLvRemovable(ctx, containerID, contentID); err != nil {
		h.Logger.Error(
			err,
			"failed to set Storage removable",
			"devbox",
			devboxName,
			"containerID",
			containerID,
			"contentID",
			contentID,
		)
		return fmt.Errorf("failed to set Storage removable: %w", err)
	}

	h.Logger.Info(
		"Successfully marked storage removable",
		"devbox",
		devboxName,
		"containerID",
		containerID,
		"contentID",
		contentID,
	)

	return nil
}

// parseStorageCleanupAnno parses the annotations from the event and returns the devboxName, contentID, and baseImage
func (h *EventHandler) parseStorageCleanupAnno(
	annotations events.Annotations,
) (devboxName, contentID, baseImage, snapshotter, runtimeClass, runtimeHandler string) {
	devboxName = annotations[events.KeyAnnotationDevboxName]
	contentID = annotations[events.KeyAnnotationContentID]
	baseImage = annotations[events.KeyAnnotationBaseImage]
	snapshotter = annotations[events.KeyAnnotationSnapshotter]
	runtimeClass = annotations[events.KeyAnnotationRuntimeClass]
	runtimeHandler = annotations[events.KeyAnnotationRuntimeHandler]
	return devboxName, contentID, baseImage, snapshotter, runtimeClass, runtimeHandler
}

func normalizeContainerRuntimeID(containerID string) string {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return ""
	}
	parts := strings.SplitN(containerID, "://", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return containerID
}

func getCurrentCommitRecord(devbox *devboxv1alpha2.Devbox) (*devboxv1alpha2.CommitRecord, error) {
	if devbox.Status.CommitRecords == nil {
		return nil, fmt.Errorf("commit records are empty for devbox %s", devbox.Name)
	}
	record := devbox.Status.CommitRecords[devbox.Status.ContentID]
	if record == nil {
		return nil, fmt.Errorf(
			"commit record not found for devbox %s contentID %s",
			devbox.Name,
			devbox.Status.ContentID,
		)
	}
	return record, nil
}
