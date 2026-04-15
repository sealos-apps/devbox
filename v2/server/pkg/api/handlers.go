package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	"github.com/sealos-apps/devbox/v2/controller/label"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultExecTimeoutSecond = 30
	maxExecTimeoutSecond     = 600
	defaultExecWorkingDir    = "/home/devbox/workspace"
	defaultFileTimeoutSecond = 300
	maxFileTimeoutSecond     = 3600
	jwtSecretCacheTTL        = 300 * time.Second
	defaultSDKServerPort     = 9757
	sdkServerStatusOperation = 1600
	sdkServerStdinEnvKey     = "SEALOS_DEVBOX_STDIN"

	devboxUpstreamIDLabelKey = "devbox.sealos.io/upstream-id"
	devboxLifecycleLabelKey  = "devbox.sealos.io/lifecycle-scheduled"
	devboxJWTSecretKey       = "SEALOS_DEVBOX_JWT_SECRET"

	devboxAnnotationPauseAt               = "devbox.sealos.io/pause-at"
	devboxAnnotationArchiveAfterPauseTime = "devbox.sealos.io/archive-after-pause-time"
	devboxAnnotationPausedAt              = "devbox.sealos.io/paused-at"
	devboxAnnotationPauseRefreshAt        = "devbox.sealos.io/pause-refresh-at"
	devboxAnnotationArchiveTriggeredAt    = "devbox.sealos.io/archive-triggered-at"
)

var (
	fileModeRegexp = regexp.MustCompile(`^[0-7]{3,4}$`)
)

type apiResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type createDevboxLabel struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type createDevboxRequest struct {
	Name                  string              `json:"name"`
	Image                 string              `json:"image,omitempty"`
	Labels                []createDevboxLabel `json:"labels,omitempty"`
	Env                   map[string]string   `json:"env,omitempty"`
	UpstreamID            string              `json:"upstreamID,omitempty"`
	PauseAt               string              `json:"pauseAt,omitempty"`
	ArchiveAfterPauseTime string              `json:"archiveAfterPauseTime,omitempty"`
}

type refreshPauseAtRequest struct {
	PauseAt string `json:"pauseAt"`
}

type devboxStateSummary struct {
	Spec   string `json:"spec"`
	Status string `json:"status"`
	Phase  string `json:"phase"`
}

type listDevboxItem struct {
	Name              string             `json:"name"`
	CreationTimestamp string             `json:"creationTimestamp"`
	DeletionTimestamp *string            `json:"deletionTimestamp"`
	State             devboxStateSummary `json:"state"`
}

type execDevboxRequest struct {
	Command        []string `json:"command"`
	Stdin          string   `json:"stdin,omitempty"`
	Cwd            string   `json:"cwd,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	Container      string   `json:"container,omitempty"`
}

type sdkServerExecSyncRequest struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type sdkServerExecSyncResponse struct {
	Status   int    `json:"status"`
	Message  string `json:"message"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode *int   `json:"exitCode"`
}

type sdkServerResponseEnvelope struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func (s *apiServer) routes() http.Handler {
	mux := http.NewServeMux()
	s.registerHealthzRoute(mux)
	s.registerGatewayRoutes(mux)
	s.registerAPIRoutes(mux)
	return s.requestLoggingMiddleware(mux)
}

func (s *apiServer) apiRoutes() http.Handler {
	mux := http.NewServeMux()
	s.registerHealthzRoute(mux)
	s.registerAPIRoutes(mux)
	return s.requestLoggingMiddleware(mux)
}

func (s *apiServer) gatewayRoutes() http.Handler {
	mux := http.NewServeMux()
	s.registerHealthzRoute(mux)
	s.registerGatewayRoutes(mux)
	return s.requestLoggingMiddleware(mux)
}

func (s *apiServer) registerHealthzRoute(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("GET /healthz", s.handleHealthz)
}

func (s *apiServer) registerGatewayRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	proxyPathPrefix := gatewayPathPrefix(s.cfg.Gateway)
	mux.Handle(proxyPathPrefix, http.HandlerFunc(s.handleGatewayProxy))
	if proxyPathPrefix != "/" {
		mux.Handle(proxyPathPrefix+"/", http.HandlerFunc(s.handleGatewayProxy))
	}
}

func (s *apiServer) registerAPIRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.Handle("POST /api/v1/devbox", s.authMiddleware(http.HandlerFunc(s.handleCreateDevbox)))
	mux.Handle("GET /api/v1/devbox", s.authMiddleware(http.HandlerFunc(s.handleListDevboxes)))
	mux.Handle("GET /api/v1/devbox/{name}", s.authMiddleware(http.HandlerFunc(s.handleGetDevboxInfo)))
	mux.Handle("POST /api/v1/devbox/{name}/pause/refresh", s.authMiddleware(http.HandlerFunc(s.handleRefreshDevboxPauseAt)))
	mux.Handle("POST /api/v1/devbox/{name}/pause", s.authMiddleware(http.HandlerFunc(s.handlePauseDevbox)))
	mux.Handle("POST /api/v1/devbox/{name}/resume", s.authMiddleware(http.HandlerFunc(s.handleResumeDevbox)))
	mux.Handle("DELETE /api/v1/devbox/{name}", s.authMiddleware(http.HandlerFunc(s.handleDestroyDevbox)))
	mux.Handle("POST /api/v1/devbox/{name}/exec", s.authMiddleware(http.HandlerFunc(s.handleExecDevbox)))
	mux.Handle("POST /api/v1/devbox/{name}/files/upload", s.authMiddleware(http.HandlerFunc(s.handleUploadFile)))
	mux.Handle("GET /api/v1/devbox/{name}/files/download", s.authMiddleware(http.HandlerFunc(s.handleDownloadFile)))
}

func (s *apiServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, "ok", map[string]string{"status": "healthy"})
}

