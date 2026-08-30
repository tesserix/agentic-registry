# syntax=docker/dockerfile:1

# ---- build the web UI ------------------------------------------------------
# CHART-11: pin the tesserix base images by digest. The :latest tags are
# mutable, so an unpinned build is not reproducible; publish immutable
# date/sha tags upstream and bump these digests deliberately.
FROM ghcr.io/tesserix/base-node-builder-22:latest@sha256:517372359a366695db0898e2295158cf4b227537377a264dc55a7faa623cfc29 AS web
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
FROM ghcr.io/tesserix/base-go-builder:latest@sha256:4ec5ce5894e501cc24f3a81329a8d548661e234be00b1f68205171bc6d048d1a AS build
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
FROM ghcr.io/tesserix/base-distroless-static:latest@sha256:0d68ca8037f210e82e211017dbf49b54cde04c42a47baa5c5e429dee07b29c14
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
