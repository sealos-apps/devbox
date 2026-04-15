package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIRoutesDoesNotServeGatewayProxy(t *testing.T) {
	srv := newTestAPIServer(t)
	srv.cfg.Gateway = GatewayConfig{
		PathPrefix: "/codex",
	}

	req := httptest.NewRequest(http.MethodGet, "/codex/demo-unique-id/sse", nil)
	resp := httptest.NewRecorder()

	srv.apiRoutes().ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusNotFound, resp.Code, resp.Body.String())
	}
}

func TestGatewayRoutesDoesNotServeAPI(t *testing.T) {
	srv := newTestAPIServer(t)
	srv.cfg.Gateway = GatewayConfig{
		PathPrefix: "/codex",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devbox/demo-devbox", nil)
	resp := httptest.NewRecorder()

	srv.gatewayRoutes().ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusNotFound, resp.Code, resp.Body.String())
	}
}