func (s *apiServer) handleCreateDevbox(w http.ResponseWriter, r *http.Request) {
	var req createDevboxRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if errs := validation.IsDNS1123Subdomain(req.Name); len(errs) > 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid name: %s", strings.Join(errs, ",")))
		return
	}
	labels, err := parseCreateDevboxLabels(req.Labels)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	env, err := parseCreateDevboxEnv(req.Env)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	image, err := parseCreateDevboxImage(req.Image)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pauseAt, hasPauseAt, err := parseLifecycleTime(req.PauseAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid pauseAt: %v", err))
		return
	}
	archiveAfterPause, hasArchiveAfterPause, err := parseArchiveAfterPauseDuration(req.ArchiveAfterPauseTime)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid archiveAfterPauseTime: %v", err))
		return
	}
	upstreamID := strings.TrimSpace(req.UpstreamID)
	if upstreamID != "" {
		if errs := validation.IsValidLabelValue(upstreamID); len(errs) > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid upstreamID: %s", strings.Join(errs, ",")))
			return
		}
		if labels == nil {
			labels = make(map[string]string, 1)
		}
		labels[devboxUpstreamIDLabelKey] = upstreamID
	}
	if hasPauseAt || hasArchiveAfterPause {
		if labels == nil {
			labels = make(map[string]string, 1)
		}
		labels[devboxLifecycleLabelKey] = "true"
	}
	annotations := make(map[string]string, 2)
	if hasPauseAt {
		annotations[devboxAnnotationPauseAt] = pauseAt.UTC().Format(time.RFC3339)
	}
	if hasArchiveAfterPause {
		annotations[devboxAnnotationArchiveAfterPauseTime] = archiveAfterPause.String()
	}

	spec := defaultCreateDevboxSpec(s.cfg.CreateResource)
	if image != "" {
		spec.Image = image
	}
	spec.Config.Env = mergeCreateDevboxEnv(spec.Config.Env, env)

	devbox := &devboxv1alpha2.Devbox{
		TypeMeta: metav1.TypeMeta{
			APIVersion: devboxv1alpha2.GroupVersion.String(),
			Kind:       "Devbox",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        req.Name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: spec,
	}

	if err := s.ctrlClient.Create(r.Context(), devbox); err != nil {
		if apierrors.IsAlreadyExists(err) {
			s.logWarnError("create devbox conflict", err, "namespace", namespace, "name", req.Name, "http_status", http.StatusConflict)
			writeError(w, http.StatusConflict, "devbox already exists")
			return
		}
		s.logError("create devbox failed", err, "namespace", namespace, "name", req.Name)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create devbox failed: %v", err))
		return
	}
	s.logInfo("create devbox succeeded", "namespace", namespace, "name", req.Name, "state", devbox.Spec.State)
	if hasPauseAt || hasArchiveAfterPause {
		s.notifyLifecycleRunnerForKey(namespace, req.Name)
	}

	writeJSON(w, http.StatusCreated, "ok", map[string]interface{}{
		"name":      devbox.Name,
		"namespace": devbox.Namespace,
		"state":     devbox.Spec.State,
	})
}

func (s *apiServer) handleListDevboxes(w http.ResponseWriter, r *http.Request) {
	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	upstreamID := strings.TrimSpace(r.URL.Query().Get("upstreamID"))
	listOptions := []ctrlclient.ListOption{ctrlclient.InNamespace(namespace)}
	if upstreamID != "" {
		if errs := validation.IsValidLabelValue(upstreamID); len(errs) > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid upstreamID: %s", strings.Join(errs, ",")))
			return
		}
		listOptions = append(listOptions, ctrlclient.MatchingLabels{
			devboxUpstreamIDLabelKey: upstreamID,
		})
	}

	devboxList := &devboxv1alpha2.DevboxList{}
	if err := s.ctrlClient.List(r.Context(), devboxList, listOptions...); err != nil {
		s.logError("list devbox failed", err, "namespace", namespace, "upstream_id", upstreamID)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("list devbox failed: %v", err))
		return
	}

	items := make([]listDevboxItem, 0, len(devboxList.Items))
	for i := range devboxList.Items {
		devbox := &devboxList.Items[i]
		item := listDevboxItem{
			Name:              devbox.Name,
			CreationTimestamp: "",
			DeletionTimestamp: nil,
			State: devboxStateSummary{
				Spec:   string(devbox.Spec.State),
				Status: string(devbox.Status.State),
				Phase:  string(devbox.Status.Phase),
			},
		}
		if !devbox.CreationTimestamp.IsZero() {
			item.CreationTimestamp = devbox.CreationTimestamp.UTC().Format(time.RFC3339)
		}
		if devbox.DeletionTimestamp != nil && !devbox.DeletionTimestamp.IsZero() {
			value := devbox.DeletionTimestamp.UTC().Format(time.RFC3339)
			item.DeletionTimestamp = &value
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})

	s.logDebug("list devbox succeeded", "namespace", namespace, "upstream_id", upstreamID, "count", len(items))
	writeJSON(w, http.StatusOK, "ok", map[string]interface{}{"items": items})
}

func (s *apiServer) handleRefreshDevboxPauseAt(w http.ResponseWriter, r *http.Request) {
	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing devbox name")
		return
	}

	var req refreshPauseAtRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pauseAt, hasPauseAt, err := parseLifecycleTime(req.PauseAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid pauseAt: %v", err))
		return
	}
	if !hasPauseAt {
		writeError(w, http.StatusBadRequest, "pauseAt is required")
		return
	}

	key := ctrlclient.ObjectKey{Namespace: namespace, Name: name}
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &devboxv1alpha2.Devbox{}
		if getErr := s.ctrlClient.Get(r.Context(), key, latest); getErr != nil {
			return getErr
		}

		latest.SetAnnotations(withUpdatedLifecycleAnnotations(latest.GetAnnotations(), pauseAt.UTC(), time.Now().UTC()))
		latest.SetLabels(withLifecycleLabel(latest.GetLabels()))
		return s.ctrlClient.Update(r.Context(), latest)
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			s.logWarnError("refresh devbox pauseAt not found", err, "namespace", namespace, "name", name, "http_status", http.StatusNotFound)
			writeError(w, http.StatusNotFound, "devbox not found")
			return
		}
		s.logError("refresh devbox pauseAt failed", err, "namespace", namespace, "name", name)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("refresh devbox pauseAt failed: %v", err))
		return
	}

	s.notifyLifecycleRunnerForKey(namespace, name)
	s.logDebug("refresh devbox pauseAt succeeded", "namespace", namespace, "name", name, "pause_at", pauseAt.UTC().Format(time.RFC3339))
	writeJSON(w, http.StatusOK, "ok", map[string]interface{}{
		"name":        name,
		"namespace":   namespace,
		"pauseAt":     pauseAt.UTC().Format(time.RFC3339),
		"refreshedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func parseCreateDevboxLabels(items []createDevboxLabel) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}

	labels := make(map[string]string, len(items))
	for i, item := range items {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			return nil, fmt.Errorf("labels[%d].key is required", i)
		}
		if errs := validation.IsQualifiedName(key); len(errs) > 0 {
			return nil, fmt.Errorf("invalid labels[%d].key: %s", i, strings.Join(errs, ","))
		}
		if _, exists := labels[key]; exists {
			return nil, fmt.Errorf("duplicate labels[%d].key: %s", i, key)
		}

		value := item.Value
		if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
			return nil, fmt.Errorf("invalid labels[%d].value: %s", i, strings.Join(errs, ","))
		}
		labels[key] = value
	}
	return labels, nil
}

