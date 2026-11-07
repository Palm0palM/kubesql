#!/usr/bin/env bash
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"
source docs/acceptance/env.sh
reuse=false
metrics=false
for arg in "$@"; do
  case "$arg" in
    --reuse) reuse=true ;;
    --metrics) metrics=true ;;
    *) printf '%s\n' '用法：bash docs/acceptance/cluster.sh [--reuse] [--metrics]' >&2; exit 2 ;;
  esac
done
for tool in docker minikube kubectl; do
  command -v "$tool" >/dev/null || { printf '缺少工具：%s\n' "$tool" >&2; exit 1; }
done
mkdir -p tmp/live/kube "$GOTMPDIR"
docker info >/dev/null
# 不删除已有 profile。复用要求显式确认，不能把“复用”讲成“新建”。
if [[ ( -d "$MINIKUBE_HOME/.minikube/profiles/kubesql-test" || -d "$MINIKUBE_HOME/profiles/kubesql-test" ) && "$reuse" != true ]]; then
  printf '%s\n' 'kubesql-test 已存在。核实它是专用测试集群后加 --reuse；本脚本不会删除它。' >&2
  exit 1
fi
umask 077
minikube start -p kubesql-test --driver=docker --kubernetes-version=v1.35.0 --cpus=2 --memory=3072
chmod 600 "$KUBECONFIG"
kubectl --context kubesql-test wait --for=condition=Ready node/kubesql-test --timeout=180s
minikube status -p kubesql-test
kubectl --context kubesql-test get nodes -o wide
if [[ "$metrics" == true ]]; then
  minikube addons enable metrics-server -p kubesql-test
  kubectl --context kubesql-test -n kube-system rollout status deployment/metrics-server --timeout=180s
  kubectl --context kubesql-test wait --for=condition=Available apiservice/v1beta1.metrics.k8s.io --timeout=180s
fi
printf '\n专用集群就绪。kubeconfig：%s\n' "$KUBECONFIG"
printf '%s\n' '下一步：source docs/acceptance/env.sh；核实 API 地址与节点后再运行演示。'
