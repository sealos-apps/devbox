package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	neturl "net/url"
	"strconv"
	"strings"
	"time"
)

const clusterLocalDomainSuffix = "svc.cluster.local"

type gatewayProxyRoute struct {
	UniqueID     string
	UpstreamPath string
}

func newGatewayProxyTransport() http.RoundTripper {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 32
	transport.MaxConnsPerHost = 0
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	return transport
}

func (s *apiServer) handleGatewayProxy(w http.ResponseWriter, r *http.Request) {
	proxyPathPrefix := gatewayPathPrefix(s.cfg.Gateway)
	route, ok := parseGatewayProxyRoute(proxyPathPrefix, r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	entry, ok := s.getGatewayIndex(route.UniqueID)
	if !ok {
		if err := s.refreshGatewayIndex(r.Context()); err != nil {
			s.logError("refresh gateway index failed", err, "unique_id", route.UniqueID, "path", r.URL.Path)
		}
		entry, ok = s.getGatewayIndex(route.UniqueID)
	}
	if !ok {
		s.logWarn("gateway proxy route not found", "unique_id", route.UniqueID, "path", r.URL.Path, "http_status", http.StatusNotFound)
		http.Error(w, "gateway route not found", http.StatusNotFound)
		return
	}

	upstreamURL := buildGatewayUpstreamURL(entry, gatewayPort(s.cfg.Gateway), route.UpstreamPath)
	proxyPrefix := buildGatewayProxyPrefix(proxyPathPrefix, entry.UniqueID)
	r.Header.Set("X-Namespace", entry.Namespace)

	s.newGatewayReverseProxy(r, upstreamURL, proxyPrefix, entry).ServeHTTP(w, r)
}

func (s *apiServer) refreshGatewayIndex(ctx context.Context) error {
	if s == nil {
		return nil
	}

	reader := s.lifecycleReader
	if reader == nil {
		reader = s.ctrlClient
	}
	if reader == nil {
		return nil
	}
	return s.rebuildGatewayIndex(ctx, reader)
}

func parseGatewayProxyRoute(pathPrefix string, requestPath string) (gatewayProxyRoute, bool) {
	pathPrefix = gatewayPathPrefix(GatewayConfig{PathPrefix: pathPrefix})
	if requestPath == pathPrefix {
		return gatewayProxyRoute{}, false
	}
	if pathPrefix != "/" && !strings.HasPrefix(requestPath, pathPrefix+"/") {
		return gatewayProxyRoute{}, false
	}

	remainder := strings.TrimPrefix(requestPath, pathPrefix)
	remainder = strings.TrimPrefix(remainder, "/")
	if remainder == "" {
		return gatewayProxyRoute{}, false
	}

	parts := strings.SplitN(remainder, "/", 2)
	uniqueID := strings.TrimSpace(parts[0])
	if uniqueID == "" {
		return gatewayProxyRoute{}, false
	}

	upstreamPath := "/"
	if len(parts) == 2 {
		upstreamPath = "/" + parts[1]
	}

	return gatewayProxyRoute{
		UniqueID:     uniqueID,
		UpstreamPath: upstreamPath,
	}, true
}

func buildGatewayUpstreamURL(entry gatewayIndexEntry, port int, upstreamPath string) *neturl.URL {
	upstreamHost := fmt.Sprintf("%s.%s.%s", entry.UniqueID, entry.Namespace, clusterLocalDomainSuffix)
	return &neturl.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(upstreamHost, strconv.Itoa(port)),
		Path:   upstreamPath,
	}
}

func buildGatewayProxyPrefix(pathPrefix string, uniqueID string) string {
	pathPrefix = gatewayPathPrefix(GatewayConfig{PathPrefix: pathPrefix})
	pathPrefix = strings.TrimRight(pathPrefix, "/")
	if pathPrefix == "" {
		return "/" + strings.TrimSpace(uniqueID)
	}
	return pathPrefix + "/" + strings.TrimSpace(uniqueID)
}

