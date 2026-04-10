package commit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/remotes/docker/config"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	ctdsnapshotters "github.com/containerd/containerd/v2/pkg/snapshotters"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/nerdctl/v2/pkg/api/types"
	"github.com/containerd/nerdctl/v2/pkg/cmd/container"
	"github.com/containerd/nerdctl/v2/pkg/cmd/image"
	"github.com/containerd/nerdctl/v2/pkg/cmd/login"
	"github.com/containerd/nerdctl/v2/pkg/containerutil"
	ncdefaults "github.com/containerd/nerdctl/v2/pkg/defaults"
	nerderrutil "github.com/containerd/nerdctl/v2/pkg/errutil"
	"github.com/containerd/nerdctl/v2/pkg/imgutil/dockerconfigresolver"
	"github.com/containerd/platforms"
	"github.com/containerd/stargz-snapshotter/fs/source"
	"github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Committer interface {
	CreateContainer(ctx context.Context, devboxName, contentID, baseImage string) (string, error)
	Commit(
		ctx context.Context,
		devboxName, contentID, baseImage, commitImage string,
	) (string, error)
	ImageExists(ctx context.Context, imageName string) (bool, error)
	Push(ctx context.Context, imageName string) error
	RemoveImages(ctx context.Context, imageNames []string, force, async bool) error
	RemoveContainers(ctx context.Context, containerNames []string) error
	InitializeGC(ctx context.Context) error
	SetLvRemovable(ctx context.Context, containerID, contentID string) error
	UnmountSnapshot(ctx context.Context, containerID string) error
	WaitContainerStopped(ctx context.Context, containerID string, timeout time.Duration) error
}

type CommitterImpl struct {
	containerdClient *containerd.Client          // containerd client
	conn             *grpc.ClientConn            // gRPC connection
	globalOptions    *types.GlobalCommandOptions // global options
	registryAddr     string
	registryUsername string
	registryPassword string
	// Merge base image layers control
	mergeBaseImageTopLayer bool
	// Configurable via flags
	devboxSnapshotter string
	networkMode       string
}

func shouldCommitAsEstargz(snapshotter string) bool {
	return snapshotter == DevboxStargzSnapshotter
}

func isDevboxStargzSnapshotter(snapshotter string) bool {
	return snapshotter == DevboxStargzSnapshotter
}

func (c *CommitterImpl) newCommitOptions() types.ContainerCommitOptions {
	opt := types.ContainerCommitOptions{
		Stdout:   io.Discard,
		GOptions: *c.globalOptions,
		Pause:    PauseContainerDuringCommit,
		DevboxOptions: types.DevboxOptions{
			RemoveBaseImageTopLayer: c.mergeBaseImageTopLayer,
		},
	}
	if shouldCommitAsEstargz(c.devboxSnapshotter) {
		opt.Format = types.ImageFormatOCI
		opt.Estargz = true
	}
	return opt
}

