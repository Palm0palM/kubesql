# KubeSQL

把 SQL 作为操作 Kubernetes 的前端语言，命令行程序名为 `ksql`。

项目依据本地任务书 `docs/KubeSQL.pdf`，优先完成基础查询与过滤的可验收闭环，
再按剩余时间和审查确认扩展写操作、动态资源、Metrics API 和 CRD。
它不是关系型数据库，不提供事务、JOIN 或 ACID 保证。

## 当前状态

M0 已完成；M1 的独立 SQL 解析包已实现并完成代码审查。
当前入口只报告 SQL 执行尚未实现并退出（退出码 1），不连接集群。
已验证 Namespace、Deployment、Service、Ingress 的 API 创建、读取和测试命名空间清理。
SQL 查询执行尚未实现；这些是环境冒烟检查，不是 SQL E2E 通过。

本地设计与计划保存在被忽略的 `docs/` 中，不提供远端不可用的文档链接。

## 代码结构

```text
cmd/ksql/       最小命令行入口及行为测试
internal/sql/   token、lexer、SELECT AST、parser 及单元测试
go.mod          Go 版本和模块路径（当前无第三方依赖）
```

SQL 包不依赖 Kubernetes；后续执行层会通过其 `Parse` 入口获得 AST。
只按当前功能创建包，尚未预建 Kubernetes 后端或通用执行框架。

提交前已通过 gofmt、`go test ./...`、`go vet ./...` 和 staticcheck v0.8.1。
Go 1.27 的 `go fix -diff`、x/tools v0.51.0 的 modernize 无修改建议；同版本 deadcode 包含测试时无报告。
普通 deadcode 会报告尚未接入 CLI 的 SQL 解析函数，当前由单元测试调用，属于保留的 M1 实现，不据此删除。

## 仓库约定

- 正式交付：源码、测试、可复用测试夹具、构建/检查配置和使用文档。
- 临时文件：全部放入已被 Git 忽略的 `tmp/`，包括二进制、日志、覆盖率报告、实验代码和本地 kubeconfig。
- `docs/` 包含本地任务书和规划，目前被 Git 忽略，不属于远端交付内容。
- 不提交真实集群凭据或 Secret；测试只使用明确标注的虚构数据。

## 最小工程验证

需要 Go 1.27。下列命令关闭命令级 Go 实验选项，不修改全局配置；构建缓存和二进制均放在 `tmp/`。

```sh
mkdir -p tmp/go-cache tmp/go-tmp tmp/go-build
export GOEXPERIMENT= GOTOOLCHAIN=local
export GOCACHE="$PWD/tmp/go-cache" GOTMPDIR="$PWD/tmp/go-tmp"
go test ./...
go vet ./...
go build -o tmp/go-build/ksql ./cmd/ksql
```

## 已实现的 SQL 语法（M1）

`internal/sql.Parse` 逐字符扫描并递归下降解析一条 SELECT：

```sql
SELECT name, replicas FROM deployments;
SELECT * FROM ingresses
```

关键字忽略大小写，未引用的表名/列名归一化为小写；分号可省略，后面必须是 EOF。
AST 保留独立星号节点，不展开字段、不检查表名或列名是否存在。
错误为 `E_PARSE`，包含从 1 开始的行列位置。任务书 1-1 的 JSON AST 和 1-2 的第 1 行第 14 列错误均由单元测试验证。
当前不支持 WHERE、引号标识符或字符串；解析 API 尚未接入 CLI，不增加 `--parse-only` 参数。

## 本地真实测试环境

已验证组合：Docker 29.8.2、minikube 1.39.0（Docker driver）、Kubernetes/kubectl 1.35.0。
独立 profile/context 为 `kubesql-test`，分配 2 CPU、3072 MiB 内存；节点及全部系统 Pod 已 Ready。
工具下载后校验官方 SHA-256，放在 `tmp/tools/`；minikube 数据及 kubeconfig 也只在 `tmp/`，未修改默认 kubeconfig。

从仓库根目录检查或重新启动已准备的环境：

```sh
export MINIKUBE_HOME="$PWD/tmp/minikube"
export KUBECONFIG="$PWD/tmp/kube/config"
./tmp/tools/minikube start -p kubesql-test --driver=docker --kubernetes-version=v1.35.0 --cpus=2 --memory=3072
./tmp/tools/kubectl --context=kubesql-test get --raw=/readyz
./tmp/tools/kubectl --context=kubesql-test get nodes
./tmp/tools/kubectl --context=kubesql-test get pods -n kube-system
```

本次 Docker Hub 直连超时，使用国内镜像源拉取 CNI 镜像、重标记后加载到测试节点，未改全局 Docker 镜像源：

```sh
docker pull docker.m.daocloud.io/kindest/kindnetd:v20260820-69b56db7
docker tag docker.m.daocloud.io/kindest/kindnetd:v20260820-69b56db7 docker.io/kindest/kindnetd:v20260820-69b56db7
MINIKUBE_HOME="$PWD/tmp/minikube" KUBECONFIG="$PWD/tmp/kube/config" ./tmp/tools/minikube image load -p kubesql-test docker.io/kindest/kindnetd:v20260820-69b56db7
```

镜像源属于第三方，本次拉取成功不保证后续可用，也未与上游镜像独立比对。
环境冒烟 Deployment 使用 0 副本：业务镜像拉取、业务 Pod 网络、Ingress 流量、Metrics Server 均尚未验证。
测试集群保留供后续开发；本次创建的 `sql-m0-smoke` 命名空间及其资源已清理。

后续预期 SQL 使用形式（尚不可运行）：

```sh
ksql --context kubesql-test --namespace default --output json < query.sql
```
