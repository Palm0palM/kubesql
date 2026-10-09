# KubeSQL

把 SQL 作为操作 Kubernetes 的前端语言，命令行程序名为 `ksql`。

项目依据本地任务书 `docs/KubeSQL.pdf`，优先完成基础查询与过滤的可验收闭环，
再按剩余时间和审查确认扩展写操作、动态资源、Metrics API 和 CRD。
它不是关系型数据库，不提供事务、JOIN 或 ACID 保证。

## 当前状态

M0–M3 已完成并通过代码审查；M4 UPDATE/DELETE 已实现，等待代码审查。
CLI 通过官方 client-go dynamic client 读取 Kubernetes，支持连接参数、命名空间选择和 JSON 输出。
任务书第 2–4 章已通过真实集群 CLI E2E，夹具资源已清理。INSERT 尚未实现。

本地设计与计划保存在被忽略的 `docs/` 中，不提供远端不可用的文档链接。

## 代码结构

```text
cmd/ksql/       进程入口、信号取消及入口行为测试
internal/cli/   flags、stdin、错误/结果输出、依赖装配
internal/sql/   token、lexer、Statement/表达式 AST、递归下降 parser
internal/engine/ 三表绑定、三值逻辑、过滤/投影、精确 patch 与写入汇总
internal/kube/  kubeconfig、dynamic client、分页 List/Patch/Delete
test/e2e/       显式启用的真实集群 CLI 测试
testdata/       可复用 YAML、SQL 和预期 JSON
go.mod/go.sum   Go 版本与固定依赖（client-go/apimachinery v0.35.9）
```

SQL 包不依赖 Kubernetes。执行链为 `cli.Run → sql.Parse → engine.Bind → kube.Connect → Query.Execute → Client.List → WHERE 求值 → 投影 → JSON`。
读操作使用 `Lister`，写操作使用包含 List/Patch/Delete 的 `Writer`；没有提前搭建通用 CRUD 框架。
`Statement.Type` 区分 select/update/delete，SELECT 使用 Columns，UPDATE 使用 Assignments；三者共用 Table 和 WHERE。

提交前已通过 gofmt、`go test ./...`、`go vet ./...` 和 staticcheck v0.8.1。
Go 更新至 1.27.2 后，staticcheck v0.8.1 原依赖无法读取新版导出格式；使用 x/tools v0.51.0 在本地临时模块中重建工具后检查通过，项目依赖未变。
已审查并采用 Go 1.27 `go fix` 的 errors.AsType 建议，复查无剩余建议；x/tools v0.51.0 的 modernize、deadcode 无报告。

## 仓库约定

- 正式交付：源码、测试、可复用测试夹具、构建/检查配置和使用文档。
- 临时文件：全部放入已被 Git 忽略的 `tmp/`，包括二进制、日志、覆盖率报告、实验代码和本地 kubeconfig。
- `docs/` 包含本地任务书和规划，目前被 Git 忽略，不属于远端交付内容。
- 不提交真实集群凭据或 Secret；测试只使用明确标注的虚构数据。

## 构建与离线测试

需要 Go 1.27。下列命令关闭命令级 Go 实验选项，不修改全局配置；构建缓存和二进制均放在 `tmp/`。

```sh
mkdir -p tmp/go-cache tmp/go-tmp tmp/go-build tmp/go-mod-cache
# 防止 Go 的 ./... 将 tmp 中缓存的源码当成项目包。
test -f tmp/go.mod || printf 'module kubesql.local/tmp\n\ngo 1.27.0\n' > tmp/go.mod
export GOEXPERIMENT= GOTOOLCHAIN=local
export GOCACHE="$PWD/tmp/go-cache" GOTMPDIR="$PWD/tmp/go-tmp"
export GOMODCACHE="$PWD/tmp/go-mod-cache" TMPDIR="$PWD/tmp/go-tmp"
go test ./...
go vet ./...
go build -o tmp/go-build/ksql ./cmd/ksql
```

## 查询语法与连接参数

