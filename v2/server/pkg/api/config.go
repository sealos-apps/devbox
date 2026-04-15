package api

import (
	"bytes"
	"fmt"
	"log/slog"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	defaultServerAddr        = ":8090"
	defaultGatewayServerAddr = ":8091"
	defaultLifecycleResync   = 30 * time.Minute
	defaultCreateCPU         = "2000m"
	defaultCreateMemory      = "4096Mi"
	defaultCreateStorageSize = "10Gi"
	defaultCreateImage       = "ghcr.io/labring-actions/devbox-runtime-expt/python-3.12:v2.5.0-zh-cn"
	defaultGatewayPathPrefix = "/codex"
	defaultGatewayPort       = 1317
	defaultLogLevel          = slog.LevelInfo
)

type ServerConfig struct {
	Addr                    string
	GatewayAddr             string
	LogLevel                slog.Level
	LifecycleResyncInterval time.Duration
	JWTSigningKey           string
	SSH                     SSHConnectionConfig
	Gateway                 GatewayConfig
	CreateResource          CreateDevboxResourceConfig
}

type SSHConnectionConfig struct {
	User                string
	Host                string
	Port                int
	PrivateKeySecretKey string
}

type GatewayConfig struct {
	Domain     string
	PathPrefix string
	Port       int
}

type CreateDevboxResourceConfig struct {
	CPU          string
	Memory       string
	StorageLimit string
	Image        string
}

type fileConfig struct {
	Server  serverSection  `yaml:"server"`
	Auth    authSection    `yaml:"auth"`
	SSH     sshSection     `yaml:"ssh"`
	Gateway gatewaySection `yaml:"gateway"`
	Devbox  devboxSection  `yaml:"devbox"`
}

type serverSection struct {
	ListenAddress           string `yaml:"listenAddress"`
	GatewayListenAddress    string `yaml:"gatewayListenAddress"`
	LogLevel                string `yaml:"logLevel"`
	LifecycleResyncInterval string `yaml:"lifecycleResyncInterval"`
}

type authSection struct {
	JWTSigningKey     string `yaml:"jwtSigningKey"`
	JWTSigningKeyFile string `yaml:"jwtSigningKeyFile"`
}

type sshSection struct {
	User                string `yaml:"user"`
	Host                string `yaml:"host"`
	Port                int    `yaml:"port"`
	PrivateKeySecretKey string `yaml:"privateKeySecretKey"`
}

type gatewaySection struct {
	Domain     string `yaml:"domain"`
	PathPrefix string `yaml:"pathPrefix"`
	Port       int    `yaml:"port"`
	SSEPath    string `yaml:"ssePath"`
}

type devboxSection struct {
	CreateDefaults createDefaultsSection `yaml:"createDefaults"`
}

type createDefaultsSection struct {
	Resource     createResourceSection `yaml:"resource"`
	StorageLimit string                `yaml:"storageLimit"`
	Image        string                `yaml:"image"`
}

type createResourceSection struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

