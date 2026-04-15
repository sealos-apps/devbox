package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHandleGatewayProxyForwardsToHeadlessService(t *testing.T) {
	type upstreamRequest struct {
		Path            string
		Query           string
		Authorization   string
		ForwardedPrefix string
		ForwardedHost   string
		ForwardedProto  string
		UniqueID        string
		Namespace       string
		Name            string
		Host            string
	}

	requestCh := make(chan upstreamRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCh <- upstreamRequest{
			Path:            r.URL.Path,
			Query:           r.URL.RawQuery,
			Authorization:   r.Header.Get("Authorization"),
			ForwardedPrefix: r.Header.Get("X-Forwarded-Prefix"),
			ForwardedHost:   r.Header.Get("X-Forwarded-Host"),
			ForwardedProto:  r.Header.Get("X-Forwarded-Proto"),
			UniqueID:        r.Header.Get("X-Devbox-UniqueID"),
			Namespace:       r.Header.Get("X-Devbox-Namespace"),
			Name:            r.Header.Get("X-Devbox-Name"),
			Host:            r.Host,
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ok\n\n")
	}))
	defer upstream.Close()

	devbox := &devboxv1alpha2.Devbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-devbox",
			Namespace: "ns-test",
		},
		Status: devboxv1alpha2.DevboxStatus{
			Network: devboxv1alpha2.NetworkStatus{
				UniqueID: "demo-unique-id",
			},
		},
	}
	srv := newTestAPIServer(t, devbox)
	srv.cfg.Gateway = GatewayConfig{
		Domain:     "devbox-gateway.staging-usw-1.sealos.io",
		PathPrefix: "/codex",
		Port:       1317,
		SSEPath:    "/sse",
	}
	srv.syncGatewayIndex(devbox)
	srv.gatewayProxyTransport = newTestGatewayProxyTransport(t, upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/codex/demo-unique-id/sse?watch=1", nil)
	req.Host = "devbox-gateway.staging-usw-1.sealos.io"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Authorization", "Bearer devbox-jwt-secret")
	resp := httptest.NewRecorder()

	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusOK, resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); body != "data: ok\n\n" {
		t.Fatalf("unexpected proxy body: %q", body)
	}

	upstreamReq := <-requestCh
	if upstreamReq.Path != "/sse" {
		t.Fatalf("unexpected upstream path: %s", upstreamReq.Path)
	}
	if upstreamReq.Query != "watch=1" {
		t.Fatalf("unexpected upstream query: %s", upstreamReq.Query)
	}
	if upstreamReq.Authorization != "Bearer devbox-jwt-secret" {
		t.Fatalf("unexpected authorization header: %s", upstreamReq.Authorization)
	}
	if upstreamReq.ForwardedPrefix != "/codex/demo-unique-id" {
		t.Fatalf("unexpected forwarded prefix: %s", upstreamReq.ForwardedPrefix)
	}
	if upstreamReq.ForwardedHost != "devbox-gateway.staging-usw-1.sealos.io" {
		t.Fatalf("unexpected forwarded host: %s", upstreamReq.ForwardedHost)
	}
	if upstreamReq.ForwardedProto != "https" {
		t.Fatalf("unexpected forwarded proto: %s", upstreamReq.ForwardedProto)
	}
	if upstreamReq.UniqueID != "demo-unique-id" || upstreamReq.Namespace != "ns-test" || upstreamReq.Name != "demo-devbox" {
		t.Fatalf("unexpected devbox proxy headers: %+v", upstreamReq)
	}
	if upstreamReq.Host != "demo-unique-id.ns-test.svc.cluster.local:1317" {
		t.Fatalf("unexpected upstream host: %s", upstreamReq.Host)
	}
}

func TestHandleGatewayProxyRewritesLocationAndCookiePath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "session=abc; Path=/; HttpOnly")
		w.Header().Set("Location", "http://demo-unique-id.ns-test.svc.cluster.local:1317/login/callback?code=1")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()

	devbox := &devboxv1alpha2.Devbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-devbox",
			Namespace: "ns-test",
		},
		Status: devboxv1alpha2.DevboxStatus{
			Network: devboxv1alpha2.NetworkStatus{
				UniqueID: "demo-unique-id",
			},
		},
	}
	srv := newTestAPIServer(t, devbox)
	srv.cfg.Gateway = GatewayConfig{
		Domain:     "devbox-gateway.staging-usw-1.sealos.io",
		PathPrefix: "/codex",
		Port:       1317,
		SSEPath:    "/sse",
	}
	srv.syncGatewayIndex(devbox)
	srv.gatewayProxyTransport = newTestGatewayProxyTransport(t, upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/codex/demo-unique-id/login", nil)
	req.Host = "devbox-gateway.staging-usw-1.sealos.io"
	req.Header.Set("X-Forwarded-Proto", "https")
	resp := httptest.NewRecorder()

	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusFound {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusFound, resp.Code, resp.Body.String())
	}
	if location := resp.Header().Get("Location"); location != "https://devbox-gateway.staging-usw-1.sealos.io/codex/demo-unique-id/login/callback?code=1" {
		t.Fatalf("unexpected rewritten location: %s", location)
	}
	cookie := resp.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "Path=/codex/demo-unique-id/") {
		t.Fatalf("unexpected rewritten cookie path: %s", cookie)
	}
}

func TestHandleGatewayProxyReturnsNotFoundForUnknownUniqueID(t *testing.T) {
	srv := newTestAPIServer(t)
	srv.cfg.Gateway = GatewayConfig{
		PathPrefix: "/codex",
		Port:       1317,
	}

	req := httptest.NewRequest(http.MethodGet, "/codex/missing-id/sse", nil)
	resp := httptest.NewRecorder()

	srv.routes().ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusNotFound, resp.Code, resp.Body.String())
	}
}

func newTestGatewayProxyTransport(t *testing.T, upstreamURL string) http.RoundTripper {
	t.Helper()

	parsed, err := neturl.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("parse upstream url failed: %v", err)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, parsed.Host)
	}
	return transport
}
