# syntax=docker/dockerfile:1

# ---- build the web UI ------------------------------------------------------
# CHART-11: pin the tesserix base images by digest. The :latest tags are
# mutable, so an unpinned build is not reproducible; publish immutable
# date/sha tags upstream and bump these digests deliberately.
FROM ghcr.io/tesserix/base-node-builder-22:latest@sha256:a8d70bc7af6346aeb8eef23c58a1c72b411facf6f3bb4a0924a1610fb7a730e6 AS web
USER 0:0
# CI=true lets pnpm reconcile/purge node_modules non-interactively (no TTY in
# the build), e.g. when the lockfile changed since a cached install layer.
ENV CI=true
WORKDIR /web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml* web/pnpm-workspace.yaml* ./
RUN pnpm install --frozen-lockfile || pnpm install
COPY web/ ./
RUN pnpm build

# ---- build the Go server (static binary) -----------------------------------
# CHART-11: pin by digest (see note above).
FROM ghcr.io/tesserix/base-go-builder:latest@sha256:40fbd9b59ef20100c90213a5390332c48fa4ef7d5908223f4f088cb4ab476972 AS build
USER 0:0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
# -trimpath makes the paths the runtime reports module-relative rather than
# describing this builder's filesystem, which is what lets an error be mapped
# onto a file in the repo at the commit this image was built from.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X github.com/tesserix/agentic-registry/internal/config.Version=${VERSION}" \
    -trimpath \
    -o /out/agentic-registry ./cmd/agentic-registry

# The capability prober ships in the same image: it shares the export adapter
# with the server, so a route the gateway serves and a route the probe dials
# can never drift apart across two independently built artifacts.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w" \
    -trimpath \
    -o /out/agentic-probe ./cmd/agentic-probe

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w" \
    -trimpath \
    -o /out/agentgateway-sync ./cmd/agentgateway-sync

# Named runtime user `aregistry` at uid 1000 (tesserix backend convention,
# same uid devai/fanzone use). distroless has no shell to adduser, so the
# passwd/group entries are created here and copied into the final image.
RUN addgroup -g 1000 aregistry \
    && adduser -u 1000 -G aregistry -D -H aregistry

# ---- final: distroless static ----------------------------------------------
# CHART-11: pin by digest (see note above).
FROM ghcr.io/tesserix/base-distroless-static:latest@sha256:ccf26e72c62d53b69caae2ab73f3b059fe46785c0e5c18868e103b63c52a729f
WORKDIR /app
COPY --from=build /etc/passwd /etc/group /etc/
COPY --from=build --chown=aregistry:aregistry /out/agentic-registry /app/agentic-registry
COPY --from=build --chown=aregistry:aregistry /out/agentic-probe    /app/agentic-probe
COPY --from=build --chown=aregistry:aregistry /out/agentgateway-sync /app/agentgateway-sync
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