// NewCommitter new a CommitterImpl with registry configuration.
// snapshotter and networkMode use DefaultDevboxSnapshotter and DefaultNetworkMode when empty.
func NewCommitter(
	registryAddr, registryUsername, registryPassword string,
	merge bool,
	snapshotter, networkMode string,
) (Committer, error) {
	if snapshotter == "" {
		snapshotter = DefaultDevboxSnapshotter
	}
	if networkMode == "" {
		networkMode = DefaultNetworkMode
	}
	var conn *grpc.ClientConn
	var err error

	// login to registry
	err = login.Login(context.Background(), types.LoginCommandOptions{
		GOptions:      *newGlobalOptionConfigWithSnapshotter(snapshotter),
		ServerAddress: registryAddr,
		Username:      registryUsername,
		Password:      registryPassword,
	}, io.Discard)
	if err != nil {
		return nil, err
	}

	// retry to connect
	for i := 0; i <= DefaultMaxRetries; i++ {
		if i > 0 {
			log.Printf("Retrying connection to containerd (attempt %d/%d)...", i, DefaultMaxRetries)
			time.Sleep(DefaultRetryDelay)
		}

		// create gRPC connection
		conn, err = grpc.NewClient(
			DefaultContainerdAddress,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err == nil {
			log.Printf("Successfully connected to containerd at %s", DefaultContainerdAddress)
			break
		}

		log.Printf(
			"Failed to connect to containerd (attempt %d/%d): %v",
			i+1,
			DefaultMaxRetries+1,
			err,
		)

		if i == DefaultMaxRetries {
			return nil, fmt.Errorf(
				"failed to connect to containerd after %d attempts: %w",
				DefaultMaxRetries+1,
				err,
			)
		}
	}

	// create Containerd client
	containerdClient, err := containerd.NewWithConn(
		conn,
		containerd.WithDefaultNamespace(DefaultNamespace),
	)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create containerd client: %w", err)
	}

	return &CommitterImpl{
		containerdClient:       containerdClient,
		conn:                   conn,
		globalOptions:          newGlobalOptionConfigWithSnapshotter(snapshotter),
		registryAddr:           registryAddr,
		registryUsername:       registryUsername,
		registryPassword:       registryPassword,
		mergeBaseImageTopLayer: merge,
		devboxSnapshotter:      snapshotter,
		networkMode:            networkMode,
	}, nil
}