func parseCreateDevboxEnv(items map[string]string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}

	env := make(map[string]string, len(items))
	for rawName, value := range items {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("env key is required")
		}
		if errs := validation.IsEnvVarName(name); len(errs) > 0 {
			return nil, fmt.Errorf("invalid env %q: %s", name, strings.Join(errs, ","))
		}
		env[name] = value
	}
	return env, nil
}

func parseCreateDevboxImage(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	image := strings.TrimSpace(raw)
	if image == "" {
		return "", fmt.Errorf("image must not be blank")
	}
	return image, nil
}

func mergeCreateDevboxEnv(base []corev1.EnvVar, extra map[string]string) []corev1.EnvVar {
	if len(extra) == 0 {
		return base
	}

	out := make([]corev1.EnvVar, len(base))
	copy(out, base)
	indexByName := make(map[string]int, len(base))
	for i := range out {
		indexByName[out[i].Name] = i
	}

	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := extra[key]
		if idx, exists := indexByName[key]; exists {
			out[idx].Value = value
			out[idx].ValueFrom = nil
			continue
		}
		out = append(out, corev1.EnvVar{
			Name:  key,
			Value: value,
		})
	}
	return out
}

func parseLifecycleTime(raw string) (time.Time, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("must be RFC3339 format")
	}
	return parsed.UTC(), true, nil
}

func parseArchiveAfterPauseDuration(raw string) (time.Duration, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, false, err
	}
	if parsed <= 0 {
		return 0, false, fmt.Errorf("must be greater than 0")
	}
	return parsed, true, nil
}

func withLifecycleLabel(in map[string]string) map[string]string {
	if in == nil {
		in = make(map[string]string, 1)
	}
	if in[devboxLifecycleLabelKey] == "true" {
		return in
	}
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	out[devboxLifecycleLabelKey] = "true"
	return out
}

func withUpdatedLifecycleAnnotations(in map[string]string, pauseAt time.Time, refreshedAt time.Time) map[string]string {
	out := make(map[string]string, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	out[devboxAnnotationPauseAt] = pauseAt.UTC().Format(time.RFC3339)
	out[devboxAnnotationPauseRefreshAt] = refreshedAt.UTC().Format(time.RFC3339)
	delete(out, devboxAnnotationPausedAt)
	delete(out, devboxAnnotationArchiveTriggeredAt)
	return out
}

func (s *apiServer) handleGetDevboxInfo(w http.ResponseWriter, r *http.Request) {
	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing devbox name")
		return
	}

	devbox := &devboxv1alpha2.Devbox{}
	if err := s.ctrlClient.Get(r.Context(), ctrlclient.ObjectKey{Namespace: namespace, Name: name}, devbox); err != nil {
		if apierrors.IsNotFound(err) {
			s.logWarnError("get devbox info not found", err, "namespace", namespace, "name", name, "http_status", http.StatusNotFound)
			writeError(w, http.StatusNotFound, "devbox not found")
			return
		}
		s.logError("get devbox info failed", err, "namespace", namespace, "name", name)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("get devbox info failed: %v", err))
		return
	}

	privateKeyBase64, err := s.loadDevboxPrivateKeyBase64(r.Context(), namespace, name)
	if err != nil {
		s.logError("get devbox private key failed", err, "namespace", namespace, "name", name)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("get devbox private key failed: %v", err))
		return
	}
	devboxJWTSecret, err := s.loadDevboxJWTSecret(r.Context(), namespace, name)
	if err != nil {
		s.logError("get devbox jwt secret failed", err, "namespace", namespace, "name", name)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("get devbox jwt secret failed: %v", err))
		return
	}
	s.syncGatewayIndex(devbox)

	sshInfo := buildConfiguredSSHInfo(s.cfg.SSH, privateKeyBase64)
	gatewayInfo, hasGatewayRoute := buildGatewayInfo(
		s.cfg.Gateway,
		devbox.Status.Network.UniqueID,
		devboxJWTSecret,
	)
	creationTimestamp := ""
	if !devbox.CreationTimestamp.IsZero() {
		creationTimestamp = devbox.CreationTimestamp.UTC().Format(time.RFC3339)
	}
	var deletionTimestamp interface{}
	if devbox.DeletionTimestamp != nil && !devbox.DeletionTimestamp.IsZero() {
		deletionTimestamp = devbox.DeletionTimestamp.UTC().Format(time.RFC3339)
	}

	data := map[string]interface{}{
		"name":              devbox.Name,
		"creationTimestamp": creationTimestamp,
		"deletionTimestamp": deletionTimestamp,
		"state": map[string]string{
			"spec":   string(devbox.Spec.State),
			"status": string(devbox.Status.State),
			"phase":  string(devbox.Status.Phase),
		},
		"ssh": sshInfo,
	}
	if hasGatewayRoute {
		data["gateway"] = gatewayInfo
	}

	s.logDebug("get devbox info succeeded", "namespace", namespace, "name", name)
	writeJSON(w, http.StatusOK, "ok", data)
}

func (s *apiServer) handlePauseDevbox(w http.ResponseWriter, r *http.Request) {
	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing devbox name")
		return
	}

	key := ctrlclient.ObjectKey{Namespace: namespace, Name: name}
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &devboxv1alpha2.Devbox{}
		if err := s.ctrlClient.Get(r.Context(), key, latest); err != nil {
			return err
		}

		if latest.Spec.State == devboxv1alpha2.DevboxStatePaused {
			return nil
		}
		latest.Spec.State = devboxv1alpha2.DevboxStatePaused
		return s.ctrlClient.Update(r.Context(), latest)
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			s.logWarnError("pause devbox not found", err, "namespace", namespace, "name", name, "http_status", http.StatusNotFound)
			writeError(w, http.StatusNotFound, "devbox not found")
			return
		}
		s.logError("pause devbox failed", err, "namespace", namespace, "name", name)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("pause devbox failed: %v", err))
		return
	}
	s.logInfo("pause devbox succeeded", "namespace", namespace, "name", name)
	s.notifyLifecycleRunnerForKey(namespace, name)

	writeJSON(w, http.StatusOK, "ok", map[string]interface{}{
		"name":      name,
		"namespace": namespace,
		"state":     devboxv1alpha2.DevboxStatePaused,
	})
}

