package events

const (
	ReasonStorageCleanupRequested = "storage-cleanup-requested"
	ReasonDevboxStateChanged      = "devbox-state-changed"

	KeyAnnotationReason         = "reason"
	KeyAnnotationDevboxName     = "devbox-name"
	KeyAnnotationContentID      = "content-id"
	KeyAnnotationBaseImage      = "base-image"
	KeyAnnotationSnapshotter    = "snapshotter"
	KeyAnnotationRuntimeClass   = "runtime-class"
	KeyAnnotationRuntimeHandler = "runtime-handler"
)

type Annotations map[string]string

func BuildStorageCleanupAnnotations(
	devboxName, contentID, baseImage, snapshotter, runtimeClass, runtimeHandler string,
) Annotations {
	return Annotations{
		KeyAnnotationReason:         ReasonStorageCleanupRequested,
		KeyAnnotationDevboxName:     devboxName,
		KeyAnnotationContentID:      contentID,
		KeyAnnotationBaseImage:      baseImage,
		KeyAnnotationSnapshotter:    snapshotter,
		KeyAnnotationRuntimeClass:   runtimeClass,
		KeyAnnotationRuntimeHandler: runtimeHandler,
	}
}