// CreateContainer create container with labels
func (c *CommitterImpl) CreateContainer(
	ctx context.Context,
	devboxName, contentID, baseImage string,
) (string, error) {
	log.Printf(
		"========>>>> create container, devboxName: %s, contentID: %s, baseImage: %s",
		devboxName,
		contentID,
		baseImage,
	)
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)

	// check connection status, if connection is bad, try to reconnect
	if err := c.CheckConnection(ctx); err != nil {
		log.Printf("Connection check failed: %v, attempting to reconnect...", err)
		if reconnectErr := c.Reconnect(ctx); reconnectErr != nil {
			return "", fmt.Errorf("failed to reconnect: %w", reconnectErr)
		}
	}

	if err := c.ensureBaseImageForSnapshotter(ctx, baseImage); err != nil {
		return "", fmt.Errorf("failed to ensure base image for snapshotter %s: %w", c.devboxSnapshotter, err)
	}

	// create container with labels
	originalAnnotations := map[string]string{
		v1alpha2.AnnotationContentID:    contentID,
		v1alpha2.AnnotationStorageLimit: AnnotationUseLimitValue,
		AnnotationKeyNamespace:          DefaultNamespace,
		AnnotationKeyImageName:          baseImage,
	}

	// Add merge base image layers annotation if enabled
	if c.mergeBaseImageTopLayer {
		originalAnnotations[v1alpha2.AnnotationInit] = AnnotationImageFromValue
	}

	// convert labels to "containerd.io/snapshot/devbox-" format
	convertedLabels := convertLabels(originalAnnotations)
	convertedAnnotations := convertMapToSlice(originalAnnotations)

	// create container options
	createOpt := types.ContainerCreateOptions{
		GOptions:       *c.globalOptions,
		Runtime:        DefaultRuntime, // user devbox runtime
		Name:           fmt.Sprintf("devbox-%s-container-%d", devboxName, time.Now().UnixMicro()),
		Pull:           "missing",
		InRun:          false, // not start container
		Rm:             false,
		LogDriver:      "json-file",
		StopSignal:     "SIGTERM",
		Restart:        "unless-stopped",
		Interactive:    false,  // not interactive, avoid conflict with Detach
		Cgroupns:       "host", // add cgroupns mode
		Detach:         true,   // run in background
		Rootfs:         false,
		Label:          convertedAnnotations,
		SnapshotLabels: convertedLabels,
		ImagePullOpt: types.ImagePullOptions{
			GOptions: *c.globalOptions,
		},
	}

	// create network manager
	networkManager, err := containerutil.NewNetworkingOptionsManager(createOpt.GOptions,
		types.NetworkOptions{
			NetworkSlice: []string{c.networkMode},
		}, c.containerdClient)
	if err != nil {
		log.Println("failed to create network manager:", err)
		return "", fmt.Errorf("failed to create network manager: %w", err)
	}

	// create container
	container, cleanup, err := container.Create(
		ctx,
		c.containerdClient,
		[]string{originalAnnotations[AnnotationKeyImageName]},
		networkManager,
		createOpt,
	)
	if err != nil {
		log.Println("failed to create container:", err)
		return "", fmt.Errorf("failed to create container: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	log.Printf("container created successfully: %s\n", container.ID())
	return container.ID(), nil
}

func (c *CommitterImpl) ensureBaseImageForSnapshotter(ctx context.Context, imageRef string) error {
	if !isDevboxStargzSnapshotter(c.devboxSnapshotter) {
		return nil
	}

	platformStr := platforms.Format(platforms.DefaultSpec())

	if existing, err := c.containerdClient.GetImage(ctx, imageRef); err == nil {
		if unpacked, unpackErr := existing.IsUnpacked(ctx, c.devboxSnapshotter); unpackErr == nil && unpacked {
			return nil
		}
	} else if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("failed to inspect base image %q: %w", imageRef, err)
	}

	resolver, err := c.newResolver(ctx, imageRef, false)
	if err != nil {
		return err
	}

	remoteOpts := []containerd.RemoteOpt{
		containerd.WithResolver(resolver),
		containerd.WithPlatform(platformStr),
		containerd.WithPullUnpack,
		containerd.WithPullSnapshotter(
			c.devboxSnapshotter,
			snapshots.WithLabels(map[string]string{}),
		),
		containerd.WithImageHandlerWrapper(
			source.AppendExtraLabelsHandler(
				DefaultStargzPrefetchSize,
				ctdsnapshotters.AppendInfoHandlerWrapper(imageRef),
			),
		),
	}

	_, err = c.containerdClient.Pull(ctx, imageRef, remoteOpts...)
	if err == nil {
		return nil
	}
	if !errors.Is(err, http.ErrSchemeMismatch) && !nerderrutil.IsErrConnectionRefused(err) {
		return fmt.Errorf("failed to pull image %q with snapshotter %s: %w", imageRef, c.devboxSnapshotter, err)
	}

	resolver, resolveErr := c.newResolver(ctx, imageRef, true)
	if resolveErr != nil {
		return fmt.Errorf("failed to rebuild plain-http resolver after pull error: %w", resolveErr)
	}
	remoteOpts[0] = containerd.WithResolver(resolver)
	if _, err = c.containerdClient.Pull(ctx, imageRef, remoteOpts...); err != nil {
		return fmt.Errorf("failed to pull image %q with plain HTTP and snapshotter %s: %w", imageRef, c.devboxSnapshotter, err)
	}

	return nil
}

func (c *CommitterImpl) newResolver(ctx context.Context, imageRef string, plainHTTP bool) (remotes.Resolver, error) {
	ref, err := dockerconfigresolver.Parse(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image ref %q: %w", imageRef, err)
	}

	opts := []dockerconfigresolver.Opt{
		dockerconfigresolver.WithHostsDirs([]string{DefaultNerdctlHostsDir}),
	}
	if InsecureRegistry {
		opts = append(opts, dockerconfigresolver.WithSkipVerifyCerts(true))
	}
	if plainHTTP {
		opts = append(opts, dockerconfigresolver.WithPlainHTTP(true))
	}
	return dockerconfigresolver.New(ctx, ref.Hostname(), opts...)
}