func (s *apiServer) newGatewayReverseProxy(
	in *http.Request,
	upstreamURL *neturl.URL,
	proxyPrefix string,
	entry gatewayIndexEntry,
) *httputil.ReverseProxy {
	originalHost := in.Host
	originalProto := forwardedProtoFromRequest(in)
	transport := s.gatewayProxyTransport
	if transport == nil {
		transport = newGatewayProxyTransport()
	}

	return &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = upstreamURL.Scheme
			pr.Out.URL.Host = upstreamURL.Host
			pr.Out.URL.Path = upstreamURL.Path
			pr.Out.URL.RawPath = upstreamURL.RawPath
			pr.Out.Host = upstreamURL.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Host", originalHost)
			pr.Out.Header.Set("X-Forwarded-Proto", originalProto)
			pr.Out.Header.Set("X-Forwarded-Prefix", proxyPrefix)
			pr.Out.Header.Set("X-Devbox-UniqueID", entry.UniqueID)
			pr.Out.Header.Set("X-Devbox-Namespace", entry.Namespace)
			pr.Out.Header.Set("X-Devbox-Name", entry.Name)
		},
		ModifyResponse: func(resp *http.Response) error {
			rewriteGatewayProxyResponse(resp, proxyPrefix, originalHost, originalProto, upstreamURL.Host)
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			statusCode := http.StatusBadGateway
			message := "gateway upstream request failed"
			if ctxErr := req.Context().Err(); ctxErr != nil {
				statusCode = http.StatusGatewayTimeout
				message = "gateway upstream request timeout"
				err = ctxErr
			}
			s.logError(
				"gateway proxy request failed",
				err,
				"http_status", statusCode,
				"unique_id", entry.UniqueID,
				"namespace", entry.Namespace,
				"name", entry.Name,
				"upstream", upstreamURL.Host,
				"path", req.URL.Path,
			)
			http.Error(rw, message, statusCode)
		},
	}
}

func rewriteGatewayProxyResponse(resp *http.Response, proxyPrefix string, originalHost string, originalProto string, upstreamHost string) {
	if resp == nil {
		return
	}

	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location != "" {
		resp.Header.Set("Location", rewriteGatewayLocationHeader(location, proxyPrefix, originalHost, originalProto, upstreamHost))
	}

	setCookies := resp.Header.Values("Set-Cookie")
	if len(setCookies) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, value := range setCookies {
		resp.Header.Add("Set-Cookie", rewriteGatewaySetCookieHeader(value, proxyPrefix))
	}
}

func rewriteGatewayLocationHeader(location string, proxyPrefix string, originalHost string, originalProto string, upstreamHost string) string {
	if strings.HasPrefix(location, "/") {
		return joinGatewayPublicPath(proxyPrefix, location)
	}

	parsed, err := neturl.Parse(location)
	if err != nil {
		return location
	}
	if !parsed.IsAbs() || !strings.EqualFold(parsed.Host, upstreamHost) {
		return location
	}

	parsed.Scheme = originalProto
	parsed.Host = originalHost
	parsed.Path = joinGatewayPublicPath(proxyPrefix, parsed.Path)
	parsed.RawPath = ""
	return parsed.String()
}

func rewriteGatewaySetCookieHeader(value string, proxyPrefix string) string {
	parts := strings.Split(value, ";")
	for i := 1; i < len(parts); i++ {
		part := strings.TrimSpace(parts[i])
		if len(part) < 5 || !strings.EqualFold(part[:5], "Path=") {
			continue
		}

		pathValue := strings.TrimSpace(part[5:])
		parts[i] = " Path=" + joinGatewayPublicPath(proxyPrefix, pathValue)
		return strings.Join(parts, ";")
	}
	return value
}

func joinGatewayPublicPath(proxyPrefix string, upstreamPath string) string {
	proxyPrefix = strings.TrimRight(strings.TrimSpace(proxyPrefix), "/")
	if upstreamPath == "" || upstreamPath == "/" {
		if proxyPrefix == "" {
			return "/"
		}
		return proxyPrefix + "/"
	}
	if !strings.HasPrefix(upstreamPath, "/") {
		upstreamPath = "/" + upstreamPath
	}
	if proxyPrefix == "" {
		return upstreamPath
	}
	return proxyPrefix + upstreamPath
}

func forwardedProtoFromRequest(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); value != "" {
		parts := strings.Split(value, ",")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return strings.TrimSpace(parts[0])
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
