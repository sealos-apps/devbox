package commit

import "time"

const (
	DefaultNamespace            = "k8s.io"
	DefaultContainerdAddress    = "unix:///var/run/containerd/containerd.sock"
	DefaultRuntime              = "io.containerd.runc.v2"
	DefaultNerdctlDataRoot      = "/var/lib/containerd"
	DefaultNerdctlHostsDir      = "/etc/containerd/certs.d"
	DefaultDevboxSnapshotter    = "devbox"
	DevboxStargzSnapshotter     = "stargz"
	DefaultNetworkMode          = "none"
	DefaultRemoveImageAsync     = true
	DefaultRemoveImageForce     = false
	DefaultRemoveContainerForce = false
	InsecureRegistry            = true
	PauseContainerDuringCommit  = false

	AnnotationKeyNamespace               = "namespace"
	AnnotationKeyImageName               = "image.name"
	AnnotationImageFromValue             = "true"
	AnnotationUseLimitValue              = "1Gi"
	DevboxOptionsRemoveBaseImageTopLayer = true

	SnapshotLabelPrefix  = "containerd.io/snapshot/devbox-"
	ContainerLabelPrefix = "devbox.sealos.io/"
	RemoveContentIDkey   = "containerd.io/snapshot/devbox-remove-content-id"

	DefaultStargzPrefetchSize = 10 * 1024 * 1024

	DefaultMaxRetries = 3
	DefaultRetryDelay = 5 * time.Second
	DefaultGcInterval = 20 * time.Minute
)