// DeleteContainer delete container
func (c *CommitterImpl) DeleteContainer(ctx context.Context, containerName string) error {
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)
	container, err := c.containerdClient.LoadContainer(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to load container: %w", err)
	}

	// try to get and stop task
	task, err := container.Task(ctx, nil)
	if err == nil {
		log.Printf("Stopping task for container: %s", containerName)

		// force kill task
		err = task.Kill(ctx, 9) // SIGKILL
		if err != nil {
			log.Printf("Warning: failed to send SIGKILL: %v", err)
		} else {
			log.Printf("Sent SIGKILL to task")
		}

		// delete task
		log.Printf("Deleting task...")
		_, err = task.Delete(ctx, containerd.WithProcessKill)
		if err != nil {
			log.Printf("Warning: failed to delete task: %v", err)
		} else {
			log.Printf("Task deleted for container: %s", containerName)
		}
	}

	// delete container (include snapshot)
	err = container.Delete(ctx, containerd.WithSnapshotCleanup)
	if err != nil {
		return fmt.Errorf("failed to delete container: %w", err)
	}

	log.Printf("Container deleted: %s successfully", containerName)
	return nil
}

func (c *CommitterImpl) SetLvRemovable(ctx context.Context, containerID, contentID string) error {
	log.Printf(
		"========>>>> set lv removable for container, containerID: %s, contentID: %s",
		containerID,
		contentID,
	)
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)

	// check connection status, if connection is bad, try to reconnect
	if err := c.CheckConnection(ctx); err != nil {
		log.Printf("Connection check failed: %v, attempting to reconnect...", err)
		if reconnectErr := c.Reconnect(ctx); reconnectErr != nil {
			return fmt.Errorf("failed to reconnect: %w", reconnectErr)
		}
	}

	_, err := c.containerdClient.SnapshotService(c.devboxSnapshotter).
		Update(ctx, snapshots.Info{
			Name:   containerID,
			Labels: map[string]string{RemoveContentIDkey: contentID},
		}, "labels."+RemoveContentIDkey)
	if err != nil {
		return err
	}
	return nil
}

func (c *CommitterImpl) UnmountSnapshot(ctx context.Context, containerID string) error {
	log.Printf("========>>>> unmount snapshot for container, containerID: %s", containerID)
	if strings.TrimSpace(containerID) == "" {
		return errors.New("[UnmountSnapshot]containerID is empty")
	}
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)

	if err := c.CheckConnection(ctx); err != nil {
		log.Printf("Connection check failed: %v, attempting to reconnect...", err)
		if reconnectErr := c.Reconnect(ctx); reconnectErr != nil {
			return fmt.Errorf("failed to reconnect: %w", reconnectErr)
		}
	}

	_, err := c.containerdClient.SnapshotService(c.devboxSnapshotter).
		Update(ctx, snapshots.Info{
			Name:   containerID,
			Labels: map[string]string{"containerd.io/snapshot/devbox-unmount-lvm": "true"},
		}, "labels.containerd.io/snapshot/devbox-unmount-lvm")
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

func (c *CommitterImpl) WaitContainerStopped(ctx context.Context, containerID string, timeout time.Duration) error {
	if strings.TrimSpace(containerID) == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)
	if err := c.CheckConnection(ctx); err != nil {
		log.Printf("Connection check failed: %v, attempting to reconnect...", err)
		if reconnectErr := c.Reconnect(ctx); reconnectErr != nil {
			return fmt.Errorf("failed to reconnect: %w", reconnectErr)
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		container, err := c.containerdClient.LoadContainer(waitCtx, containerID)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to load container %q: %w", containerID, err)
		}

		task, err := container.Task(waitCtx, nil)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to load task for container %q: %w", containerID, err)
		}

		status, err := task.Status(waitCtx)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get task status for container %q: %w", containerID, err)
		}

		switch status.Status {
		case containerd.Stopped, containerd.Unknown:
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timeout waiting for container %q to stop", containerID)
		case <-ticker.C:
		}
	}
}

