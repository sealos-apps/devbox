SERVICE_NAME ?= ghcr.io/sealos-apps/devbox-v1-frontend
# Image URL to use all building/pushing image targets
IMG ?= $(SERVICE_NAME):latest
PNPM ?= pnpm
PNPM_INSTALL_FLAGS ?= --frozen-lockfile
CONTAINER_TOOL ?= docker
DOCKERFILE ?= Dockerfile
DOCKER_BUILD_ARGS ?=
BUILDX_BUILDER ?= devbox-builder
PLATFORMS ?= linux/amd64,linux/arm64

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: install
install: ## Install frontend dependencies.
	$(PNPM) install $(PNPM_INSTALL_FLAGS)

.PHONY: lint
lint: ## Run frontend lint checks.
	$(PNPM) run lint

.PHONY: typecheck
typecheck: ## Run TypeScript type checks.
	$(PNPM) run ts-lint

.PHONY: verify
verify: ## Run the standard frontend verification pipeline.
	$(MAKE) install
	$(MAKE) lint
	$(MAKE) typecheck
	$(MAKE) build

##@ Build

.PHONY: build
build: ## Build frontend application.
	$(PNPM) run build

.PHONY: dev
dev: ## Run the frontend in development mode.
	$(PNPM) run dev

.PHONY: run
run: ## Run the frontend in production mode.
	$(PNPM) run start

.PHONY: docker-build
docker-build: ## Build docker image for the frontend.
	$(CONTAINER_TOOL) build $(DOCKER_BUILD_ARGS) -t ${IMG} -f $(DOCKERFILE) .

.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the frontend for cross-platform support.
	- $(CONTAINER_TOOL) buildx create --name $(BUILDX_BUILDER)
	$(CONTAINER_TOOL) buildx use $(BUILDX_BUILDER)
	- $(CONTAINER_TOOL) buildx build --push $(DOCKER_BUILD_ARGS) --platform=$(PLATFORMS) --tag ${IMG} -f $(DOCKERFILE) .
	- $(CONTAINER_TOOL) buildx rm $(BUILDX_BUILDER)

.PHONY: docker-push
docker-push: ## Push docker image for the frontend.
	$(CONTAINER_TOOL) push ${IMG}
