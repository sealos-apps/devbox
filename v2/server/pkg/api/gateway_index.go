package api

import (
	"context"
	"path"
	"strings"
	"time"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type gatewayIndexEntry struct {
	Namespace string
	Name      string
	UniqueID  string
	URL       string
	SSEURL    string
	Port      int
	UpdatedAt time.Time
}

func gatewayPathPrefix(cfg GatewayConfig) string {
	pathPrefix := strings.TrimSpace(cfg.PathPrefix)
	if pathPrefix == "" {
		pathPrefix = defaultGatewayPathPrefix
	}
	if !strings.HasPrefix(pathPrefix, "/") {
		pathPrefix = "/" + pathPrefix
	}
	pathPrefix = path.Clean(pathPrefix)
	if pathPrefix != "/" {
		pathPrefix = strings.TrimRight(pathPrefix, "/")
	}
	return pathPrefix
}

func gatewaySSEPath(cfg GatewayConfig) string {
	ssePath := strings.TrimSpace(cfg.SSEPath)
	if ssePath == "" {
		ssePath = defaultGatewaySSEPath
	}
	if !strings.HasPrefix(ssePath, "/") {
		ssePath = "/" + ssePath
	}
	ssePath = path.Clean(ssePath)
	if ssePath != "/" {
		ssePath = strings.TrimRight(ssePath, "/")
	}
	return ssePath
}

func gatewayPort(cfg GatewayConfig) int {
	if cfg.Port <= 0 {
		return defaultGatewayPort
	}
	return cfg.Port
}

func buildGatewayURLs(cfg GatewayConfig, uniqueID string) (string, string, bool) {
	domain := strings.TrimRight(strings.TrimSpace(cfg.Domain), "/")
	uniqueID = strings.TrimSpace(uniqueID)
	if domain == "" || uniqueID == "" {
		return "", "", false
	}

	pathPrefix := gatewayPathPrefix(cfg)
	ssePath := gatewaySSEPath(cfg)

	basePath := path.Join("/", pathPrefix, uniqueID)
	return "https://" + domain + basePath, "https://" + domain + path.Join(basePath, ssePath), true
}

func buildGatewayIndexEntry(cfg GatewayConfig, devbox *devboxv1alpha2.Devbox) (gatewayIndexEntry, bool) {
	if devbox == nil {
		return gatewayIndexEntry{}, false
	}

	uniqueID := strings.TrimSpace(devbox.Status.Network.UniqueID)
	if uniqueID == "" {
		return gatewayIndexEntry{}, false
	}
	url, sseURL, _ := buildGatewayURLs(cfg, uniqueID)
	return gatewayIndexEntry{
		Namespace: devbox.Namespace,
		Name:      devbox.Name,
		UniqueID:  uniqueID,
		URL:       url,
		SSEURL:    sseURL,
		Port:      gatewayPort(cfg),
		UpdatedAt: time.Now().UTC(),
	}, true
}

func (s *apiServer) rebuildGatewayIndex(ctx context.Context, reader ctrlclient.Reader) error {
	if s == nil || reader == nil {
		return nil
	}

	devboxList := &devboxv1alpha2.DevboxList{}
	if err := reader.List(ctx, devboxList); err != nil {
		return err
	}

	nextByUniqueID := make(map[string]gatewayIndexEntry, len(devboxList.Items))
	nextByDevbox := make(map[string]string, len(devboxList.Items))
	for i := range devboxList.Items {
		entry, ok := buildGatewayIndexEntry(s.cfg.Gateway, &devboxList.Items[i])
		if !ok {
			continue
		}
		nextByUniqueID[entry.UniqueID] = entry
		nextByDevbox[gatewayIndexKey(entry.Namespace, entry.Name)] = entry.UniqueID
	}

	s.gatewayIndexMu.Lock()
	defer s.gatewayIndexMu.Unlock()
	s.gatewayIndexByUniqueID = nextByUniqueID
	s.gatewayIndexByDevbox = nextByDevbox
	return nil
}

func (s *apiServer) syncGatewayIndex(devbox *devboxv1alpha2.Devbox) {
	if s == nil || devbox == nil {
		return
	}

	key := gatewayIndexKey(devbox.Namespace, devbox.Name)
	entry, ok := buildGatewayIndexEntry(s.cfg.Gateway, devbox)

	s.gatewayIndexMu.Lock()
	defer s.gatewayIndexMu.Unlock()

	if s.gatewayIndexByUniqueID == nil {
		s.gatewayIndexByUniqueID = make(map[string]gatewayIndexEntry, 128)
	}
	if s.gatewayIndexByDevbox == nil {
		s.gatewayIndexByDevbox = make(map[string]string, 128)
	}

	if prevUniqueID, exists := s.gatewayIndexByDevbox[key]; exists && (!ok || prevUniqueID != entry.UniqueID) {
		delete(s.gatewayIndexByUniqueID, prevUniqueID)
	}

	if !ok {
		delete(s.gatewayIndexByDevbox, key)
		return
	}

	s.gatewayIndexByUniqueID[entry.UniqueID] = entry
	s.gatewayIndexByDevbox[key] = entry.UniqueID
}

func (s *apiServer) deleteGatewayIndex(namespace string, name string, uniqueID string) {
	if s == nil {
		return
	}

	key := gatewayIndexKey(namespace, name)
	s.gatewayIndexMu.Lock()
	defer s.gatewayIndexMu.Unlock()

	if prevUniqueID, exists := s.gatewayIndexByDevbox[key]; exists {
		delete(s.gatewayIndexByUniqueID, prevUniqueID)
		delete(s.gatewayIndexByDevbox, key)
		return
	}

	uniqueID = strings.TrimSpace(uniqueID)
	if uniqueID != "" {
		delete(s.gatewayIndexByUniqueID, uniqueID)
	}
}

func (s *apiServer) getGatewayIndex(uniqueID string) (gatewayIndexEntry, bool) {
	if s == nil {
		return gatewayIndexEntry{}, false
	}

	uniqueID = strings.TrimSpace(uniqueID)
	if uniqueID == "" {
		return gatewayIndexEntry{}, false
	}

	s.gatewayIndexMu.RLock()
	defer s.gatewayIndexMu.RUnlock()
	entry, ok := s.gatewayIndexByUniqueID[uniqueID]
	return entry, ok
}

func gatewayIndexKey(namespace string, name string) string {
	return namespace + "/" + name
}