// RemoveContainer remove container
func (c *CommitterImpl) RemoveContainers(ctx context.Context, containerNames []string) error {
	filtered := make([]string, 0, len(containerNames))
	for _, name := range containerNames {
		if strings.TrimSpace(name) != "" {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		return errors.New("[RemoveContainers]containerNames is empty")
	}

	log.Printf("========>>>> remove container, containerNames: %v", filtered)
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)

	// check connection status, if connection is bad, try to reconnect
	if err := c.CheckConnection(ctx); err != nil {
		log.Printf("Connection check failed: %v, attempting to reconnect...", err)
		if reconnectErr := c.Reconnect(ctx); reconnectErr != nil {
			return fmt.Errorf("failed to reconnect: %w", reconnectErr)
		}
	}

	opt := types.ContainerRemoveOptions{
		Stdout:   io.Discard,
		Force:    DefaultRemoveContainerForce,
		Volumes:  false,
		GOptions: *c.globalOptions,
	}
	err := container.Remove(ctx, c.containerdClient, filtered, opt)
	if err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}
	return nil
}

// Commit commit container to image
func (c *CommitterImpl) Commit(
	ctx context.Context,
	devboxName, contentID, baseImage, commitImage string,
) (string, error) {
	log.Printf(
		"========>>>> commit devbox, devboxName: %s, contentID: %s, baseImage: %s, commitImage: %s",
		devboxName,
		contentID,
		baseImage,
		commitImage,
	)
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)
	containerID, err := c.CreateContainer(ctx, devboxName, contentID, baseImage)
	if err != nil {
		return containerID, fmt.Errorf("failed to create container: %w", err)
	}

	// // mark for gc
	// defer c.MarkForGC(containerID, commitImage)

	// create commit options
	opt := c.newCommitOptions()

	// commit container
	err = container.Commit(ctx, c.containerdClient, commitImage, containerID, opt)
	if err != nil {
		return containerID, fmt.Errorf("failed to commit container: %w", err)
	}

	return containerID, nil
}

// ImageExists checks whether image metadata exists in local containerd.
func (c *CommitterImpl) ImageExists(ctx context.Context, imageName string) (bool, error) {
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)

	// check connection status, if connection is bad, try to reconnect
	if err := c.CheckConnection(ctx); err != nil {
		log.Printf("Connection check failed: %v, attempting to reconnect...", err)
		if reconnectErr := c.Reconnect(ctx); reconnectErr != nil {
			return false, fmt.Errorf("failed to reconnect: %w", reconnectErr)
		}
	}

	_, err := c.containerdClient.GetImage(ctx, imageName)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to check local image %s: %w", imageName, err)
}

// GetContainerAnnotations get container annotations
func (c *CommitterImpl) GetContainerAnnotations(
	ctx context.Context,
	containerName string,
) (map[string]string, error) {
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)
	container, err := c.containerdClient.LoadContainer(ctx, containerName)
	if err != nil {
		return nil, fmt.Errorf("failed to load container: %w", err)
	}

	// get container labels (annotations)
	labels, err := container.Labels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container labels: %w", err)
	}
	return labels, nil
}

// Push pushes an image to a remote repository
func (c *CommitterImpl) Push(ctx context.Context, imageName string) error {
	log.Printf("========>>>> push image, imageName: %s", imageName)
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)

	// check connection status, if connection is bad, try to reconnect
	if err := c.CheckConnection(ctx); err != nil {
		log.Printf("Connection check failed: %v, attempting to reconnect...", err)
		if reconnectErr := c.Reconnect(ctx); reconnectErr != nil {
			return fmt.Errorf("failed to reconnect: %w", reconnectErr)
		}
	}

	// set resolver
	resolver, err := GetResolver(ctx, c.registryUsername, c.registryPassword)
	if err != nil {
		log.Printf("failed to set resolver, Image: %s, err: %v\n", imageName, err)
		return err
	}

	imageRef, err := c.containerdClient.GetImage(ctx, imageName)
	if err != nil {
		log.Printf("failed to get image: %s, err: %v\n", imageName, err)
		return err
	}

	// push image
	err = c.containerdClient.Push(ctx, imageName, imageRef.Target(),
		containerd.WithResolver(resolver),
	)
	if err != nil {
		log.Printf("failed to push image: %s, err: %v\n", imageName, err)
		return err
	}
	log.Printf("Pushed image success Image: %s\n", imageName)
	return nil
}

