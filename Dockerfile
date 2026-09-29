# codepilot — сборка CLI и нативного рантайма onnxruntime для контейнера.
# Модели в образ не входят: монтируются в ./models (см. docker-compose.yml).

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/codepilot ./cmd/codepilot

FROM debian:bookworm-slim
ARG TARGETARCH
ARG ORT_VERSION=1.23.1
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN set -e; \
    case "$TARGETARCH" in \
      arm64) A=aarch64 ;; \
      amd64) A=x64 ;; \
      *) echo "unsupported TARGETARCH=$TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-${A}-${ORT_VERSION}.tgz" -o /tmp/ort.tgz; \
    tar -xzf /tmp/ort.tgz -C /tmp; \
    cp -P /tmp/onnxruntime-linux-${A}-${ORT_VERSION}/lib/libonnxruntime.so* /usr/local/lib/; \
    ln -sf "libonnxruntime.so.${ORT_VERSION}" /usr/local/lib/libonnxruntime.so; \
    ldconfig; \
    rm -rf /tmp/ort*
COPY --from=build /out/codepilot /usr/local/bin/codepilot
WORKDIR /work
ENTRYPOINT ["codepilot"]
CMD ["help"]