`internal/sql.Parse` 逐字符扫描并递归下降解析一条 SELECT：

```sql
SELECT name, replicas FROM deployments;
SELECT * FROM ingresses
```

关键字忽略大小写，未引用的表名/列名归一化为小写；分号可省略，后面必须是 EOF。
AST 保留独立星号节点，不展开字段、不检查表名或列名是否存在。
错误为 `E_PARSE`，包含从 1 开始的行列位置。任务书 1-1 的 JSON AST 和 1-2 的第 1 行第 14 列错误均由单元测试验证。
执行前会检查未知表、未知列和重复列，错误先于 kubeconfig 加载和 API 请求；空查询结果为 `[]`。
当前不支持双引号标识符、JOIN 或 INSERT，也不增加 `--parse-only` 参数。

| 表 | SELECT * 公开列 |
| --- | --- |
| namespaces | name、labels、annotations |
| deployments | name、namespace、replicas、labels、annotations |
| ingresses | name、namespace、default_backend_service、labels、annotations |

Ingress 无 Service 类型默认后端时输出 JSON null，不使用 rules 后端填充；数字保留数字类型。
只支持这些复数表名，不支持 deploy/ns 等缩写。

```sh
./tmp/go-build/ksql --kubeconfig "$PWD/tmp/kube/config" --context kubesql-test \
  --namespace default --output json < testdata/easy-select/deployments.sql
```

- 不传 `--kubeconfig` 时使用 client-go 默认加载规则（包括 KUBECONFIG）；不传 context 时使用当前 context。
- namespace 优先级：`--namespace` > 所选 context 的 namespace > default。
- `--all-namespaces` 对 Deployment/Ingress 查询全部命名空间；Namespace 表始终查询集群范围。
- stdout 只输出查询 JSON 数组；错误 JSON 输出到 stderr。退出码：0 成功、1 配置/API/I/O 错误、2 参数/语法/语义错误。
- 查询总超时 30 秒，Ctrl+C/SIGTERM 取消 API 请求；不等待 Pod Ready。

### WHERE 过滤（M3）

```sql
SELECT name FROM deployments
WHERE name = 'web' OR name = 'worker' AND replicas >= 3;

SELECT name FROM ingresses WHERE default_backend_service IS NULL;
```

- 比较支持 `=`、`<>`、`>`、`>=`、`<`、`<=`；逻辑支持 AND、OR、NOT 和括号。
- 优先级：比较/IS NULL > NOT > AND > OR。括号可改变结合顺序。
- 值支持字符串、整数、小数、TRUE、FALSE、NULL 及列引用。单引号用 `''` 转义，反斜杠不作 SQL 转义。
- 数字支持相邻正负号和十进制小数；不支持算术或科学计数法。数字比较用 `big.Rat` 保留精度，不转换成字符串或统一 float64。
- WHERE 可以引用未投影的列；未知列及已知不兼容类型在 API 请求前报 E_UNKNOWN_COLUMN/E_TYPE，即便结果为空也不会忽略。
- 不隐式转换 `'3'` 和数字 3。WHERE 与逻辑运算需要布尔值；字段缺失是 NULL，不是未知列。
- NULL 比较得 UNKNOWN，NOT UNKNOWN 仍是 UNKNOWN；AND/OR 遵循三值真值表，WHERE 只保留 TRUE。`IS NULL`/`IS NOT NULL` 返回确定的布尔值。
- `field = NULL` 不能判断空值；应使用 `field IS NULL`。
- 先分页读取候选资源，在客户端求值后投影，不把完整 WHERE 强行转换为 fieldSelector。
- 静态检查遍历整个表达式；运行时也检查两侧操作数，不用短路掩盖类型错误。错误不会输出资源字段值或部分结果。

### 条件 UPDATE/DELETE（M4）

```sql
UPDATE deployments SET replicas = 2 WHERE name = 'web';
UPDATE namespaces SET annotations = '{"owner":"alice"}' WHERE name = 'sql-medium-write';
UPDATE ingresses SET default_backend_service = 'web-v2' WHERE name = 'temporary';
DELETE FROM deployments WHERE name = 'web';
```