// RemoveImage remove image
func (c *CommitterImpl) RemoveImages(
	ctx context.Context,
	imageNames []string,
	force, async bool,
) error {
	if len(imageNames) == 0 {
		return errors.New("[RemoveImages]imageNames is empty")
	}
	log.Printf("========>>>> remove image, imageNames: %v", imageNames)
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)

	// check connection status, if connection is bad, try to reconnect
	if err := c.CheckConnection(ctx); err != nil {
		log.Printf("Connection check failed: %v, attempting to reconnect...", err)
		if reconnectErr := c.Reconnect(ctx); reconnectErr != nil {
			return fmt.Errorf("failed to reconnect: %w", reconnectErr)
		}
	}

	opt := types.ImageRemoveOptions{
		Stdout:   io.Discard,
		GOptions: *c.globalOptions,
		Force:    force,
		Async:    async,
	}
	return image.Remove(ctx, c.containerdClient, imageNames, opt)
}

// InitializeGC initialize force GC
func (c *CommitterImpl) InitializeGC(ctx context.Context) error {
	gcCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := c.forceGC(gcCtx); err != nil {
		log.Printf("Failed to initialize force GC, err: %v", err)
		return fmt.Errorf("failed to initialize force GC: %w", err)
	}
	log.Println("Force GC initialized successfully")
	return nil
}

// forceGC force gc container and image
func (c *CommitterImpl) forceGC(ctx context.Context) error {
	log.Printf("Starting force GC in namespace: %s", DefaultNamespace)
	ctx = namespaces.WithNamespace(ctx, DefaultNamespace)
	containers, err := c.containerdClient.Containers(ctx)
	if err != nil {
		log.Printf("Failed to get containers, err: %v", err)
		return err
	}

	// gc container
	containerNames := make([]string, 0)
	for _, container := range containers {
		containerNames = append(containerNames, container.ID())
	}
	if len(containerNames) > 0 {
		if err := c.RemoveContainers(ctx, containerNames); err != nil {
			log.Printf("Failed to remove containers, err: %v", err)
			return err
		}
	}

	// gc image
	images, err := c.containerdClient.ListImages(ctx)
	if err != nil {
		log.Printf("Failed to get images, err: %v", err)
		return err
	}
	imageNames := make([]string, 0)
	for _, image := range images {
		imageNames = append(imageNames, image.Name())
	}
	if len(imageNames) > 0 {
		if err := c.RemoveImages(
			ctx,
			imageNames,
			DefaultRemoveImageForce,
			DefaultRemoveImageAsync,
		); err != nil {
			log.Printf("Failed to remove images, err: %v", err)
			return err
		}
	}
	return nil
}

// GetResolver get resolver
func GetResolver(ctx context.Context, username, secret string) (remotes.Resolver, error) {
	resolverOptions := docker.ResolverOptions{
		Tracker: docker.NewInMemoryTracker(),
	}
	hostOptions := config.HostOptions{}
	if username == "" && secret == "" {
		hostOptions.Credentials = nil
	} else {
		// TODO: fix this, use flags or configs to set mulit registry credentials
		hostOptions.Credentials = func(host string) (string, string, error) {
			return username, secret, nil
		}
	}
	hostOptions.DefaultScheme = "http"
	hostOptions.DefaultTLS = nil
	resolverOptions.Hosts = config.ConfigureHosts(ctx, hostOptions)
	return docker.NewResolver(resolverOptions), nil
}

// convertLabels convert labels to "containerd.io/snapshot/devbox-" format
func convertLabels(labels map[string]string) map[string]string {
	convertedLabels := make(map[string]string)
	for key, value := range labels {
		if strings.HasPrefix(key, ContainerLabelPrefix) {
			// convert "devbox.sealos.io/" to "containerd.io/snapshot/devbox-"
			newKey := SnapshotLabelPrefix + key[len(ContainerLabelPrefix):]
			convertedLabels[newKey] = value
		}
	}
	return convertedLabels
}