func (s *apiServer) handleResumeDevbox(w http.ResponseWriter, r *http.Request) {
	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing devbox name")
		return
	}

	key := ctrlclient.ObjectKey{Namespace: namespace, Name: name}
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &devboxv1alpha2.Devbox{}
		if err := s.ctrlClient.Get(r.Context(), key, latest); err != nil {
			return err
		}

		if latest.Spec.State == devboxv1alpha2.DevboxStateRunning {
			return nil
		}
		latest.Spec.State = devboxv1alpha2.DevboxStateRunning
		return s.ctrlClient.Update(r.Context(), latest)
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			s.logWarnError("resume devbox not found", err, "namespace", namespace, "name", name, "http_status", http.StatusNotFound)
			writeError(w, http.StatusNotFound, "devbox not found")
			return
		}
		s.logError("resume devbox failed", err, "namespace", namespace, "name", name)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("resume devbox failed: %v", err))
		return
	}
	s.logInfo("resume devbox succeeded", "namespace", namespace, "name", name)
	s.notifyLifecycleRunnerForKey(namespace, name)

	writeJSON(w, http.StatusOK, "ok", map[string]interface{}{
		"name":      name,
		"namespace": namespace,
		"state":     devboxv1alpha2.DevboxStateRunning,
	})
}

func (s *apiServer) handleDestroyDevbox(w http.ResponseWriter, r *http.Request) {
	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing devbox name")
		return
	}

	obj := &devboxv1alpha2.Devbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if err := s.ctrlClient.Delete(r.Context(), obj); err != nil {
		if apierrors.IsNotFound(err) {
			s.logWarnError("destroy devbox not found", err, "namespace", namespace, "name", name, "http_status", http.StatusNotFound)
			writeError(w, http.StatusNotFound, "devbox not found")
			return
		}
		s.logError("destroy devbox failed", err, "namespace", namespace, "name", name)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("destroy devbox failed: %v", err))
		return
	}
	s.logInfo("destroy devbox requested", "namespace", namespace, "name", name)
	s.notifyLifecycleRunnerForKey(namespace, name)

	writeJSON(w, http.StatusOK, "ok", map[string]string{
		"name":      name,
		"namespace": namespace,
		"status":    "deletion requested",
	})
}

