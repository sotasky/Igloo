# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-bookworm AS build
ARG DEBIAN_FRONTEND=noninteractive

WORKDIR /src

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates nodejs npm \
    && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum package.json package-lock.json ./
RUN go mod download
RUN npm ci
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1001

COPY cmd ./cmd
COPY internal ./internal
COPY locales ./locales
COPY static ./static
COPY scripts/dev/esbuild.mjs ./scripts/dev/esbuild.mjs
RUN templ generate
RUN node scripts/dev/esbuild.mjs
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/igloo ./cmd/igloo \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/igloo-adduser ./cmd/adduser \
    && mkdir -p /out/static /out/locales \
    && cp -a static/. /out/static/ \
    && cp -a locales/. /out/locales/

FROM debian:bookworm-slim AS runtime
ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl ffmpeg python3 python3-venv \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m venv /opt/igloo-py \
    && /opt/igloo-py/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/igloo-py/bin/pip install --no-cache-dir yt-dlp gallery-dl

ENV PATH="/opt/igloo-py/bin:${PATH}" \
    IGLOO_DATA_DIR=/data \
    IGLOO_CONFIG_DIR=/config \
    IGLOO_REPO_DIR=/app \
    IGLOO_PORT=5001 \
    IGLOO_ENABLED_PLATFORMS=all

WORKDIR /app
COPY --from=build /out/igloo /usr/local/bin/igloo
COPY --from=build /out/igloo-adduser /usr/local/bin/igloo-adduser
COPY --from=build /out/locales /app/locales
COPY --from=build /out/static /app/static

RUN mkdir -p /data /config

VOLUME ["/data", "/config"]
EXPOSE 5001

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD curl -fsS http://127.0.0.1:5001/api/health >/dev/null || exit 1

CMD ["igloo"]
