<div align="center">
  <img src="./v2/frontend/public/logo.svg" alt="DevBox logo" width="96" />

  <h1>DevBox</h1>
  <p>Cloud development workspace platform for Sealos</p>
</div>

This repository contains the DevBox codebase split by version (`v1` and `v2`), with each version including:
- a `frontend` app (Next.js)
- a `controller` service (Kubernetes operator)

> [!NOTE]
> `v2` is the current development line and should be preferred for new work.

Repository: `github.com/sealos-apps/devbox`

## Overview

DevBox provides a cloud IDE/workspace experience on Kubernetes. The frontend handles user interactions (creating DevBox instances, runtime/template management, release flow, domain and SSH settings), while the controller reconciles CRDs in the cluster.

## Repository Layout

| Path | Description |
| --- | --- |
| `v1/frontend` | DevBox v1 web app (Next.js + TypeScript) |
| `v1/controller` | DevBox v1 Kubernetes controller (Kubebuilder project) |
| `v2/frontend` | DevBox v2 web app (Next.js + TypeScript, current) |
| `v2/controller` | DevBox v2 Kubernetes controller (Kubebuilder project, current) |

## Prerequisites

- Node.js 20+
- pnpm 10+
- Go 1.24+
- Docker 17.03+
- kubectl with access to a Kubernetes cluster

## Quick Start (v2)

### 1. Run frontend

```bash
cd v2/frontend
cp .env.template .env.local
pnpm install
pnpm dev
```

Frontend runs on `http://localhost:3000` by default.

> [!IMPORTANT]
> Before running features that require cluster access, configure `.env.local` with at least:
> `NEXT_PUBLIC_MOCK_USER`, `SEALOS_DOMAIN`, and related backend endpoints (`DATABASE_URL`, `METRICS_URL`, `ACCOUNT_URL`, `RETAG_SVC_URL`) based on your environment.

### 2. Run controller

```bash
cd v2/controller
make run
```

## Common Commands

### `v2/frontend`

```bash
pnpm dev        # start dev server
pnpm build      # production build
pnpm start      # run production server
pnpm lint       # lint
pnpm ts-lint    # type check
```

### `v2/controller`

```bash
make test         # run tests
make build        # build manager binary
make docker-build # build controller image
make deploy       # deploy controller to cluster
make undeploy     # remove controller from cluster
```

To see all available targets:

```bash
make help
```

## Images

Default image names now follow the new repository naming:

- `ghcr.io/sealos-apps/devbox-v1-controller:latest`
- `ghcr.io/sealos-apps/devbox-v1-frontend:latest`
- `ghcr.io/sealos-apps/devbox-v2-controller:latest`
- `ghcr.io/sealos-apps/devbox-v2-frontend:latest`

You can override these at build or deploy time with `IMG=...` for controllers and `IMG=...` for frontends.

## Release

GitHub Actions workflows live under [`.github/workflows`](/Users/yy/archary/sealos-devbox/.github/workflows) and are split into three stages:

- `CI`: validates `v1/v2` controllers and frontends on pull requests and pushes to `main`
- `Images`: builds and pushes the four GHCR images on `main`, tags, or manual dispatch
- `Release`: publishes a GitHub Release on `v*` tags and attaches generated controller manifests

Tagging a release such as `v1.2.3` will publish:

- `ghcr.io/sealos-apps/devbox-v1-controller:v1.2.3`
- `ghcr.io/sealos-apps/devbox-v1-frontend:v1.2.3`
- `ghcr.io/sealos-apps/devbox-v2-controller:v1.2.3`
- `ghcr.io/sealos-apps/devbox-v2-frontend:v1.2.3`

The release workflow also uploads controller manifest bundles generated from:

- `v1/controller`
- `v2/controller`

If you need to publish manually, you can still run the local make targets:

```bash
cd v2/controller
make docker-build docker-push IMG=ghcr.io/sealos-apps/devbox-v2-controller:<tag>

cd ../../v2/frontend
make docker-build docker-push IMG=ghcr.io/sealos-apps/devbox-v2-frontend:<tag>
```

## Working with v1

If you need the legacy line, use the same workflow in `v1/frontend` and `v1/controller`.

## Additional Docs

- [`v2/frontend/README.md`](./v2/frontend/README.md)
- [`v2/controller/README.md`](./v2/controller/README.md)
- [`v1/frontend/README.md`](./v1/frontend/README.md)
- [`v1/controller/README.md`](./v1/controller/README.md)

> [!TIP]
> Frontend packages in this repo are consumed from npm (published `@labring/*` packages). Avoid `yalc link` / `yalc remove` workflows here.
