package api

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadServerConfigWithInlineJWTKey(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
server:
  listenAddress: ":18090"
  gatewayListenAddress: ":18091"
  logLevel: "debug"
auth:
  jwtSigningKey: "jwt-secret"
ssh:
  user: "devbox"
  host: "staging-usw-1.sealos.io"
  port: 2233
gateway:
  domain: "devbox-gateway.staging-usw-1.sealos.io"
  pathPrefix: "/codex"
  port: 1317
  ssePath: "/events"
devbox:
  createDefaults:
    image: "registry.example.com/devbox/runtime:latest"
    storageLimit: "20Gi"
    resource:
      cpu: "2500m"
      memory: "6144Mi"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	cfg, err := loadServerConfig(configPath)
	if err != nil {
		t.Fatalf("loadServerConfig failed: %v", err)
	}

	if cfg.Addr != ":18090" {
		t.Fatalf("unexpected addr: %s", cfg.Addr)
	}
	if cfg.GatewayAddr != ":18091" {
		t.Fatalf("unexpected gateway addr: %s", cfg.GatewayAddr)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected log level: %v", cfg.LogLevel)
	}
	if cfg.JWTSigningKey != "jwt-secret" {
		t.Fatalf("unexpected jwt key")
	}
	if cfg.SSH.User != "devbox" {
		t.Fatalf("unexpected ssh user: %s", cfg.SSH.User)
	}
	if cfg.SSH.Host != "staging-usw-1.sealos.io" {
		t.Fatalf("unexpected ssh host: %s", cfg.SSH.Host)
	}
	if cfg.SSH.Port != 2233 {
		t.Fatalf("unexpected ssh port: %d", cfg.SSH.Port)
	}
	if cfg.Gateway.Domain != "devbox-gateway.staging-usw-1.sealos.io" {
		t.Fatalf("unexpected gateway domain: %s", cfg.Gateway.Domain)
	}
	if cfg.Gateway.PathPrefix != "/codex" {
		t.Fatalf("unexpected gateway pathPrefix: %s", cfg.Gateway.PathPrefix)
	}
	if cfg.Gateway.Port != 1317 {
		t.Fatalf("unexpected gateway port: %d", cfg.Gateway.Port)
	}
	if cfg.Gateway.SSEPath != "/events" {
		t.Fatalf("unexpected gateway ssePath: %s", cfg.Gateway.SSEPath)
	}
	if cfg.CreateResource.CPU != "2500m" {
		t.Fatalf("unexpected cpu: %s", cfg.CreateResource.CPU)
	}
	if cfg.CreateResource.Memory != "6144Mi" {
		t.Fatalf("unexpected memory: %s", cfg.CreateResource.Memory)
	}
	if cfg.CreateResource.StorageLimit != "20Gi" {
		t.Fatalf("unexpected storageLimit: %s", cfg.CreateResource.StorageLimit)
	}
	if cfg.CreateResource.Image != "registry.example.com/devbox/runtime:latest" {
		t.Fatalf("unexpected image: %s", cfg.CreateResource.Image)
	}
	if cfg.LifecycleResyncInterval != defaultLifecycleResync {
		t.Fatalf("unexpected default lifecycleResyncInterval: %s", cfg.LifecycleResyncInterval)
	}
}

func TestLoadServerConfigWithJWTKeyFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	keyFilePath := filepath.Join(tmpDir, "jwt.key")

	if err := os.WriteFile(keyFilePath, []byte("jwt-secret-from-file\n"), 0o600); err != nil {
		t.Fatalf("write key file failed: %v", err)
	}

	configContent := `
server:
  listenAddress: ":18091"
  lifecycleResyncInterval: "10m"
auth:
  jwtSigningKeyFile: "./jwt.key"
ssh:
  host: "staging-usw-1.sealos.io"
  port: 2233
devbox:
  createDefaults:
    resource:
      cpu: "2000m"
      memory: "4096Mi"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	cfg, err := loadServerConfig(configPath)
	if err != nil {
		t.Fatalf("loadServerConfig failed: %v", err)
	}
	if cfg.JWTSigningKey != "jwt-secret-from-file" {
		t.Fatalf("unexpected jwt key from file: %q", cfg.JWTSigningKey)
	}
	if cfg.SSH.User != "devbox" {
		t.Fatalf("unexpected ssh default user: %s", cfg.SSH.User)
	}
	if cfg.Gateway.Port != defaultGatewayPort {
		t.Fatalf("unexpected default gateway port: %d", cfg.Gateway.Port)
	}
	if cfg.GatewayAddr != defaultGatewayServerAddr {
		t.Fatalf("unexpected default gateway listen address: %s", cfg.GatewayAddr)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Fatalf("unexpected default log level: %v", cfg.LogLevel)
	}
	if cfg.Gateway.PathPrefix != defaultGatewayPathPrefix {
		t.Fatalf("unexpected default gateway pathPrefix: %s", cfg.Gateway.PathPrefix)
	}
	if cfg.Gateway.SSEPath != defaultGatewaySSEPath {
		t.Fatalf("unexpected default gateway ssePath: %s", cfg.Gateway.SSEPath)
	}
	if cfg.CreateResource.Image != defaultCreateImage {
		t.Fatalf("unexpected default image: %s", cfg.CreateResource.Image)
	}
	if cfg.LifecycleResyncInterval != 10*time.Minute {
		t.Fatalf("unexpected lifecycleResyncInterval: %s", cfg.LifecycleResyncInterval)
	}
}

func TestLoadServerConfigNoJWTKey(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
server:
  listenAddress: ":18090"
auth: {}
ssh:
  host: "staging-usw-1.sealos.io"
  port: 2233
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	if _, err := loadServerConfig(configPath); err == nil {
		t.Fatalf("expected error when no jwt key configured")
	}
}

func TestLoadServerConfigRejectsInvalidLifecycleResyncInterval(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
server:
  lifecycleResyncInterval: "bad"
auth:
  jwtSigningKey: "jwt-secret"
ssh:
  host: "staging-usw-1.sealos.io"
  port: 2233
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	if _, err := loadServerConfig(configPath); err == nil {
		t.Fatalf("expected error for invalid lifecycleResyncInterval")
	}
}

func TestLoadServerConfigRejectsInvalidLogLevel(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
server:
  logLevel: "verbose"
auth:
  jwtSigningKey: "jwt-secret"
ssh:
  host: "staging-usw-1.sealos.io"
  port: 2233
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	if _, err := loadServerConfig(configPath); err == nil {
		t.Fatalf("expected error for invalid server.logLevel")
	}
}

func TestLoadServerConfigRejectsInvalidGatewaySSEPath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
auth:
  jwtSigningKey: "jwt-secret"
ssh:
  host: "staging-usw-1.sealos.io"
  port: 2233
gateway:
  ssePath: "sse"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	if _, err := loadServerConfig(configPath); err == nil {
		t.Fatalf("expected error for invalid gateway.ssePath")
	}
}

func TestLoadServerConfigRejectsInvalidGatewayPathPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
auth:
  jwtSigningKey: "jwt-secret"
ssh:
  host: "staging-usw-1.sealos.io"
  port: 2233
gateway:
  pathPrefix: "codex"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	if _, err := loadServerConfig(configPath); err == nil {
		t.Fatalf("expected error for invalid gateway.pathPrefix")
	}
}

func TestLoadServerConfigRejectsSameAPIAndGatewayListenAddress(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
server:
  listenAddress: ":8090"
  gatewayListenAddress: ":8090"
auth:
  jwtSigningKey: "jwt-secret"
ssh:
  host: "staging-usw-1.sealos.io"
  port: 2233
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	if _, err := loadServerConfig(configPath); err == nil {
		t.Fatalf("expected error for identical api and gateway listen addresses")
	}
}
