# 测试与验收

## 离线测试

仓库根目录执行，需要 Go 1.27+：

```sh
go test -count=1 ./...
go vet ./...
```

涵盖 lexer/parser、AST/错误位置、WHERE 真值表与精度、绑定与写保护、HTTP kubeconfig/Discovery/CRUD/Metrics、部分失败汇总和 Review。默认不访问 Kubernetes。

## 真实集群 E2E

**只能在独立测试集群运行。** 测试会创建并删除 Namespace、工作负载、CRD/CR，并使用 impersonation 检查 RBAC。需要有相应权限的测试凭据；context 名称检查不能证明连接的是安全集群，运行前自行确认。

已验证组合：minikube 1.39.0、Docker driver、Kubernetes 1.35.0、Metrics Server 0.9.0。请预先安装 Go、Docker、minikube、kubectl，并能拉取集群镜像、busybox:1.36 和 Metrics Server 镜像。

从仓库根目录准备独立 profile，避免修改默认 kubeconfig：

```sh
mkdir -p tmp/kube tmp/bin
export MINIKUBE_HOME="$PWD/tmp/minikube"
export KUBECONFIG="$PWD/tmp/kube/config"
minikube start -p kubesql-test --driver=docker --kubernetes-version=v1.35.0 \
  --cpus=2 --memory=3072
minikube addons enable metrics-server -p kubesql-test
kubectl --context kubesql-test rollout status deployment/metrics-server \
  -n kube-system --timeout=180s
kubectl --context kubesql-test get nodes
kubectl --context kubesql-test get apiservice v1beta1.metrics.k8s.io
```

需要节点名/context 均为 `kubesql-test`，Metrics APIService Available=True。镜像拉取失败时应先修复环境或自行加载可信镜像；不能用离线 mock 代替真实指标验收。

构建并运行：

```sh
go build -o tmp/bin/ksql ./cmd/ksql
KSQL_E2E_KUBECONFIG="$PWD/tmp/kube/config" \
KSQL_E2E_CONTEXT=kubesql-test \
KSQL_E2E_BINARY="$PWD/tmp/bin/ksql" \
go test -tags=e2e -count=1 -v ./test/e2e
```

二进制路径必须绝对，必须明确提供专用 context；配置缺失时测试失败，不跳过或使用默认集群。测试通常需要数分钟，指标采集有独立 180 秒上限。

### 资源所有权与清理

- 拒绝覆盖已有测试 Namespace：sql-easy-select、sql-easy-where、sql-medium-write、sql-medium-delete、sql-medium-insert、sql-medium-insert-ingress、sql-hard-native、sql-hard-metrics、sql-hard-crd、sql-hard-cluster-crd。
- CRD 用例拒绝已有 gadgets.lab.example.com/clusternotes.lab.example.com；只清理本次创建并登记 UID 的定义。
- 不要先手动 apply 目标 YAML。第 5 章 preparation 与 targets 分开；第 8 章必须按 CRD → Established → Discovery → 实例顺序准备。
- 正常结束及测试失败都会清理测试资源。进程被强制杀死或 API 中断可能留下资源，此时核实所有权后再手动处理，不能全局删除 Namespace/CRD。
- Metrics Server 和测试集群保留。夹具里的 Secret 是明确的虚构数据，真实凭据只在忽略的 tmp/ 内。

## 验收映射

| 任务书用例 | 实际测试 | 检查 |
| --- | --- | --- |
| 1-1 / 1-2 | internal/sql/parser_test.go | JSON AST、星号节点、错误行列 |
| 2-1 / 2-2 | TestEasySelect | Namespace 包含断言，Deployment/Ingress 查询 |
| 3-1 / 3-2 | TestEasyWhere | 优先级、括号、IS NULL 与 = NULL、NOT UNKNOWN |
| 4-1 / 4-2 | TestMediumUpdate / TestMediumDelete | 三表精确写入/字段保留、独立删除、finalizer |
| 5-1 / 5-2 | TestMediumInsertNamespaceDeployment / TestMediumInsertIngressAlreadyExists | SQL 创建、目标字段、重复创建不覆盖 |
| 6-1 | TestHardNative | 五种资源查询、ConfigMap 更新、通用 CRUD |
| 7-1 | TestHardMetrics；internal/kube/metrics_test.go | 双容器 Ready、真实指标；固定 Quantity 样本 |
| 8-1 / 8-2 | TestHardCRDNamespaced / TestHardCRDCluster | 陌生 CR CRUD、集群 scope、CRD 自身 CRUD |

补充验收覆盖：任务书列出的全部 18 个内置资源族读取路由（允许空列表，不声称每类工作负载都已部署），大于 2^53 的整数 INSERT/SQL UPDATE/CAST UPDATE/WHERE，真实 nullable 字段的 JSON null 保留与 SQL NULL 移除，多 served version、schema/不可变字段拒绝、RBAC、版本前置条件、无名称 Review。

聚合 API 部分组失败、逐对象 Conflict/Invalid/Forbidden 后继续及缺 verb 由 HTTP 测试验证；没有额外安装真实自定义聚合 API 服务。

### 样例与真实服务器差异

最终阶段 SELECT * 包含后续新增公开列，不能继续与第 2 章的早期列集合做完全相等比较。测试另外验证完整 manifest，再比较早期便捷列。

Kubernetes 会添加 Deployment revision 注解、Namespace 名称标签和 kube-root-ca.crt ConfigMap。CLI 不隐藏这些真实数据；对应测试仅处理明确的服务器生成字段/对象，同时仍严格检查用户字段及其他额外数据。

INSERT 只比较目标明确字段，不比较服务器默认值/时间戳。真实 Metrics 数值不必等于固定样本，只检查身份、一行与非负整数；不拿 requests/limits 作指标。

## 质量检查

使用兼容 Go 1.27 的工具版本（如本地重建所用 x/tools v0.51.0）；工具不可用不能声称检查通过：

```sh
staticcheck ./...
staticcheck -tags=e2e ./...
go fix -diff ./...
go fix -diff -tags=e2e ./test/e2e
modernize ./...
GOFLAGS=-tags=e2e modernize ./test/e2e
deadcode ./...
deadcode -test ./...
```

审阅建议后再修改，不自动套用或删除代码。日志、二进制、缓存和本地配置放 tmp/；如果把 Go module 缓存也放在 tmp/，先用一个独立 tmp/go.mod 隔离它，防止根目录 ./... 扫描缓存源码。

未提供 CI、全面 fuzz 或所有版本/所有 schema 的兼容性保证。业务镜像/网络/Ingress 流量、PVC 绑定不属于这些 SQL 任务的通过条件；Metrics 的双容器 Pod Ready 已单独验证。