func (s *apiServer) handleExecDevbox(w http.ResponseWriter, r *http.Request) {
	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing devbox name")
		return
	}

	var req execDevboxRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Command) == 0 {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}
	for _, arg := range req.Command {
		if strings.TrimSpace(arg) == "" {
			writeError(w, http.StatusBadRequest, "command contains empty argument")
			return
		}
	}

	if req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = defaultExecTimeoutSecond
	}
	if req.TimeoutSeconds < 0 || req.TimeoutSeconds > maxExecTimeoutSecond {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("timeoutSeconds must be in [1, %d]", maxExecTimeoutSecond))
		return
	}

	if req.Cwd == "" {
		req.Cwd = defaultExecWorkingDir
	}

	pod, container, sdkServerBaseURL, sdkServerToken, err := s.resolveRunningPodAndSDKServer(
		r.Context(),
		namespace,
		name,
		req.Container,
	)
	if err != nil {
		s.logPodResolveError(err, "namespace", namespace, "name", name, "container", strings.TrimSpace(req.Container))
		s.writePodResolveError(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutSeconds)*time.Second)
	defer cancel()

	execReq := buildSDKServerExecSyncRequest(req)

	execBody, err := json.Marshal(execReq)
	if err != nil {
		s.logError("marshal sdk server exec request failed", err, "namespace", namespace, "name", name)
		writeError(w, http.StatusInternalServerError, "build exec request failed")
		return
	}

	upstreamReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		sdkServerBaseURL+"/api/v1/process/exec-sync",
		bytes.NewReader(execBody),
	)
	if err != nil {
		s.logError("create sdk server exec request failed", err, "namespace", namespace, "name", name)
		writeError(w, http.StatusInternalServerError, "create exec request failed")
		return
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+sdkServerToken)
	upstreamReq.Header.Set("Content-Type", "application/json")

	upstreamResp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.logWarnError("exec command timeout", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "http_status", http.StatusGatewayTimeout)
			writeError(w, http.StatusGatewayTimeout, "exec command timeout")
			return
		}
		s.logError("exec command via sdk server failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("exec command failed: %v", err))
		return
	}
	defer upstreamResp.Body.Close()

	if upstreamResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(upstreamResp.Body, 1024*1024))
		errMsg := strings.TrimSpace(string(body))
		if errMsg == "" {
			errMsg = "exec command failed"
		}
		httpStatus := s.logSDKServerHTTPError("exec command sdk server http error", fmt.Errorf("status %d: %s", upstreamResp.StatusCode, errMsg), upstreamResp.StatusCode, "namespace", namespace, "name", name, "pod", pod.Name, "container", container)
		writeError(w, httpStatus, errMsg)
		return
	}

	var execResp sdkServerExecSyncResponse
	if err := json.NewDecoder(io.LimitReader(upstreamResp.Body, 1024*1024)).Decode(&execResp); err != nil {
		s.logError("decode sdk server exec response failed", err, "namespace", namespace, "name", name, "pod", pod.Name)
		writeError(w, http.StatusInternalServerError, "invalid exec response from sdk server")
		return
	}

	if execResp.Status != 0 && execResp.Status != sdkServerStatusOperation {
		if isSDKServerTimeout(execResp.Status, execResp.Message) {
			writeError(w, http.StatusGatewayTimeout, "exec command timeout")
			return
		}
		httpStatus := mapSDKServerBusinessStatus(execResp.Status)
		errMsg := strings.TrimSpace(execResp.Message)
		if errMsg == "" {
			errMsg = "exec command failed"
		}
		s.logHTTPStatusError("exec command sdk server returned business error", fmt.Errorf("status=%d message=%s", execResp.Status, errMsg), httpStatus, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "sdk_status", execResp.Status)
		writeError(w, httpStatus, errMsg)
		return
	}

	exitCode := 0
	if execResp.ExitCode != nil {
		exitCode = *execResp.ExitCode
	} else if execResp.Status == sdkServerStatusOperation {
		exitCode = -1
	}
	s.logDebug(
		"exec command completed",
		"namespace", namespace,
		"name", name,
		"pod", pod.Name,
		"container", container,
		"exit_code", exitCode,
		"command", strings.Join(req.Command, " "),
	)

	writeJSON(w, http.StatusOK, "ok", map[string]interface{}{
		"podName":    pod.Name,
		"namespace":  namespace,
		"container":  container,
		"command":    req.Command,
		"exitCode":   exitCode,
		"stdout":     execResp.Stdout,
		"stderr":     execResp.Stderr,
		"executedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *apiServer) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing devbox name")
		return
	}

	filePath, err := parseFilePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	timeoutSeconds, err := parseTransferTimeoutSeconds(
		r.URL.Query().Get("timeoutSeconds"),
		defaultFileTimeoutSecond,
		maxFileTimeoutSecond,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fileMode := strings.TrimSpace(r.URL.Query().Get("mode"))
	if fileMode != "" && !fileModeRegexp.MatchString(fileMode) {
		writeError(w, http.StatusBadRequest, "invalid mode: must be octal digits like 644 or 0644")
		return
	}

	pod, container, sdkServerBaseURL, sdkServerToken, err := s.resolveRunningPodAndSDKServer(
		r.Context(),
		namespace,
		name,
		r.URL.Query().Get("container"),
	)
	if err != nil {
		s.logPodResolveError(err, "namespace", namespace, "name", name, "container", strings.TrimSpace(r.URL.Query().Get("container")), "path", filePath)
		s.writePodResolveError(w, err)
		return
	}

	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "request body is empty")
		return
	}
	defer r.Body.Close()

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	countingBody := &countingReader{Reader: r.Body}
	upstreamReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		sdkServerBaseURL+"/api/v1/files/write?path="+neturl.QueryEscape(filePath),
		countingBody,
	)
	if err != nil {
		s.logError("create sdk server upload request failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "path", filePath)
		writeError(w, http.StatusInternalServerError, "create upload request failed")
		return
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+sdkServerToken)
	upstreamReq.Header.Set("Content-Type", "application/octet-stream")

	upstreamResp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.logWarnError("upload file timeout", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "http_status", http.StatusGatewayTimeout)
			writeError(w, http.StatusGatewayTimeout, "upload file timeout")
			return
		}
		s.logError("upload file via sdk server failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "path", filePath)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("upload file failed: %v", err))
		return
	}
	defer upstreamResp.Body.Close()

	if upstreamResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(upstreamResp.Body, 1024*1024))
		errMsg := strings.TrimSpace(string(body))
		if errMsg == "" {
			errMsg = "upload file failed"
		}
		httpStatus := s.logSDKServerHTTPError("upload file sdk server http error", fmt.Errorf("status %d: %s", upstreamResp.StatusCode, errMsg), upstreamResp.StatusCode, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath)
		writeError(w, httpStatus, errMsg)
		return
	}

	var uploadResp sdkServerResponseEnvelope
	if err := json.NewDecoder(io.LimitReader(upstreamResp.Body, 1024*1024)).Decode(&uploadResp); err != nil {
		s.logError("decode sdk server upload response failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "path", filePath)
		writeError(w, http.StatusInternalServerError, "invalid upload response from sdk server")
		return
	}
	if uploadResp.Status != 0 {
		httpStatus := mapSDKServerBusinessStatus(uploadResp.Status)
		errMsg := strings.TrimSpace(uploadResp.Message)
		if errMsg == "" {
			errMsg = "upload file failed"
		}
		s.logHTTPStatusError("upload file sdk server returned business error", fmt.Errorf("status=%d message=%s", uploadResp.Status, errMsg), httpStatus, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "sdk_status", uploadResp.Status)
		writeError(w, httpStatus, errMsg)
		return
	}

	if fileMode != "" {
		chmodReqBody, err := json.Marshal(map[string]interface{}{
			"path": filePath,
			"mode": fileMode,
		})
		if err != nil {
			s.logError("build chmod request failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "mode", fileMode)
			writeError(w, http.StatusInternalServerError, "build chmod request failed")
			return
		}

		chmodReq, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			sdkServerBaseURL+"/api/v1/files/chmod",
			bytes.NewReader(chmodReqBody),
		)
		if err != nil {
			s.logError("create chmod request failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "mode", fileMode)
			writeError(w, http.StatusInternalServerError, "create chmod request failed")
			return
		}
		chmodReq.Header.Set("Authorization", "Bearer "+sdkServerToken)
		chmodReq.Header.Set("Content-Type", "application/json")

		chmodResp, err := http.DefaultClient.Do(chmodReq)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.logWarnError("chmod after upload timeout", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "mode", fileMode, "http_status", http.StatusGatewayTimeout)
				writeError(w, http.StatusGatewayTimeout, "chmod timeout")
				return
			}
			s.logError("chmod after upload failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "path", filePath, "mode", fileMode)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("chmod failed: %v", err))
			return
		}
		defer chmodResp.Body.Close()

		if chmodResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(chmodResp.Body, 1024*1024))
			errMsg := strings.TrimSpace(string(body))
			if errMsg == "" {
				errMsg = "chmod failed"
			}
			httpStatus := s.logSDKServerHTTPError("chmod after upload sdk server http error", fmt.Errorf("status %d: %s", chmodResp.StatusCode, errMsg), chmodResp.StatusCode, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "mode", fileMode)
			writeError(w, httpStatus, errMsg)
			return
		}

		var chmodResult sdkServerResponseEnvelope
		if err := json.NewDecoder(io.LimitReader(chmodResp.Body, 1024*1024)).Decode(&chmodResult); err != nil {
			s.logError("decode chmod response failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "mode", fileMode)
			writeError(w, http.StatusInternalServerError, "invalid chmod response from sdk server")
			return
		}
		if chmodResult.Status != 0 {
			errMsg := strings.TrimSpace(chmodResult.Message)
			if errMsg == "" {
				errMsg = "chmod failed"
			}
			httpStatus := mapSDKServerBusinessStatus(chmodResult.Status)
			s.logHTTPStatusError("chmod after upload sdk server returned business error", fmt.Errorf("status=%d message=%s", chmodResult.Status, errMsg), httpStatus, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "mode", fileMode, "sdk_status", chmodResult.Status)
			writeError(w, httpStatus, errMsg)
			return
		}
	}
	s.logDebug("upload file succeeded", "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "size_bytes", countingBody.N, "mode", fileMode)

	writeJSON(w, http.StatusOK, "ok", map[string]interface{}{
		"name":          name,
		"namespace":     namespace,
		"podName":       pod.Name,
		"container":     container,
		"path":          filePath,
		"sizeBytes":     countingBody.N,
		"mode":          fileMode,
		"uploadedAt":    time.Now().UTC().Format(time.RFC3339),
		"timeoutSecond": timeoutSeconds,
	})
}

