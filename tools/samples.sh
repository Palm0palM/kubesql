#!/usr/bin/env bash
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
source tools/env-config.sh
if (( $# != 1 )); then
  printf '%s\n' '用法：bash tools/samples.sh {1|2|3|4-update|4-delete|5-create|5-duplicate|6|7|8-ns|8-cluster|all}' >&2
  exit 2
fi
case "$1" in
  1) go test -count=1 -v ./internal/sql -run 'TestParseSelectAST|TestParseErrorJSON'; exit ;;
  2) pattern='^TestEasySelect$' ;;
  3) pattern='^TestEasyWhere$' ;;
  4-update) pattern='^TestMediumUpdate$' ;;
  4-delete) pattern='^TestMediumDelete$' ;;
  5-create) pattern='^TestMediumInsertNamespaceDeployment$' ;;
  5-duplicate) pattern='^TestMediumInsertIngressAlreadyExists$' ;;
  6) pattern='^TestHardNative$' ;;
  7) pattern='^TestHardMetrics$' ;;
  8-ns) pattern='^TestHardCRDNamespaced$' ;;
  8-cluster) pattern='^TestHardCRDCluster$' ;;
  all) pattern='.' ;;
  *) printf '未知用例：%s\n' "$1" >&2; exit 2 ;;
esac
[[ -x "$KSQL_BIN" && -f "$KUBECONFIG" ]] || { printf '%s\n' '请先构建并准备测试集群。' >&2; exit 1; }
printf '%s\n' '将对 kubesql-test 执行真实 E2E，创建/修改/删除测试对象；请勿并行修改 sql-* Namespace 或 lab.example.com CRD。'
printf '用例过滤：%s\n' "$pattern"
go test -tags=e2e -count=1 -v ./test/e2e -run "$pattern"
