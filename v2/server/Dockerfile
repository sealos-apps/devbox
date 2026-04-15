FROM gcr.io/distroless/static:nonroot

ARG TARGETARCH=amd64

USER 65532:65532

COPY bin/devbox-api-${TARGETARCH} /usr/local/bin/devbox-api

EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/devbox-api"]
