# syntax=docker/dockerfile:1

# ---- build the web UI ------------------------------------------------------
FROM ghcr.io/tesserix/base-node-builder-22:latest AS web
USER 0:0
WORKDIR /web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml* web/pnpm-workspace.yaml* ./
RUN pnpm install --frozen-lockfile || pnpm install
COPY web/ ./
RUN pnpm build

# ---- build the Go server (static binary) -----------------------------------
FROM ghcr.io/tesserix/base-go-builder:latest AS build
USER 0:0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X github.com/tesserix/agentic-registry/internal/config.Version=${VERSION}" \
    -o /out/agentic-registry ./cmd/agentic-registry

# Named runtime user `aregistry` at uid 1000 (tesserix backend convention,
# same uid devai/fanzone use). distroless has no shell to adduser, so the
# passwd/group entries are created here and copied into the final image.
RUN addgroup -g 1000 aregistry \
    && adduser -u 1000 -G aregistry -D -H aregistry

# ---- final: distroless static ----------------------------------------------
FROM ghcr.io/tesserix/base-distroless-static:latest
WORKDIR /app
COPY --from=build /etc/passwd /etc/group /etc/
COPY --from=build --chown=aregistry:aregistry /out/agentic-registry /app/agentic-registry
COPY --from=web   --chown=aregistry:aregistry /web/dist             /app/web
ENV ADDR=:8080 \
    STORE_BACKEND=postgres \
    WEB_DIR=/app/web
EXPOSE 8080
# Numeric form so Kubernetes' runAsNonRoot check can verify uid>0 at
# admission (it can't resolve a name); the copied passwd maps 1000 ->
# aregistry, so the process still runs as the named `aregistry` user.
USER 1000:1000
ENTRYPOINT ["/app/agentic-registry"]
