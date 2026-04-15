package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleCreateDevboxAddsUpstreamLabel(t *testing.T) {
	srv := newTestAPIServer(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devbox",
		bytes.NewBufferString(`{"name":"demo-devbox","upstreamID":"session-1","labels":[{"key":"app.kubernetes.io/component","value":"runtime"}]}`),
	)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusCreated, resp.Code, resp.Body.String())
	}

	obj := &devboxv1alpha2.Devbox{}
	if err := srv.ctrlClient.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "ns-test", Name: "demo-devbox"}, obj); err != nil {
		t.Fatalf("get created devbox failed: %v", err)
	}
	if got := obj.Labels[devboxUpstreamIDLabelKey]; got != "session-1" {
		t.Fatalf("unexpected upstream label value: %q", got)
	}
	if got := obj.Labels["app.kubernetes.io/component"]; got != "runtime" {
		t.Fatalf("unexpected custom label value: %q", got)
	}
}

func TestHandleCreateDevboxRejectsInvalidUpstreamID(t *testing.T) {
	srv := newTestAPIServer(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devbox",
		bytes.NewBufferString(`{"name":"demo-devbox","upstreamID":"bad/value"}`),
	)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusBadRequest, resp.Code, resp.Body.String())
	}
}

func TestHandleCreateDevboxWithLifecycleConfig(t *testing.T) {
	srv := newTestAPIServer(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devbox",
		bytes.NewBufferString(`{"name":"demo-devbox","pauseAt":"2026-03-02T12:30:00Z","archiveAfterPauseTime":"2h"}`),
	)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusCreated, resp.Code, resp.Body.String())
	}

	obj := &devboxv1alpha2.Devbox{}
	if err := srv.ctrlClient.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "ns-test", Name: "demo-devbox"}, obj); err != nil {
		t.Fatalf("get created devbox failed: %v", err)
	}
	if got := obj.Labels[devboxLifecycleLabelKey]; got != "true" {
		t.Fatalf("unexpected lifecycle label value: %q", got)
	}
	if got := obj.Annotations[devboxAnnotationPauseAt]; got != "2026-03-02T12:30:00Z" {
		t.Fatalf("unexpected pauseAt annotation: %q", got)
	}
	if got := obj.Annotations[devboxAnnotationArchiveAfterPauseTime]; got != "2h0m0s" {
		t.Fatalf("unexpected archiveAfterPauseTime annotation: %q", got)
	}
}

func TestHandleCreateDevboxWithEnv(t *testing.T) {
	srv := newTestAPIServer(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devbox",
		bytes.NewBufferString(`{"name":"demo-devbox","env":{"FOO":"bar","DEVBOX_SDK_RUN_AS_ROOT":"false"}}`),
	)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusCreated, resp.Code, resp.Body.String())
	}

	obj := &devboxv1alpha2.Devbox{}
	if err := srv.ctrlClient.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "ns-test", Name: "demo-devbox"}, obj); err != nil {
		t.Fatalf("get created devbox failed: %v", err)
	}
	envByName := make(map[string]string, len(obj.Spec.Config.Env))
	for _, item := range obj.Spec.Config.Env {
		envByName[item.Name] = item.Value
	}
	if got := envByName["FOO"]; got != "bar" {
		t.Fatalf("unexpected FOO env value: %q", got)
	}
	if got := envByName["DEVBOX_SDK_RUN_AS_ROOT"]; got != "false" {
		t.Fatalf("unexpected DEVBOX_SDK_RUN_AS_ROOT env value: %q", got)
	}
}

func TestHandleCreateDevboxWithImage(t *testing.T) {
	srv := newTestAPIServer(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devbox",
		bytes.NewBufferString(`{"name":"demo-devbox","image":"registry.example.com/devbox/runtime:custom-v2"}`),
	)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusCreated, resp.Code, resp.Body.String())
	}

	obj := &devboxv1alpha2.Devbox{}
	if err := srv.ctrlClient.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "ns-test", Name: "demo-devbox"}, obj); err != nil {
		t.Fatalf("get created devbox failed: %v", err)
	}
	if got := obj.Spec.Image; got != "registry.example.com/devbox/runtime:custom-v2" {
		t.Fatalf("unexpected image value: %q", got)
	}
}

func TestHandleCreateDevboxRejectsBlankImage(t *testing.T) {
	srv := newTestAPIServer(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devbox",
		bytes.NewBufferString(`{"name":"demo-devbox","image":"   "}`),
	)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusBadRequest, resp.Code, resp.Body.String())
	}
}

func TestHandleCreateDevboxRejectsInvalidEnvName(t *testing.T) {
	srv := newTestAPIServer(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devbox",
		bytes.NewBufferString(`{"name":"demo-devbox","env":{"1BAD":"x"}}`),
	)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusBadRequest, resp.Code, resp.Body.String())
	}
}

