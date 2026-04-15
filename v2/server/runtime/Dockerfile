FROM registry.cn-hangzhou.aliyuncs.com/lingdie/devbox-sandbox:3.31

ARG CODEX_VERSION=latest

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bubblewrap \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @openai/codex@${CODEX_VERSION} \
    && mkdir -p /codex-home \
    && codex --version
