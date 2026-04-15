package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"
)

var requestSeq uint64

func (s *apiServer) requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = nextRequestID()
		}

		rw := &statusResponseWriter{ResponseWriter: w}
		rw.Header().Set("X-Request-ID", requestID)

		defer func() {
			if recovered := recover(); recovered != nil {
				s.logError(
					"panic recovered",
					fmt.Errorf("%v", recovered),
					"request_id", requestID,
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				if !rw.wroteHeader {
					rw.Header().Set("Content-Type", "application/json")
					rw.WriteHeader(http.StatusInternalServerError)
					_, _ = rw.Write([]byte(`{"code":500,"message":"internal server error"}`))
				}
			}

			statusCode := rw.statusCodeOrOK()
			if shouldSkipRequestLog(r) {
				return
			}

			level := requestLogLevel(r, statusCode)
			kv := []interface{}{
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"namespace", resolveLogNamespace(r, rw),
				"status", statusCode,
				"bytes", rw.writtenBytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			}

			s.log(context.Background(), level, "http request", kv...)
		}()

		next.ServeHTTP(rw, r)
	})
}

func shouldSkipRequestLog(r *http.Request) bool {
	path := strings.TrimSpace(r.URL.Path)
	return path == "/healthz"
}

func resolveLogNamespace(r *http.Request, rw *statusResponseWriter) string {
	namespace := strings.TrimSpace(rw.Header().Get("X-Namespace"))
	if namespace != "" {
		return namespace
	}

	namespace = strings.TrimSpace(r.Header.Get("X-Namespace"))
	if namespace != "" {
		return namespace
	}
	return strings.TrimSpace(r.URL.Query().Get("namespace"))
}

func requestLogLevel(r *http.Request, statusCode int) slog.Level {
	path := strings.TrimSpace(r.URL.Path)
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/exec") && statusCode == http.StatusConflict {
		return slog.LevelDebug
	}
	switch {
	case statusCode >= http.StatusInternalServerError:
		return slog.LevelError
	case statusCode >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(handler)
}

func (s *apiServer) logInfo(message string, kv ...interface{}) {
	s.log(context.Background(), slog.LevelInfo, message, kv...)
}

func (s *apiServer) logDebug(message string, kv ...interface{}) {
	s.log(context.Background(), slog.LevelDebug, message, kv...)
}

func (s *apiServer) logWarn(message string, kv ...interface{}) {
	s.log(context.Background(), slog.LevelWarn, message, kv...)
}

func (s *apiServer) logWarnError(message string, err error, kv ...interface{}) {
	if s == nil || s.logger == nil {
		return
	}
	fields := make([]interface{}, 0, len(kv)+2)
	fields = append(fields, "error", err)
	fields = append(fields, kv...)
	s.log(context.Background(), slog.LevelWarn, message, fields...)
}

func (s *apiServer) logError(message string, err error, kv ...interface{}) {
	if s == nil || s.logger == nil {
		return
	}
	fields := make([]interface{}, 0, len(kv)+2)
	fields = append(fields, "error", err)
	fields = append(fields, kv...)
	s.log(context.Background(), slog.LevelError, message, fields...)
}

func (s *apiServer) log(ctx context.Context, level slog.Level, message string, kv ...interface{}) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Log(ctx, level, message, normalizeLogArgs(kv...)...)
}

func nextRequestID() string {
	seq := atomic.AddUint64(&requestSeq, 1)
	return fmt.Sprintf("req-%d-%d", time.Now().UnixNano(), seq)
}

func normalizeLogArgs(kv ...interface{}) []any {
	if len(kv) == 0 {
		return nil
	}

	args := make([]any, 0, len(kv)+1)
	for i := 0; i < len(kv); i += 2 {
		key := fmt.Sprint(kv[i])
		var value any = "<missing>"
		if i+1 < len(kv) {
			value = kv[i+1]
		}
		args = append(args, key, value)
	}
	return args
}

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	wroteHeader  bool
	writtenBytes int64
}

func (w *statusResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *statusResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.writtenBytes += int64(n)
	return n, err
}

func (w *statusResponseWriter) statusCodeOrOK() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

func (w *statusResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *statusResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(r)
		w.writtenBytes += n
		return n, err
	}
	n, err := io.Copy(w.ResponseWriter, r)
	w.writtenBytes += n
	return n, err
}

func (w *statusResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
