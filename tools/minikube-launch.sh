#!/usr/bin/env bash
# 可选快捷入口；验收指南也给出等价的手敲命令。
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
source tools/env-config.sh
if [[ ! -f "$MINIKUBE_HOME/.minikube/profiles/kubesql-test/config.json" ]]; then
  printf '%s\n' '找不到已恢复的 profile，停止。不会自动新建集群。' >&2
  exit 1
fi
minikube start -p kubesql-test --kubernetes-version=v1.35.0 \
  --base-image=gcr.io/k8s-minikube/kicbase:v0.0.51 \
  --preload=false --cache-images=false
minikube status -p kubesql-test
kubectl wait --for=condition=Ready pod --all -n kube-system --timeout=180s
kubectl --context kubesql-test get nodes