func TestHandleCreateDevboxRejectsInvalidArchiveAfterPauseTime(t *testing.T) {
	srv := newTestAPIServer(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devbox",
		bytes.NewBufferString(`{"name":"demo-devbox","archiveAfterPauseTime":"bad"}`),
	)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusBadRequest, resp.Code, resp.Body.String())
	}
}

func TestHandleListDevboxesFilterByUpstreamID(t *testing.T) {
	t1 := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	t2 := metav1.NewTime(time.Unix(1_700_000_100, 0).UTC())

	srv := newTestAPIServer(
		t,
		&devboxv1alpha2.Devbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "db-a",
				Namespace:         "ns-test",
				CreationTimestamp: t1,
				Labels: map[string]string{
					devboxUpstreamIDLabelKey: "session-a",
				},
			},
			Spec: devboxv1alpha2.DevboxSpec{
				State: devboxv1alpha2.DevboxStateRunning,
			},
			Status: devboxv1alpha2.DevboxStatus{
				State: devboxv1alpha2.DevboxStateRunning,
				Phase: devboxv1alpha2.DevboxPhaseRunning,
			},
		},
		&devboxv1alpha2.Devbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "db-b",
				Namespace:         "ns-test",
				CreationTimestamp: t2,
				Labels: map[string]string{
					devboxUpstreamIDLabelKey: "session-b",
				},
			},
			Spec: devboxv1alpha2.DevboxSpec{
				State: devboxv1alpha2.DevboxStatePaused,
			},
			Status: devboxv1alpha2.DevboxStatus{
				State: devboxv1alpha2.DevboxStatePaused,
				Phase: devboxv1alpha2.DevboxPhasePaused,
			},
		},
		&devboxv1alpha2.Devbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "db-c",
				Namespace: "ns-other",
				Labels: map[string]string{
					devboxUpstreamIDLabelKey: "session-a",
				},
			},
			Spec: devboxv1alpha2.DevboxSpec{
				State: devboxv1alpha2.DevboxStateRunning,
			},
			Status: devboxv1alpha2.DevboxStatus{
				State: devboxv1alpha2.DevboxStateRunning,
				Phase: devboxv1alpha2.DevboxPhaseRunning,
			},
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devbox?upstreamID=session-a", nil)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusOK, resp.Code, resp.Body.String())
	}

	var payload struct {
		Code int `json:"code"`
		Data struct {
			Items []listDevboxItem `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if len(payload.Data.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(payload.Data.Items))
	}
	item := payload.Data.Items[0]
	if item.Name != "db-a" {
		t.Fatalf("unexpected item name: %s", item.Name)
	}
	if item.State.Spec != string(devboxv1alpha2.DevboxStateRunning) {
		t.Fatalf("unexpected item state.spec: %s", item.State.Spec)
	}
	if item.CreationTimestamp != t1.UTC().Format(time.RFC3339) {
		t.Fatalf("unexpected creationTimestamp: %s", item.CreationTimestamp)
	}
}

func TestHandleListDevboxesRejectsInvalidUpstreamID(t *testing.T) {
	srv := newTestAPIServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devbox?upstreamID=bad/value", nil)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusBadRequest, resp.Code, resp.Body.String())
	}
}

func TestHandleRefreshDevboxPauseAt(t *testing.T) {
	srv := newTestAPIServer(
		t,
		&devboxv1alpha2.Devbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "db-a",
				Namespace: "ns-test",
				Labels: map[string]string{
					devboxLifecycleLabelKey: "true",
				},
				Annotations: map[string]string{
					devboxAnnotationPauseAt:               "2026-03-02T08:00:00Z",
					devboxAnnotationArchiveAfterPauseTime: "1h0m0s",
					devboxAnnotationPausedAt:              "2026-03-02T08:01:00Z",
				},
			},
		},
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devbox/db-a/pause/refresh",
		bytes.NewBufferString(`{"pauseAt":"2026-03-03T09:00:00Z"}`),
	)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusOK, resp.Code, resp.Body.String())
	}

	latest := &devboxv1alpha2.Devbox{}
	if err := srv.ctrlClient.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "ns-test", Name: "db-a"}, latest); err != nil {
		t.Fatalf("get refreshed devbox failed: %v", err)
	}
	if got := latest.Annotations[devboxAnnotationPauseAt]; got != "2026-03-03T09:00:00Z" {
		t.Fatalf("unexpected pauseAt annotation: %q", got)
	}
	if _, exists := latest.Annotations[devboxAnnotationPausedAt]; exists {
		t.Fatalf("pausedAt annotation should be cleared on refresh")
	}
	if got := latest.Annotations[devboxAnnotationPauseRefreshAt]; got == "" {
		t.Fatalf("pauseRefreshAt annotation should be set")
	}
}

func TestHandleGetDevboxInfoIncludesGateway(t *testing.T) {
	srv := newTestAPIServer(
		t,
		&devboxv1alpha2.Devbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "demo-devbox",
				Namespace: "ns-test",
			},
			Spec: devboxv1alpha2.DevboxSpec{
				State: devboxv1alpha2.DevboxStateRunning,
			},
			Status: devboxv1alpha2.DevboxStatus{
				State: devboxv1alpha2.DevboxStateRunning,
				Phase: devboxv1alpha2.DevboxPhaseRunning,
				Network: devboxv1alpha2.NetworkStatus{
					UniqueID: "demo-unique-id",
				},
			},
		},
	)
	srv.cfg.Gateway = GatewayConfig{
		Domain:     "devbox-gateway.staging-usw-1.sealos.io",
		PathPrefix: "/codex",
		Port:       1317,
		SSEPath:    "/sse",
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-devbox",
			Namespace: "ns-test",
		},
		Data: map[string][]byte{
			"SEALOS_DEVBOX_PRIVATE_KEY": []byte("fake-private-key"),
			devboxJWTSecretKey:          []byte("devbox-jwt-secret"),
		},
	}
	if err := srv.ctrlClient.Create(context.Background(), secret); err != nil {
		t.Fatalf("create secret failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devbox/demo-devbox", nil)
	req.Header.Set("Authorization", issueBearerTokenForNamespace(t, "ns-test"))
	resp := httptest.NewRecorder()
	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusOK, resp.Code, resp.Body.String())
	}

	var payload struct {
		Code int `json:"code"`
		Data struct {
			Name    string `json:"name"`
			Gateway struct {
				URL      string `json:"url"`
				SSEURL   string `json:"sseURL"`
				Token    string `json:"token"`
				Port     int    `json:"port"`
				SSEPath  string `json:"ssePath"`
				UniqueID string `json:"uniqueID"`
			} `json:"gateway"`
			SSH struct {
				PrivateKeyBase64 string `json:"privateKeyBase64"`
			} `json:"ssh"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.Data.Name != "demo-devbox" {
		t.Fatalf("unexpected name: %s", payload.Data.Name)
	}
	if payload.Data.Gateway.Port != 1317 {
		t.Fatalf("unexpected gateway port: %d", payload.Data.Gateway.Port)
	}
	if payload.Data.Gateway.URL != "https://devbox-gateway.staging-usw-1.sealos.io/codex/demo-unique-id" {
		t.Fatalf("unexpected gateway url: %s", payload.Data.Gateway.URL)
	}
	if payload.Data.Gateway.SSEURL != "https://devbox-gateway.staging-usw-1.sealos.io/codex/demo-unique-id/sse" {
		t.Fatalf("unexpected gateway sseURL: %s", payload.Data.Gateway.SSEURL)
	}
	if payload.Data.Gateway.Token != "devbox-jwt-secret" {
		t.Fatalf("unexpected gateway token: %s", payload.Data.Gateway.Token)
	}
	if payload.Data.Gateway.UniqueID != "demo-unique-id" {
		t.Fatalf("unexpected uniqueID: %s", payload.Data.Gateway.UniqueID)
	}
	entry, ok := srv.getGatewayIndex("demo-unique-id")
	if !ok {
		t.Fatalf("expected gateway index entry for uniqueID")
	}
	if entry.Name != "demo-devbox" || entry.Namespace != "ns-test" {
		t.Fatalf("unexpected gateway index entry: %+v", entry)
	}
	if payload.Data.SSH.PrivateKeyBase64 != base64.StdEncoding.EncodeToString([]byte("fake-private-key")) {
		t.Fatalf("unexpected private key base64: %s", payload.Data.SSH.PrivateKeyBase64)
	}
}

func newTestAPIServer(t *testing.T, objs ...ctrlclient.Object) *apiServer {
	t.Helper()

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme failed: %v", err)
	}
	if err := devboxv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add devbox scheme failed: %v", err)
	}

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		Build()

	return &apiServer{
		cfg: ServerConfig{
			JWTSigningKey: "test-secret",
			SSH: SSHConnectionConfig{
				User:                "devbox",
				Host:                "staging-usw-1.sealos.io",
				Port:                2233,
				PrivateKeySecretKey: "SEALOS_DEVBOX_PRIVATE_KEY",
			},
			CreateResource: CreateDevboxResourceConfig{
				CPU:          "1000m",
				Memory:       "1Gi",
				StorageLimit: "10Gi",
				Image:        "registry.example.com/devbox/runtime:latest",
			},
		},
		ctrlClient: c,
		logger:     newLogger(io.Discard, slog.LevelDebug),
	}
}

func issueBearerTokenForNamespace(t *testing.T, namespace string) string {
	t.Helper()
	now := time.Now().UTC()
	token := issueTestJWT(t, "test-secret", jwtClaims{
		Namespace: namespace,
		Iat:       now.Unix() - 10,
		Nbf:       now.Unix() - 5,
		Exp:       now.Unix() + 3600,
	})
	return "Bearer " + token
}