// convertMapToSlice convert map to slice
func convertMapToSlice(labels map[string]string) []string {
	slice := make([]string, 0, len(labels))
	for key, value := range labels {
		slice = append(slice, fmt.Sprintf("%s=%s", key, value))
	}
	return slice
}

func newGlobalOptionConfigWithSnapshotter(snapshotter string) *types.GlobalCommandOptions {
	cfg := NewGlobalOptionConfig()
	if snapshotter != "" {
		cfg.Snapshotter = snapshotter
	}
	return cfg
}

// NewGlobalOptionConfig new global option config
func NewGlobalOptionConfig() *types.GlobalCommandOptions {
	return &types.GlobalCommandOptions{
		Namespace:        DefaultNamespace,
		Address:          DefaultContainerdAddress,
		DataRoot:         DefaultNerdctlDataRoot,
		Debug:            false,
		DebugFull:        false,
		Snapshotter:      DefaultDevboxSnapshotter,
		CNIPath:          ncdefaults.CNIPath(),
		CNINetConfPath:   ncdefaults.CNINetConfPath(),
		CgroupManager:    ncdefaults.CgroupManager(),
		InsecureRegistry: InsecureRegistry,
		HostsDir:         []string{DefaultNerdctlHostsDir},
		Experimental:     true,
		HostGatewayIP:    ncdefaults.HostGatewayIP(),
		KubeHideDupe:     false,
		CDISpecDirs:      ncdefaults.CDISpecDirs(),
		UsernsRemap:      "",
		DNS:              []string{},
		DNSOpts:          []string{},
		DNSSearch:        []string{},
	}
}

// CheckConnection check if the connection is still alive
func (c *CommitterImpl) CheckConnection(ctx context.Context) error {
	if c.conn == nil {
		return errors.New("connection is nil")
	}

	// check connection state
	state := c.conn.GetState()
	if state.String() == "TRANSIENT_FAILURE" || state.String() == "SHUTDOWN" {
		return fmt.Errorf("connection is in bad state: %s", state.String())
	}

	// try to ping containerd
	_, err := c.containerdClient.Version(ctx)
	if err != nil {
		return fmt.Errorf("failed to ping containerd: %w", err)
	}

	return nil
}

// Reconnect attempt to reconnect to containerd
func (c *CommitterImpl) Reconnect(ctx context.Context) error {
	log.Printf("Attempting to reconnect to containerd...")

	// close old connection
	if c.conn != nil {
		c.conn.Close()
	}

	var conn *grpc.ClientConn
	var err error

	for i := 0; i <= DefaultMaxRetries; i++ {
		if i > 0 {
			log.Printf("Retrying connection to containerd (attempt %d/%d)...", i, DefaultMaxRetries)
			time.Sleep(DefaultRetryDelay)
		}

		// create gRPC connection
		conn, err = grpc.NewClient(
			DefaultContainerdAddress,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err == nil {
			log.Printf("Successfully connected to containerd at %s", DefaultContainerdAddress)
			break
		}

		log.Printf(
			"Failed to connect to containerd (attempt %d/%d): %v",
			i+1,
			DefaultMaxRetries+1,
			err,
		)

		if i == DefaultMaxRetries {
			return fmt.Errorf(
				"failed to connect to containerd after %d attempts: %w",
				DefaultMaxRetries+1,
				err,
			)
		}
	}

	// recreate containerd client
	containerdClient, err := containerd.NewWithConn(
		conn,
		containerd.WithDefaultNamespace(DefaultNamespace),
	)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to recreate containerd client: %w", err)
	}

	// update instance
	c.containerdClient = containerdClient
	c.conn = conn

	log.Printf("Successfully reconnected to containerd")
	return nil
}

// Close close the connection
func (c *CommitterImpl) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