func (s *apiServer) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	namespace, err := s.resolveNamespace(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing devbox name")
		return
	}

	filePath, err := parseFilePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	timeoutSeconds, err := parseTransferTimeoutSeconds(
		r.URL.Query().Get("timeoutSeconds"),
		defaultFileTimeoutSecond,
		maxFileTimeoutSecond,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	pod, container, sdkServerBaseURL, sdkServerToken, err := s.resolveRunningPodAndSDKServer(
		r.Context(),
		namespace,
		name,
		r.URL.Query().Get("container"),
	)
	if err != nil {
		s.logPodResolveError(err, "namespace", namespace, "name", name, "container", strings.TrimSpace(r.URL.Query().Get("container")), "path", filePath)
		s.writePodResolveError(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	upstreamReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		sdkServerBaseURL+"/api/v1/files/read?path="+neturl.QueryEscape(filePath),
		nil,
	)
	if err != nil {
		s.logError("create sdk server download request failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "path", filePath)
		writeError(w, http.StatusInternalServerError, "create download request failed")
		return
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+sdkServerToken)

	upstreamResp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.logWarnError("download file timeout", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "http_status", http.StatusGatewayTimeout)
			writeError(w, http.StatusGatewayTimeout, "download file timeout")
			return
		}
		s.logError("download file via sdk server failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "path", filePath)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("download file failed: %v", err))
		return
	}
	defer upstreamResp.Body.Close()

	if upstreamResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(upstreamResp.Body, 1024*1024))
		errMsg := strings.TrimSpace(string(body))
		if errMsg == "" {
			errMsg = "download file failed"
		}
		httpStatus := s.logSDKServerHTTPError("download file sdk server http error", fmt.Errorf("status %d: %s", upstreamResp.StatusCode, errMsg), upstreamResp.StatusCode, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath)
		writeError(w, httpStatus, errMsg)
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(upstreamResp.Header.Get("Content-Type")))
	if strings.Contains(contentType, "application/json") {
		var bizResp sdkServerResponseEnvelope
		if err := json.NewDecoder(io.LimitReader(upstreamResp.Body, 1024*1024)).Decode(&bizResp); err != nil {
			s.logError("decode download response failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath)
			writeError(w, http.StatusInternalServerError, "invalid download response from sdk server")
			return
		}
		if bizResp.Status != 0 {
			errMsg := strings.TrimSpace(bizResp.Message)
			if errMsg == "" {
				errMsg = "download file failed"
			}
			httpStatus := mapSDKServerBusinessStatus(bizResp.Status)
			s.logHTTPStatusError("download file sdk server returned business error", fmt.Errorf("status=%d message=%s", bizResp.Status, errMsg), httpStatus, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "sdk_status", bizResp.Status)
			writeError(w, httpStatus, errMsg)
			return
		}
		s.logError("download file sdk server returned unexpected json payload", fmt.Errorf("missing file content"), "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath)
		writeError(w, http.StatusInternalServerError, "unexpected json response from sdk server")
		return
	}

	fileName := sanitizeDownloadName(r.URL.Query().Get("filename"), filePath)
	w.Header().Set("Content-Type", "application/octet-stream")
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if contentLength := strings.TrimSpace(upstreamResp.Header.Get("Content-Length")); contentLength != "" {
		w.Header().Set("Content-Length", contentLength)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	w.Header().Set("X-Devbox-Path", filePath)

	tracker := &countingResponseWriter{ResponseWriter: w}
	if _, err := io.Copy(tracker, upstreamResp.Body); err != nil {
		if tracker.WrittenBytes == 0 {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.logWarnError("download file stream timeout", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "http_status", http.StatusGatewayTimeout)
				writeError(w, http.StatusGatewayTimeout, "download file timeout")
				return
			}
			s.logError("download file stream failed", err, "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("download file failed: %v", err))
			return
		}
		s.logError("download file copy interrupted", err, "namespace", namespace, "name", name, "pod", pod.Name, "path", filePath, "written_bytes", tracker.WrittenBytes)
		return
	}

	s.logDebug("download file succeeded", "namespace", namespace, "name", name, "pod", pod.Name, "container", container, "path", filePath, "size_bytes", tracker.WrittenBytes)
}

func (s *apiServer) findDevboxPod(
	ctx context.Context,
	namespace string,
	devboxName string,
) (*corev1.Pod, error) {
	pod := &corev1.Pod{}
	if err := s.ctrlClient.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: devboxName}, pod); err == nil {
		return pod, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}
	podList := &corev1.PodList{}
	if err := s.ctrlClient.List(
		ctx,
		podList,
		ctrlclient.InNamespace(namespace),
		ctrlclient.MatchingLabels{
			label.AppName:   devboxName,
			label.AppPartOf: devboxv1alpha2.LabelDevBoxPartOf,
		},
	); err != nil {
		return nil, err
	}

	if len(podList.Items) == 0 {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, devboxName)
	}

	var candidate *corev1.Pod
	for i := range podList.Items {
		p := &podList.Items[i]
		if p.Status.Phase == corev1.PodRunning {
			return p, nil
		}
		if candidate == nil {
			candidate = p
		}
	}
	return candidate, nil
}

func podContainsContainer(pod *corev1.Pod, container string) bool {
	for _, c := range pod.Spec.Containers {
		if c.Name == container {
			return true
		}
	}
	return false
}

