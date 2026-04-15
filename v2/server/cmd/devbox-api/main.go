package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/sealos-apps/devbox/v2/server/pkg/api"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	var configPath string
	flag.StringVar(&configPath, "config", "/etc/devbox-server/config/config.yaml", "path to YAML config file")
	flag.Parse()

	if err := api.Run(configPath); err != nil {
		slog.Error("devbox api server exited", "error", err)
		os.Exit(1)
	}
}
