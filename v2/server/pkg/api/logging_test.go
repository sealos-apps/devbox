package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLoggingMiddlewareSkipsHealthz(t *testing.T) {
	var logBuf bytes.Buffer
	srv := &apiServer{
		logger: newLogger(&logBuf, defaultLogLevel),
	}

	handler := srv.requestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}
	if got := strings.TrimSpace(logBuf.String()); got != "" {
		t.Fatalf("expected no log output for /healthz, got %q", got)
	}
}

func TestRequestLoggingMiddlewareLogsNormalRequests(t *testing.T) {
	var logBuf bytes.Buffer
	srv := &apiServer{
		logger: newLogger(&logBuf, defaultLogLevel),
	}

	handler := srv.requestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devbox", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, resp.Code)
	}
	output := logBuf.String()
	if !strings.Contains(output, `msg="http request"`) {
		t.Fatalf("expected http request log message, got %q", output)
	}
	if !strings.Contains(output, `path=/api/v1/devbox`) {
		t.Fatalf("expected request path in log output, got %q", output)
	}
	if !strings.Contains(output, `level=INFO`) {
		t.Fatalf("expected info level in log output, got %q", output)
	}
}

func TestRequestLoggingMiddlewareWarnsOnClientError(t *testing.T) {
	var logBuf bytes.Buffer
	srv := &apiServer{
		logger: newLogger(&logBuf, defaultLogLevel),
	}

	handler := srv.requestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devbox", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, resp.Code)
	}
	output := logBuf.String()
	if !strings.Contains(output, `level=WARN`) {
		t.Fatalf("expected warn level in log output, got %q", output)
	}
}

func TestRequestLoggingMiddlewareErrorsOnServerError(t *testing.T) {
	var logBuf bytes.Buffer
	srv := &apiServer{
		logger: newLogger(&logBuf, defaultLogLevel),
	}

	handler := srv.requestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devbox", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, resp.Code)
	}
	output := logBuf.String()
	if !strings.Contains(output, `level=ERROR`) {
		t.Fatalf("expected error level in log output, got %q", output)
	}
}

func TestRequestLoggingMiddlewareDebugsExpectedExecConflict(t *testing.T) {
	var logBuf bytes.Buffer
	srv := &apiServer{
		logger: newLogger(&logBuf, slog.LevelDebug),
	}

	handler := srv.requestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devbox/demo/exec", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d", http.StatusConflict, resp.Code)
	}
	output := logBuf.String()
	if !strings.Contains(output, `level=DEBUG`) {
		t.Fatalf("expected debug level in log output, got %q", output)
	}
}
