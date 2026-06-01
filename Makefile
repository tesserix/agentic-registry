BINARY      := agentic-registry
CLI         := agentic
PKG         := github.com/tesserix/agentic-registry
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X $(PKG)/internal/config.Version=$(VERSION)
CLI_LDFLAGS := -s -w -X main.Version=$(VERSION)

KIND_CLUSTER ?= kind
NS           ?= agentic-registry
# Helm chart lives in the tesserix-k8s repo (sibling dir), per platform convention.
CHART        ?= ../tesserix-k8s/charts/apps/agentic-registry

.PHONY: all build cli release-snapshot run test lint fmt vet tidy docker clean kind-image kind-deploy kind-argo helm-lint local-secret

all: build cli

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/agentic-registry

cli:
	go build -ldflags "$(CLI_LDFLAGS)" -o bin/$(CLI) ./cmd/agentic

release-snapshot:                   ## local GoReleaser dry-run (no publish)
	goreleaser release --snapshot --clean

run:
	go run ./cmd/agentic-registry

test:
	go test -race ./...

lint: fmt vet

fmt:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

vet:
	go vet ./...

tidy:
	go mod tidy

docker:
	docker build -f deploy/Dockerfile -t agentic-registry:$(VERSION) .

clean:
	rm -rf bin/

# ---- local kind deploy ------------------------------------------------------

local-secret:                       ## create k8s/secrets.yaml from the example
	@test -f k8s/secrets.yaml || cp k8s/secrets.example.yaml k8s/secrets.yaml
	@echo "edit k8s/secrets.yaml then run: make kind-deploy"

kind-image:                         ## build + load the image into kind
	docker build -f deploy/Dockerfile -t agentic-registry:local --build-arg VERSION=local .
	kind load docker-image agentic-registry:local --name $(KIND_CLUSTER)

kind-deploy:                        ## full local deploy via helm (build+load+install)
	./scripts/kind-deploy.sh

kind-argo:                          ## full local deploy via the ArgoCD Application
	./scripts/kind-deploy.sh --argo

helm-lint:                          ## render + lint the chart (in tesserix-k8s) with local values
	helm lint $(CHART) -f $(CHART)/values-local.yaml
	helm template ar $(CHART) -n $(NS) -f $(CHART)/values-local.yaml >/dev/null && echo "template OK"