func loadServerConfig(configPath string) (ServerConfig, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return ServerConfig{}, fmt.Errorf("--config is required")
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("read config file %q failed: %w", configPath, err)
	}

	var fc fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fc); err != nil {
		return ServerConfig{}, fmt.Errorf("parse YAML config %q failed: %w", configPath, err)
	}

	jwtSigningKey, err := loadJWTSigningKey(fc.Auth, configPath)
	if err != nil {
		return ServerConfig{}, err
	}
	logLevel, err := parseLogLevel(fc.Server.LogLevel)
	if err != nil {
		return ServerConfig{}, err
	}

	lifecycleResyncInterval := defaultLifecycleResync
	if value := strings.TrimSpace(fc.Server.LifecycleResyncInterval); value != "" {
		parsed, parseErr := time.ParseDuration(value)
		if parseErr != nil {
			return ServerConfig{}, fmt.Errorf("invalid server.lifecycleResyncInterval %q: %w", value, parseErr)
		}
		if parsed <= 0 {
			return ServerConfig{}, fmt.Errorf("server.lifecycleResyncInterval must be greater than 0")
		}
		lifecycleResyncInterval = parsed
	}

	cpu := strings.TrimSpace(fc.Devbox.CreateDefaults.Resource.CPU)
	if cpu == "" {
		cpu = defaultCreateCPU
	}
	if _, err := resource.ParseQuantity(cpu); err != nil {
		return ServerConfig{}, fmt.Errorf("invalid devbox.createDefaults.resource.cpu %q: %w", cpu, err)
	}

	memory := strings.TrimSpace(fc.Devbox.CreateDefaults.Resource.Memory)
	if memory == "" {
		memory = defaultCreateMemory
	}
	if _, err := resource.ParseQuantity(memory); err != nil {
		return ServerConfig{}, fmt.Errorf("invalid devbox.createDefaults.resource.memory %q: %w", memory, err)
	}

	storageLimit := strings.TrimSpace(fc.Devbox.CreateDefaults.StorageLimit)
	if storageLimit == "" {
		storageLimit = defaultCreateStorageSize
	}
	if _, err := resource.ParseQuantity(storageLimit); err != nil {
		return ServerConfig{}, fmt.Errorf("invalid devbox.createDefaults.storageLimit %q: %w", storageLimit, err)
	}

	image := strings.TrimSpace(fc.Devbox.CreateDefaults.Image)
	if image == "" {
		image = defaultCreateImage
	}

	sshUser := strings.TrimSpace(fc.SSH.User)
	if sshUser == "" {
		sshUser = "devbox"
	}
	sshHost := strings.TrimSpace(fc.SSH.Host)
	if sshHost == "" {
		return ServerConfig{}, fmt.Errorf("ssh.host is required")
	}
	sshPort := fc.SSH.Port
	if sshPort <= 0 || sshPort > 65535 {
		return ServerConfig{}, fmt.Errorf("ssh.port must be in [1, 65535]")
	}
	privateKeySecretKey := strings.TrimSpace(fc.SSH.PrivateKeySecretKey)
	if privateKeySecretKey == "" {
		privateKeySecretKey = "SEALOS_DEVBOX_PRIVATE_KEY"
	}

	gatewayDomain, err := normalizeGatewayDomain(fc.Gateway.Domain)
	if err != nil {
		return ServerConfig{}, err
	}
	gatewayPathPrefix, err := normalizeGatewayPathPrefix(fc.Gateway.PathPrefix)
	if err != nil {
		return ServerConfig{}, err
	}
	gatewayPort := fc.Gateway.Port
	if gatewayPort == 0 {
		gatewayPort = defaultGatewayPort
	}
	if gatewayPort < 1 || gatewayPort > 65535 {
		return ServerConfig{}, fmt.Errorf("gateway.port must be in [1, 65535]")
	}

	cfg := ServerConfig{
		Addr:                    strings.TrimSpace(fc.Server.ListenAddress),
		GatewayAddr:             strings.TrimSpace(fc.Server.GatewayListenAddress),
		LogLevel:                logLevel,
		LifecycleResyncInterval: lifecycleResyncInterval,
		JWTSigningKey:           jwtSigningKey,
		SSH: SSHConnectionConfig{
			User:                sshUser,
			Host:                sshHost,
			Port:                sshPort,
			PrivateKeySecretKey: privateKeySecretKey,
		},
		Gateway: GatewayConfig{
			Domain:     gatewayDomain,
			PathPrefix: gatewayPathPrefix,
			Port:       gatewayPort,
		},
		CreateResource: CreateDevboxResourceConfig{
			CPU:          cpu,
			Memory:       memory,
			StorageLimit: storageLimit,
			Image:        image,
		},
	}
	if cfg.Addr == "" {
		cfg.Addr = defaultServerAddr
	}
	if cfg.GatewayAddr == "" {
		cfg.GatewayAddr = defaultGatewayServerAddr
	}
	if cfg.GatewayAddr == cfg.Addr {
		return ServerConfig{}, fmt.Errorf("server.gatewayListenAddress must differ from server.listenAddress")
	}
	return cfg, nil
}

func loadJWTSigningKey(authCfg authSection, baseConfigPath string) (string, error) {
	inlineKey := strings.TrimSpace(authCfg.JWTSigningKey)
	keyFile := strings.TrimSpace(authCfg.JWTSigningKeyFile)
	if inlineKey != "" && keyFile != "" {
		return "", fmt.Errorf("auth.jwtSigningKey and auth.jwtSigningKeyFile are mutually exclusive")
	}

	if inlineKey != "" {
		return inlineKey, nil
	}

	if keyFile != "" {
		if !filepath.IsAbs(keyFile) {
			keyFile = filepath.Join(filepath.Dir(baseConfigPath), keyFile)
		}
		content, err := os.ReadFile(keyFile)
		if err != nil {
			return "", fmt.Errorf("read auth.jwtSigningKeyFile %q failed: %w", keyFile, err)
		}
		fileKey := strings.TrimSpace(string(content))
		if fileKey == "" {
			return "", fmt.Errorf("auth.jwtSigningKeyFile %q is empty", keyFile)
		}
		return fileKey, nil
	}

	return "", fmt.Errorf("missing JWT signing key: set auth.jwtSigningKey or auth.jwtSigningKeyFile")
}

func normalizeGatewayDomain(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}

	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		parsed, err := neturl.Parse(value)
		if err != nil {
			return "", fmt.Errorf("invalid gateway.domain %q: %w", value, err)
		}
		if parsed.Host == "" {
			return "", fmt.Errorf("gateway.domain must include a host")
		}
		if parsed.Path != "" && parsed.Path != "/" {
			return "", fmt.Errorf("gateway.domain must not include a path")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("gateway.domain must not include query or fragment")
		}
		value = parsed.Host
	}

	if strings.Contains(value, "/") {
		return "", fmt.Errorf("gateway.domain must not include a path")
	}
	return value, nil
}

func normalizeGatewayPathPrefix(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = defaultGatewayPathPrefix
	}
	if !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("gateway.pathPrefix must start with '/'")
	}
	value = path.Clean(value)
	if value != "/" {
		value = strings.TrimRight(value, "/")
	}
	return value, nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return defaultLogLevel, nil
	}

	switch value {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid server.logLevel %q: must be one of debug, info, warn, error", raw)
	}
}