- UPDATE 支持逗号分隔多个 SET，值限字面量；WHERE 与 SELECT 完全复用解析、列绑定及三值逻辑。
- UPDATE/DELETE 缺 WHERE 返回 `E_WHERE_REQUIRED`。写入拒绝 `--all-namespaces`，namespaced 资源只操作解析出的一个命名空间；Namespace 写入不受 namespace 过滤。
- 三表均可替换 labels/annotations，输入为含 JSON 对象的 SQL 字符串且所有值必须为字符串。空对象清空映射；本阶段不接受 SQL NULL 赋值。
- Deployment 额外可写 replicas：0–2147483647 的整数。Ingress 额外可写 default_backend_service：合法非空 Service 名称，必须已有 Service 类型默认后端，仅改名称并保留端口。
- 禁止写 name、namespace、status、服务器管理字段及所有未公开的可写列；重复 SET 拒绝。赋值类型和 WHERE 检查在 API 写入前完成。
- 先读取并筛选完整候选集合，再逐对象写入；WHERE 求值错误时没有对象被写入。0 匹配成功输出 `{"affected_rows":0}`。
- UPDATE 使用 JSON Patch，先 test resourceVersion，再精确 add/replace 指定路径；映射整体替换，不使用 Merge Patch，不覆盖简化后的完整资源。
- 对象状态错误或 API 失败计入逐对象汇总，继续其余匹配对象；不重试冲突、不回滚。成功输出 `{"affected_rows":N}`；部分失败输出 `{"affected_rows":S,"failed_rows":F,"errors":[{"resource":"...","namespace":"...","name":"...","reason":"..."}]}` 并退出 1。
- API 对象失败仅报告安全的 StatusReason（如 Forbidden/Conflict/Invalid）或本地错误码，不回显完整资源和字段值。整语句错误仍在 stderr。
- DELETE 带读取对象的 UID/resourceVersion 前置条件，普通删除；API 接受即计成功，不等待消失、不清理 finalizer。
- labels/annotations 查询返回 API 的 JSON 对象（缺失为 null），SELECT * 随公开列扩展。Controller/API Server 可能随后补回自己的键，如 Deployment revision 注解、Namespace 名称标签；CLI 不隐藏这些真实字段。

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

## 真实集群 E2E

先按上文构建二进制并准备独立测试集群，再执行：

```sh
KSQL_E2E_KUBECONFIG="$PWD/tmp/kube/config" \
KSQL_E2E_CONTEXT=kubesql-test \
KSQL_E2E_BINARY="$PWD/tmp/go-build/ksql" \
go test -tags=e2e -count=1 -v ./test/e2e
```

默认 `go test ./...` 不运行 E2E。显式启用时若缺少上述配置会失败，不把未运行当作通过。
E2E 仅接受明确的 `kubesql-test` context，拒绝覆盖测试所用的既有命名空间（sql-easy-select、sql-easy-where、sql-medium-write、sql-medium-delete）。
测试通过 Go dynamic client 创建任务书夹具，调用真实 ksql 二进制，按行集合比较结果，最后删除并等待本套件命名空间消失。
不需要手工提前 apply 夹具；部署副本数按任务书保留，但查询/过滤验收不要求镜像拉取或 Pod Ready。
第 3 章覆盖 AND/OR 优先级、括号、IS NULL、= NULL，并补充 NOT UNKNOWN 的真实 CLI 用例。
第 4 章分别独立准备更新/删除环境，确认镜像、selector、template、Ingress 端口等未赋值字段保持不变；另验证映射替换、版本前置检查和 finalizer 普通删除语义。
Deployment 注解断言仅在测试中排除 controller 自动生成的 revision 键，CLI 输出仍完整；Namespace 标签断言包含 API 恢复的标准名称标签，不把服务端补键误判为 Merge Patch 残留旧用户键。
