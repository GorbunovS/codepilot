# codepilot — self-contained образ: CLI, onnxruntime, e5-small и Laya ONNX.
# Модели скачиваются и экспортируются на этапе сборки, финальный образ
# содержит только готовые файлы.

# Stage 1: сборка CLI.
FROM golang:1.26 AS go-build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/codepilot ./cmd/codepilot

# Stage 2: скачивание e5-small.
FROM debian:bookworm-slim AS e5-download
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /models
COPY scripts/download_models.sh .
RUN sed -i 's/\r$//' download_models.sh && chmod +x download_models.sh && E5_DIR=/models/e5-small ./download_models.sh fp32

# Stage 3: экспорт Laya в ONNX.
FROM python:3.12-slim AS laya-export
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /export
COPY tools/laya-export/requirements.txt .
RUN sed -i 's/\r$//' requirements.txt \
    && pip install --no-cache-dir -r requirements.txt
COPY scripts/download_laya_src.sh .
RUN sed -i 's/\r$//' download_laya_src.sh && chmod +x download_laya_src.sh && ./download_laya_src.sh
COPY tools/laya-export/export_onnx.py .
RUN sed -i 's/\r$//' export_onnx.py \
    && python export_onnx.py /export/models/laya-multilingual-src /export/models/laya-multilingual

# Stage 4: финальный образ.
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
COPY --from=go-build /out/codepilot /usr/local/bin/codepilot
COPY --from=e5-download /models/e5-small /work/models/e5-small
COPY --from=laya-export /export/models/laya-multilingual /work/models/laya-multilingual
WORKDIR /work
ENTRYPOINT ["codepilot"]
CMD ["help"]
