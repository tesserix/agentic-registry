#!/usr/bin/env bash
# Build + load + deploy Agentic Registry to a local kind cluster.
#
# Two paths:
#   ./scripts/kind-deploy.sh            # helm install (fast, no Argo needed)
#   ./scripts/kind-deploy.sh --argo     # apply the ArgoCD Application instead
#
# Prereqs: kind cluster running, kubectl context pointing at it, docker, helm.
# (For --argo: Argo CD already installed in the cluster.)
set -euo pipefail

cd "$(dirname "$0")/.."

NS="${NS:-agentic-registry}"
IMAGE="${IMAGE:-agentic-registry:local}"
KIND_CLUSTER="${KIND_CLUSTER:-kind}"
RELEASE="${RELEASE:-agentic-registry}"
MODE="${1:-helm}"
# The Helm chart lives in the tesserix-k8s repo (sibling dir), per platform
# convention — not in this app repo. Override with CHART=... if your layout differs.
CHART="${CHART:-../tesserix-k8s/charts/apps/agentic-registry}"

echo "==> Building image $IMAGE"
docker build -f deploy/Dockerfile -t "$IMAGE" --build-arg VERSION=local .

echo "==> Loading image into kind cluster '$KIND_CLUSTER'"
kind load docker-image "$IMAGE" --name "$KIND_CLUSTER"

echo "==> Ensuring namespace $NS"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

echo "==> Applying secret"
if [[ -f k8s/secrets.yaml ]]; then
  kubectl apply -f k8s/secrets.yaml
else
  echo "!! k8s/secrets.yaml not found."
  echo "   cp k8s/secrets.example.yaml k8s/secrets.yaml, edit it, then re-run."
  exit 1
fi

if [[ "$MODE" == "--argo" ]]; then
  echo "==> Applying ArgoCD Application (GitOps path)"
  kubectl apply -f k8s/argocd-application-local.yaml
  echo "   Argo will sync from the repoURL set in that file."
else
  if [[ ! -d "$CHART" ]]; then
    echo "!! chart not found at $CHART"
    echo "   set CHART=/path/to/tesserix-k8s/charts/apps/agentic-registry and re-run."
    exit 1
  fi
  echo "==> helm upgrade --install $RELEASE (chart: $CHART)"
  helm upgrade --install "$RELEASE" "$CHART" \
    --namespace "$NS" \
    -f "$CHART/values-local.yaml" \
    --wait --timeout 180s
fi

echo "==> Done. Reach it with:"
echo "   kubectl -n $NS port-forward svc/$RELEASE 8080:8080"
echo "   open http://localhost:8080   (UI)   |   curl localhost:8080/v0/health"
