IMG ?= ghcr.io/labring/devbox-api:latest
TARGETOS ?= linux
TARGETARCH ?= amd64
GOCACHE ?= /tmp/go-build
CONFIG ?= $(SERVER_DIR)/config/config.yaml

SERVER_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BIN_DIR ?= $(SERVER_DIR)/bin
BINARY ?= $(BIN_DIR)/devbox-api-$(TARGETARCH)

SHELL := /usr/bin/env bash
.SHELLFLAGS := -ec

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make -C v2/server \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: fmt
fmt: ## Run go fmt for server code
	cd $(SERVER_DIR) && GOCACHE=$(GOCACHE) go fmt ./...

.PHONY: vet
vet: ## Run go vet for server code
	cd $(SERVER_DIR) && GOCACHE=$(GOCACHE) go vet ./...

.PHONY: test
test: ## Run tests for server code
	cd $(SERVER_DIR) && GOCACHE=$(GOCACHE) go test ./...

.PHONY: build
build: ## Build server binary to bin/devbox-api-<arch>
	mkdir -p $(BIN_DIR)
	cd $(SERVER_DIR) && GOCACHE=$(GOCACHE) CGO_ENABLED=0 GOOS=$(TARGETOS) GOARCH=$(TARGETARCH) \
		go build -trimpath -ldflags "-s -w" -o $(BINARY) ./cmd/devbox-api

.PHONY: run
run: ## Run server from source
	cd $(SERVER_DIR) && GOCACHE=$(GOCACHE) go run ./cmd/devbox-api --config $(CONFIG)

.PHONY: docker-build
docker-build: build ## Build docker image from local binary
	docker build \
		-f $(SERVER_DIR)/Dockerfile \
		-t $(IMG) \
		--build-arg TARGETARCH=$(TARGETARCH) \
		$(SERVER_DIR)

.PHONY: docker-push
docker-push: ## Push docker image
	docker push $(IMG)