func decodeJSON(r *http.Request, out interface{}) error {
	defer r.Body.Close()
	reader := io.LimitReader(r.Body, 1024*1024)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid request body: must contain only one json object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, statusCode int, message string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(apiResponse{
		Code:    statusCode,
		Message: message,
		Data:    data,
	})
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, message, nil)
}

func (s *apiServer) resolveNamespace(r *http.Request) (string, error) {
	tokenNamespace, ok := namespaceFromContext(r.Context())
	if !ok {
		return "", fmt.Errorf("namespace is missing in token claims")
	}

	if errs := validation.IsDNS1123Label(tokenNamespace); len(errs) > 0 {
		return "", fmt.Errorf("invalid namespace in token claims: %s", strings.Join(errs, ","))
	}
	return tokenNamespace, nil
}

func parseFilePath(raw string) (string, error) {
	filePath := strings.TrimSpace(raw)
	if filePath == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.Contains(filePath, "\x00") {
		return "", fmt.Errorf("path contains invalid null character")
	}
	return filePath, nil
}

func (s *apiServer) loadDevboxPrivateKeyBase64(ctx context.Context, namespace string, devboxName string) (string, error) {
	secret := &corev1.Secret{}
	if err := s.ctrlClient.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: devboxName}, secret); err != nil {
		return "", err
	}

	keyName := strings.TrimSpace(s.cfg.SSH.PrivateKeySecretKey)
	key := secret.Data[keyName]
	if len(key) == 0 {
		return "", fmt.Errorf("secret %s/%s missing key %q", namespace, devboxName, keyName)
	}

	return base64.StdEncoding.EncodeToString(key), nil
}

func (s *apiServer) loadDevboxJWTSecret(ctx context.Context, namespace string, devboxName string) (string, error) {
	cacheKey := devboxJWTSecretCacheKey(namespace, devboxName)
	return s.loadDevboxJWTSecretByCacheKey(ctx, namespace, devboxName, cacheKey)
}

func (s *apiServer) loadDevboxJWTSecretByCacheKey(ctx context.Context, namespace string, devboxName string, cacheKey string) (string, error) {
	now := time.Now()
	if token, ok := s.getCachedDevboxJWTSecret(cacheKey, now); ok {
		return token, nil
	}

	secret := &corev1.Secret{}
	if err := s.ctrlClient.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: devboxName}, secret); err != nil {
		return "", err
	}

	key := strings.TrimSpace(string(secret.Data[devboxJWTSecretKey]))
	if key == "" {
		return "", fmt.Errorf("secret %s/%s missing key %q", namespace, devboxName, devboxJWTSecretKey)
	}
	s.storeDevboxJWTSecret(cacheKey, key, now.Add(jwtSecretCacheTTL))
	return key, nil
}

func devboxJWTSecretCacheKey(namespace, devboxName string) string {
	return namespace + "/" + devboxName
}

func (s *apiServer) getCachedDevboxJWTSecret(cacheKey string, now time.Time) (string, bool) {
	s.jwtSecretCacheMu.RLock()
	entry, ok := s.jwtSecretCache[cacheKey]
	s.jwtSecretCacheMu.RUnlock()
	if !ok {
		return "", false
	}

	if now.After(entry.expiresAt) {
		s.jwtSecretCacheMu.Lock()
		if latest, ok := s.jwtSecretCache[cacheKey]; ok && now.After(latest.expiresAt) {
			delete(s.jwtSecretCache, cacheKey)
		}
		s.jwtSecretCacheMu.Unlock()
		return "", false
	}
	return entry.token, true
}

func (s *apiServer) storeDevboxJWTSecret(cacheKey string, token string, expiresAt time.Time) {
	s.jwtSecretCacheMu.Lock()
	if s.jwtSecretCache == nil {
		s.jwtSecretCache = make(map[string]jwtSecretCacheEntry, 128)
	}
	s.jwtSecretCache[cacheKey] = jwtSecretCacheEntry{
		token:     token,
		expiresAt: expiresAt,
	}
	s.jwtSecretCacheMu.Unlock()
}

func (s *apiServer) resolveRunningPodAndSDKServer(
	ctx context.Context,
	namespace string,
	devboxName string,
	containerRaw string,
) (*corev1.Pod, string, string, string, error) {
	pod, container, err := s.resolveRunningPodAndContainer(ctx, namespace, devboxName, containerRaw)
	if err != nil {
		return nil, "", "", "", err
	}

	podIP := strings.TrimSpace(pod.Status.PodIP)
	if podIP == "" {
		return nil, "", "", "", fmt.Errorf("devbox pod has empty podIP")
	}

	cacheKey, err := s.resolveDevboxJWTSecretCacheKey(ctx, namespace, devboxName)
	if err != nil {
		return nil, "", "", "", err
	}

	token, err := s.loadDevboxJWTSecretByCacheKey(ctx, namespace, devboxName, cacheKey)
	if err != nil {
		return nil, "", "", "", err
	}

	baseURL := "http://" + net.JoinHostPort(podIP, strconv.Itoa(defaultSDKServerPort))
	return pod, container, baseURL, token, nil
}

func (s *apiServer) resolveDevboxJWTSecretCacheKey(ctx context.Context, namespace string, devboxName string) (string, error) {
	devbox := &devboxv1alpha2.Devbox{}
	if err := s.ctrlClient.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: devboxName}, devbox); err != nil {
		return "", err
	}

	devboxUID := strings.TrimSpace(string(devbox.UID))
	if devboxUID == "" {
		return "", fmt.Errorf("devbox %s/%s has empty uid", namespace, devboxName)
	}
	return devboxJWTSecretCacheKey(namespace, devboxUID), nil
}

func mapSDKServerHTTPStatus(status int) int {
	switch status {
	case http.StatusBadRequest,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusGatewayTimeout:
		return status
	default:
		return http.StatusInternalServerError
	}
}

