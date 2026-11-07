# 从仓库根目录 source 本文件；兼容 Bash/Zsh。只影响当前终端。
if [ ! -f go.mod ] || ! grep -q '^module github.com/Palm0palM/kubesql$' go.mod; then
  printf '%s\n' '请先进入 KubeSQL 仓库根目录。' >&2
  return 1
fi
export KSQL_ROOT="$PWD"
export GOEXPERIMENT=
export GOTOOLCHAIN=local
export GOCACHE="$KSQL_ROOT/tmp/go-cache"
export GOTMPDIR="$KSQL_ROOT/tmp/go-tmp"
export GOMODCACHE="$KSQL_ROOT/tmp/go-mod-cache"
export TMPDIR="$KSQL_ROOT/tmp/go-tmp"
export MINIKUBE_HOME="$KSQL_ROOT/tmp/minikube"
export KUBECONFIG="$KSQL_ROOT/tmp/live/kube/config"
export KSQL_BIN="$KSQL_ROOT/tmp/live/bin/ksql"
export PATH="$KSQL_ROOT/tmp/live/bin:$KSQL_ROOT/tmp/tools:$PATH"
export KSQL_E2E_KUBECONFIG="$KUBECONFIG"
export KSQL_E2E_CONTEXT=kubesql-test
export KSQL_E2E_BINARY="$KSQL_BIN"
