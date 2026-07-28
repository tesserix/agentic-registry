# syntax=docker/dockerfile:1

# ---- build the web UI ------------------------------------------------------
# CHART-11: pin the tesserix base images by digest. The :latest tags are
# mutable, so an unpinned build is not reproducible; publish immutable
# date/sha tags upstream and bump these digests deliberately.
FROM ghcr.io/tesserix/base-node-builder-22:latest@sha256:a7a88f29a9f369b2b34a9d2f824b6c7e828e35adb2d31002c1b12ec2366b37bf AS web
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
FROM ghcr.io/tesserix/base-go-builder:latest@sha256:3e7866f9ed93fd4dee38b62a60c02dd9559d494d78e821e1205024d29aa72b2b AS build
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

# Named runtime user `aregistry` at uid 1000 (tesserix backend convention,
# same uid devai/fanzone use). distroless has no shell to adduser, so the
# passwd/group entries are created here and copied into the final image.
RUN addgroup -g 1000 aregistry \
    && adduser -u 1000 -G aregistry -D -H aregistry

# ---- final: distroless static ----------------------------------------------
# CHART-11: pin by digest (see note above).
FROM ghcr.io/tesserix/base-distroless-static:latest@sha256:960645da83d2afedcaf1a217cc5d816f242cefd708d627faffb8b5571c10833e
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