func mapSDKServerBusinessStatus(status int) int {
	switch status {
	case 1400, 1422:
		return http.StatusBadRequest
	case 1404:
		return http.StatusNotFound
	case 1409:
		return http.StatusConflict
	case sdkServerStatusOperation:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func (s *apiServer) logHTTPStatusError(message string, err error, httpStatus int, kv ...interface{}) {
	fields := appendLogFields(kv, "http_status", httpStatus)
	if httpStatus >= http.StatusInternalServerError {
		s.logError(message, err, fields...)
		return
	}
	s.logWarnError(message, err, fields...)
}

func (s *apiServer) logSDKServerHTTPError(message string, err error, upstreamStatus int, kv ...interface{}) int {
	httpStatus := mapSDKServerHTTPStatus(upstreamStatus)
	s.logHTTPStatusError(message, err, httpStatus, appendLogFields(kv, "upstream_status", upstreamStatus)...)
	return httpStatus
}

func appendLogFields(base []interface{}, extra ...interface{}) []interface{} {
	fields := make([]interface{}, 0, len(base)+len(extra))
	fields = append(fields, base...)
	fields = append(fields, extra...)
	return fields
}

func buildSDKServerExecSyncRequest(req execDevboxRequest) sdkServerExecSyncRequest {
	cwd := req.Cwd
	if strings.TrimSpace(cwd) == "" {
		cwd = defaultExecWorkingDir
	}

	execReq := sdkServerExecSyncRequest{
		Command: req.Command[0],
		Cwd:     cwd,
		Timeout: req.TimeoutSeconds,
	}
	if len(req.Command) > 1 {
		execReq.Args = req.Command[1:]
	}

	if req.Stdin == "" {
		return execReq
	}

	args := make([]string, 0, len(req.Command)+3)
	args = append(args, "-c", `printf '%s' "$SEALOS_DEVBOX_STDIN" | exec "$@"`, "--")
	args = append(args, req.Command...)
	execReq.Command = "/bin/sh"
	execReq.Args = args
	execReq.Env = map[string]string{
		sdkServerStdinEnvKey: req.Stdin,
	}
	return execReq
}

func isSDKServerTimeout(status int, message string) bool {
	if status != 1500 {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out")
}

func buildConfiguredSSHInfo(cfg SSHConnectionConfig, privateKeyBase64 string) map[string]interface{} {
	target := fmt.Sprintf("%s@%s -p %d", cfg.User, cfg.Host, cfg.Port)
	return map[string]interface{}{
		"user":               cfg.User,
		"host":               cfg.Host,
		"port":               cfg.Port,
		"target":             target,
		"link":               fmt.Sprintf("ssh://%s@%s:%d", cfg.User, cfg.Host, cfg.Port),
		"command":            fmt.Sprintf("ssh -i <private-key-file> %s", target),
		"privateKeyEncoding": "base64",
		"privateKeyBase64":   privateKeyBase64,
	}
}

func buildGatewayInfo(
	cfg GatewayConfig,
	uniqueID string,
	devboxJWTSecret string,
) (map[string]interface{}, bool) {
	ssePath := gatewaySSEPath(cfg)
	route, sseURL, hasRoute := buildGatewayURLs(cfg, uniqueID)

	info := map[string]interface{}{
		"url":     route,
		"sseURL":  sseURL,
		"token":   devboxJWTSecret,
		"port":    gatewayPort(cfg),
		"ssePath": ssePath,
	}
	if uniqueID != "" {
		info["uniqueID"] = uniqueID
	}
	return info, hasRoute
}

func parseTransferTimeoutSeconds(raw string, defaultValue int, max int) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return defaultValue, nil
	}
	timeoutSeconds, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid timeoutSeconds: %w", err)
	}
	if timeoutSeconds <= 0 || timeoutSeconds > max {
		return 0, fmt.Errorf("timeoutSeconds must be in [1, %d]", max)
	}
	return timeoutSeconds, nil
}

func sanitizeDownloadName(preferredName string, sourcePath string) string {
	name := strings.TrimSpace(preferredName)
	if name == "" {
		name = path.Base(sourcePath)
	}
	name = path.Base(name)
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	if name == "" || name == "." || name == "/" {
		return "download.bin"
	}
	return name
}

func (s *apiServer) resolveRunningPodAndContainer(
	ctx context.Context,
	namespace string,
	devboxName string,
	containerRaw string,
) (*corev1.Pod, string, error) {
	pod, err := s.findDevboxPod(ctx, namespace, devboxName)
	if err != nil {
		return nil, "", err
	}
	if pod.Status.Phase != corev1.PodRunning {
		return nil, "", fmt.Errorf("devbox pod is not running: %s", pod.Status.Phase)
	}

	container := strings.TrimSpace(containerRaw)
	if container == "" {
		if len(pod.Spec.Containers) == 0 {
			return nil, "", fmt.Errorf("devbox pod has no container")
		}
		container = pod.Spec.Containers[0].Name
	}
	if !podContainsContainer(pod, container) {
		return nil, "", fmt.Errorf("container %q not found", container)
	}
	return pod, container, nil
}

func (s *apiServer) writePodResolveError(w http.ResponseWriter, err error) {
	httpStatus := podResolveHTTPStatus(err)
	if httpStatus == http.StatusNotFound {
		writeError(w, httpStatus, "devbox pod not found")
		return
	}

	errMsg := strings.TrimSpace(err.Error())
	switch httpStatus {
	case http.StatusConflict, http.StatusBadRequest, http.StatusInternalServerError:
		if errMsg == "" {
			errMsg = fmt.Sprintf("locate devbox pod failed: %v", err)
		}
		writeError(w, httpStatus, errMsg)
	default:
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("locate devbox pod failed: %v", err))
	}
}

func (s *apiServer) logPodResolveError(err error, kv ...interface{}) {
	if err == nil {
		return
	}
	s.logHTTPStatusError("resolve devbox pod failed", err, podResolveHTTPStatus(err), kv...)
}

func podResolveHTTPStatus(err error) int {
	if apierrors.IsNotFound(err) {
		return http.StatusNotFound
	}

	errMsg := strings.TrimSpace(err.Error())
	switch {
	case strings.Contains(errMsg, "not running"):
		return http.StatusConflict
	case strings.Contains(errMsg, "has no container"):
		return http.StatusInternalServerError
	case strings.Contains(errMsg, "container"):
		return http.StatusBadRequest
	case strings.Contains(errMsg, "podIP"):
		return http.StatusInternalServerError
	case strings.Contains(errMsg, "missing key"):
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

type countingReader struct {
	Reader io.Reader
	N      int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.N += int64(n)
	return n, err
}

type countingResponseWriter struct {
	http.ResponseWriter
	WrittenBytes int64
}

func (w *countingResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.WrittenBytes += int64(n)
	return n, err
}
