# 在项目根目录执行：source tools/env-config.sh
if [ ! -f go.mod ] || ! grep -q '^module github.com/Palm0palM/kubesql$' go.mod; then
  printf '%s\n' '请先 cd 到 KubeSQL 项目根目录。' >&2
  return 1
fi
export PATH="$PWD/tools:$PATH"
export MINIKUBE_HOME="$PWD/tmp/minikube"
export KUBECONFIG="$PWD/tools/kubeconfig"
export GOCACHE="$PWD/tmp/go-cache"
export GOMODCACHE="$PWD/tmp/go-mod-cache"
export GOEXPERIMENT=
export GOTOOLCHAIN=local
unset GOTMPDIR TMPDIR
# 为本地 API/节点绕过代理；不改全局代理配置。
export NO_PROXY="${NO_PROXY:+$NO_PROXY,}localhost,127.0.0.1,::1,192.168.49.2,192.168.49.0/24,10.96.0.0/12,10.244.0.0/16"
export no_proxy="$NO_PROXY"
export KSQL_BIN="$PWD/ksql"
export KSQL_E2E_BINARY="$KSQL_BIN"
export KSQL_E2E_KUBECONFIG="$KUBECONFIG"
export KSQL_E2E_CONTEXT=kubesql-test
