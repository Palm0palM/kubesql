# KubeSQL

把 SQL 作为操作 Kubernetes 的前端语言，命令行程序名为 `ksql`。

项目依据本地任务书 `docs/KubeSQL.pdf`，优先完成基础查询与过滤的可验收闭环，
再按剩余时间和审查确认扩展写操作、动态资源、Metrics API 和 CRD。
它不是关系型数据库，不提供事务、JOIN 或 ACID 保证。

## 当前状态

M0–M7 已完成并提交；M8 CRD/CR、Review 与能力边界已实现并验证。
CLI 通过官方 client-go dynamic client 查询和操作 Kubernetes，支持连接参数、命名空间选择和 JSON 输出。
任务书第 2–8 章已通过真实集群 CLI E2E，夹具资源已清理；第 1 章通过离线语法测试。

本地设计与计划保存在被忽略的 `docs/` 中，不提供远端不可用的文档链接。

## 代码结构

```text
cmd/ksql/       进程入口、信号取消及入口行为测试
internal/cli/   flags、stdin、错误/结果输出、依赖装配
internal/sql/   token、lexer、Statement/表达式 AST、递归下降 parser
internal/engine/ 三表绑定、三值逻辑、过滤/投影、精确 patch 与写入汇总
internal/resource/ 共享 Discovery 描述（GVR/Kind/scope/verbs）与发现错误
internal/kube/  kubeconfig、Discovery 快照、dynamic CRUD、Metrics Quantity 汇总
test/e2e/       显式启用的真实集群 CLI 测试
testdata/       可复用 YAML、SQL 和预期 JSON
go.mod/go.sum   Go 版本与固定依赖（client-go/apimachinery v0.35.9）
```

SQL 包不依赖 Kubernetes。执行链为 `cli.Run → sql.Parse → engine.Preflight → kube.Connect → Client.Resolve → engine.Bind → Query.Execute → Client.List → WHERE 求值 → 投影 → JSON`。
读操作使用 `Lister`，条件写操作使用 `Writer`，创建使用只含 Create 的 `Creator`；没有提前搭建通用 CRUD 框架。
`Statement.Type` 区分 select/update/delete/insert；SELECT/INSERT 使用 Columns，UPDATE 使用 Assignments，INSERT 的 Values 表示一个元组。WHERE 仅用于查询和条件写入。
创建同样先 Preflight/Connect/Resolve，再 `BindInsert → Insert.Execute → Client.Create → WriteResult → JSON`。Connect 的 context 也约束无 context 参数的官方 Discovery 方法。

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

`internal/sql.Parse` 逐字符扫描并递归下降解析一条 SELECT/UPDATE/DELETE/INSERT，例如：

```sql
SELECT name, replicas FROM deployments;
SELECT * FROM ingresses
```

关键字忽略大小写，未引用的表名/列名归一化为小写；分号可省略，后面必须是 EOF。
AST 保留独立星号节点，不展开字段、不检查表名或列名是否存在。
错误为 `E_PARSE`，包含从 1 开始的行列位置。任务书 1-1 的 JSON AST 和 1-2 的第 1 行第 14 列错误均由单元测试验证。
未知列、重复输出键、已知类型及写保护等错误先于 Discovery；未知表必须由实际 Discovery 判断，因此可产生 Discovery 请求，但不发送资源 List/写入请求。空查询结果为 `[]`。
当前不支持 JOIN、批量 INSERT、generateName、子资源操作或流式接口，也不增加 `--parse-only` 参数。CRD/CR 与 create-only Review 走同一 Discovery/dynamic 路由；聚合 API 部分失败另有 HTTP 验证。

| 表 | SELECT * 公开列 |
| --- | --- |
| namespaces | name、namespace（NULL）、labels、annotations、manifest |
| deployments | name、namespace、replicas、labels、annotations、manifest |
| ingresses | name、namespace、default_backend_service、labels、annotations、manifest |

Ingress 无 Service 类型默认后端时输出 JSON null，不使用 rules 后端填充；数字保留数字类型。
所有发现到的普通资源有 name、namespace、labels、annotations、manifest；原三表保留便捷列。不支持 deploy/ns 等缩写。

```sh
./tmp/go-build/ksql --kubeconfig "$PWD/tmp/kube/config" --context kubesql-test \
  --namespace default --output json < testdata/easy-select/deployments.sql
```

- 不传 `--kubeconfig` 时使用 client-go 默认加载规则（包括 KUBECONFIG）；不传 context 时使用当前 context。
- namespace 优先级：`--namespace` > 所选 context 的 namespace > default。
- `--all-namespaces` 对 Deployment/Ingress 查询全部命名空间；Namespace 表始终查询集群范围。
- stdout 输出查询 JSON 数组或写入汇总；整语句错误 JSON 输出到 stderr，对象 API 失败包含在 stdout 汇总内。退出码：0 成功、1 配置/API/I/O 错误、2 参数/语法/语义错误。
- 单条语句总超时 30 秒，Ctrl+C/SIGTERM 取消 API 请求；不等待 Pod Ready。

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
- 所有表均可替换 labels/annotations，输入为含 JSON 对象的 SQL 字符串且所有值必须为字符串。空对象清空映射；M6 起这些可选映射接受 SQL NULL，表示移除字段。
- Deployment 额外可写 replicas：0–2147483647 的整数。Ingress 额外可写 default_backend_service：合法非空 Service 名称，必须已有 Service 类型默认后端，仅改名称并保留端口。
- 禁止写 name、namespace、status、服务器管理字段及所有未公开的可写列；重复 SET 拒绝。赋值类型和 WHERE 检查在 API 写入前完成。
- 先读取并筛选完整候选集合，再逐对象写入；WHERE 求值错误时没有对象被写入。0 匹配成功输出 `{"affected_rows":0}`。
- UPDATE 使用 JSON Patch，先 test resourceVersion，再精确 add/replace 指定路径；映射整体替换，不使用 Merge Patch，不覆盖简化后的完整资源。
- 对象状态错误或 API 失败计入逐对象汇总，继续其余匹配对象；不重试冲突、不回滚。成功输出 `{"affected_rows":N}`；部分失败输出 `{"affected_rows":S,"failed_rows":F,"errors":[{"resource":"...","namespace":"...","name":"...","reason":"..."}]}` 并退出 1。
- API 对象失败仅报告安全的 StatusReason（如 Forbidden/Conflict/Invalid）或本地错误码，不回显完整资源和字段值。整语句错误仍在 stderr。
- DELETE 带读取对象的 UID/resourceVersion 前置条件，普通删除；API 接受即计成功，不等待消失、不清理 finalizer。
- labels/annotations 查询返回 API 的 JSON 对象（缺失为 null），SELECT * 随公开列扩展。Controller/API Server 可能随后补回自己的键，如 Deployment revision 注解、Namespace 名称标签；CLI 不隐藏这些真实字段。

### INSERT 与 manifest（M5）

```sql
INSERT INTO namespaces (manifest)
VALUES ('{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"example"}}');
SELECT manifest FROM namespaces WHERE name = 'example';
```

- 支持单个 VALUES 元组，仅允许 manifest 列和一个 SQL 字符串值。SQL lexer 先解码 `''`，独立 JSON 解码器再处理 JSON 转义；不支持批量创建。
- manifest 必须是一个完整 JSON 对象；apiVersion/kind 必须匹配 Discovery 实际选中的 GVR/Kind。持久化资源的 metadata.name 必须为非空字符串；M8 起 create-only 请求对象不强制 name，见下文。不支持 generateName。
- 拒绝顶层 status 及 metadata 中 uid、resourceVersion、managedFields、generation、creationTimestamp、deletionTimestamp、deletionGracePeriodSeconds、selfLink，字段即便为 null 也拒绝。
- Deployment/Ingress 未指定 namespace（或为空）时填入 CLI 解析出的 namespace；非空且不一致时报 E_NAMESPACE，不发送 API 请求。Namespace 不得带非空 namespace；所有 INSERT 拒绝 --all-namespaces。
- 本地 JSON、列、服务器字段检查先于 Discovery，GVK/scope/verbs 在获取描述后、Create 前检查；其余资源 schema/名称合法性由 API Server 校验。INSERT 只发送一次 Create，不提前 GET，不自动 UPDATE、删除重建或重试。
- 成功输出 `{"affected_rows":1}`；API 失败沿用 M4 汇总，含对象身份及 AlreadyExists/Invalid/Forbidden 等安全 reason，退出 1；本地语义错误输出 stderr 并退出 2。
- manifest 查询返回完整 API 资源对象，包括服务端元数据、spec、status，而不是 JSON 字符串；SELECT * 包含 manifest。它是只读查询列，禁止 UPDATE manifest；WHERE 仅允许 IS NULL/IS NOT NULL，不支持对象比较。
- SQL 接受的创建 manifest 不应直接使用查询返回的完整对象：先移除服务器管理字段；创建无事务保证，超时或取消后应查询确认服务端是否已创建。

### Discovery、Pointer 与通用 CRUD（M6）

```sql
SELECT name, "/spec/replicas" AS replicas FROM "apps/v1/statefulsets";
SELECT name, "/data/username" AS username FROM secrets;
UPDATE configmaps SET "/data/mode" = 'prod' WHERE name = 'settings';
UPDATE configmaps SET "/data" = CAST('{"mode":"prod","region":"test"}' AS JSON)
WHERE name = 'settings';
UPDATE configmaps SET "/data/region" = NULL WHERE name = 'settings';
```

- 每条语句独立 Discovery，不做磁盘/跨进程缓存。普通复数表名按实际 APIResource.Name 查找，不猜复数、不使用 shortNames；先按 group 判断歧义，再选择提供该资源的 preferred version，缺资源时按该组 Discovery 版本顺序回退。
- 精确表名必须双引号包裹：核心组 `"v1/configmaps"`，其他组 `"apps/v1/statefulsets"`；只发现并使用该版本，绝不回退。子资源（status/scale/exec 等）不作为普通表。
- 部分 group/version 发现失败时，简单名返回 E_DISCOVERY，不能假定没有组间歧义；精确健康版本仍可用。跨组同名返回 E_AMBIGUOUS_TABLE；实际资源缺失返回 E_UNKNOWN_TABLE。
- SELECT 需 list，UPDATE 需 list+patch，DELETE 需 list+delete，INSERT 需 create；缺能力返回 E_UNSUPPORTED_VERB，检查后才调用资源 API。verbs 是 API 能力，不等于用户 RBAC 权限；Forbidden 仍由 API 返回。
- Discovery scope 决定路由。集群级对象 namespace 输出 NULL，并忽略 CLI namespace；所有 namespaced 写入仍限一个 namespace，拒绝 --all-namespaces。
- 双引号标识符保留大小写，用 `""` 转义双引号；Pointer 从 `/` 开始，严格解码 `~0`、`~1`。空 Pointer 可读取根对象，但不可更新。AS 只改变输出键；禁止重复输出键，不允许在 WHERE 用投影别名代替源列。
- Pointer 读取缺失路径输出 NULL；对象/数组保持 JSON 类型，数组索引为非负十进制整数，不接受前导零和 `-`。动态字段的 WHERE 类型在运行时检查，只比较标量；整个候选集合求值完成后才开始写入。
- 写入只在已有父对象下添加叶子，不自动创建中间节点。数组只支持已有索引的替换，不支持插入/删除；重复路径及祖先/后代重叠赋值在 Discovery 前拒绝。
- Pointer 普通字符串始终是字符串，不猜 JSON。受限 CAST(string AS JSON) 仅用于 SET，允许对象、数组、布尔、数字、字符串或 JSON null；不是通用 SQL 函数系统。
- SQL NULL 删除可选映射字段，叶子已缺失时不重复 remove；CAST('null' AS JSON) 发送 value:null，是否保存由服务端 schema 决定。非空对象/数组整体 replace，不能残留旧键。
- 按解码后的 Pointer 段检查写保护：根、apiVersion、kind、status、metadata.name/namespace 和服务器管理字段及其祖先/后代均禁止更新；不能整体替换 /metadata 绕过。labels/annotations 仍允许整体替换。
- SQL/JSON 整数以 int64 写入，超过 int64 范围拒绝；大于 2^53 的整数（含数学上为整数的 JSON 小数/指数写法）保持精确。非整数 JSON 数字遵循 Kubernetes 的 float64 表示，不承诺任意小数写入精度。
- Secret data 保持 API 的 Base64，不自动解码；整语句 API 错误和逐对象失败都不回显原资源、manifest、patch 或 API 详细字段值。
- 资源不可变字段、键格式和其他 schema 约束交给 API Server 拒绝，不自动重建。程序不会等待 PVC 绑定或业务 Pod Ready。

可复现第 5 章示例：先执行 Namespace 创建 SQL，等待其 Active，再执行 Deployment SQL；目标 YAML **只用于检查，不要 apply**。

```sh
./tmp/go-build/ksql --kubeconfig "$PWD/tmp/kube/config" --context kubesql-test \
  < testdata/medium-insert/namespace.sql
./tmp/tools/kubectl --kubeconfig "$PWD/tmp/kube/config" --context kubesql-test \
  wait --for=jsonpath='{.status.phase}'=Active namespace/sql-medium-insert --timeout=120s
./tmp/go-build/ksql --kubeconfig "$PWD/tmp/kube/config" --context kubesql-test \
  --namespace sql-medium-insert < testdata/medium-insert/deployment.sql
```

上述手工示例会保留资源；运行自动 E2E 前应清理自己创建的示例命名空间，测试拒绝覆盖已有资源。

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
M7 已验证 Metrics Server 可用、busybox 双容器 Pod Ready 与实际指标；其他业务镜像、业务网络和 Ingress 流量不据此声称已验证。
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
E2E 仅接受明确的 `kubesql-test` context，拒绝覆盖测试所用的既有命名空间（sql-easy-select、sql-easy-where、sql-medium-write、sql-medium-delete、sql-medium-insert、sql-medium-insert-ingress、sql-hard-native、sql-hard-metrics、sql-hard-crd、sql-hard-cluster-crd）。CRD 套件另外拒绝已有 gadgets.lab.example.com/clusternotes.lab.example.com 定义，清理只针对本套件创建并登记 UID 的 CRD。
测试通过 Go dynamic client 创建任务书夹具，调用真实 ksql 二进制，按行集合比较结果，最后删除并等待本套件命名空间消失。
不需要手工提前 apply 夹具；部署副本数按任务书保留，但查询/过滤验收不要求镜像拉取或 Pod Ready。
第 3 章覆盖 AND/OR 优先级、括号、IS NULL、= NULL，并补充 NOT UNKNOWN 的真实 CLI 用例。
第 4 章分别独立准备更新/删除环境，确认镜像、selector、template、Ingress 端口等未赋值字段保持不变；另验证映射替换、版本前置检查和 finalizer 普通删除语义。
Deployment 注解断言仅在测试中排除 controller 自动生成的 revision 键，CLI 输出仍完整；Namespace 标签断言包含 API 恢复的标准名称标签，不把服务端补键误判为 Merge Patch 残留旧用户键。
第 5 章严格区分 preparation.yaml 与 targets.yaml，仅创建 preparation；5-1 的 preparation 为空，Namespace 由 SQL 创建并等待 Active。targets 只比较明确字段（包括 selector/template/镜像/端口），不要求服务端默认值完全一致。
5-2 重复创建检查 AlreadyExists、退出码 1、汇总身份、唯一对象及原资源完整内容不变。额外验证 schema 拒绝、manifest 读取、缺省 namespace 与 SQL/JSON 两层转义。
第 6 章查询 ReplicaSet/StatefulSet/Secret/PVC/ConfigMap，并更新 ConfigMap；另验证 CAST 整对象替换、叶子添加/移除、数组读取、限定 annotation 键 Pointer 转义、通用 ConfigMap 创建/删除和 Node/Namespace 集群 scope。
Namespace 自动出现的 kube-root-ca.crt ConfigMap 不被 CLI 隐藏；测试仅在第 6 章夹具对象比较中排除这条明确系统记录。`~0` 和 JSON null 保存的 patch 结构由离线测试验证；是否允许某路径保存 null 由具体 API schema 决定。
接入官方 Discovery 新增了其所需的间接依赖（包括 k8s.io/api v0.35.9 和 OpenAPI 库）；client-go/apimachinery 固定版本未变，未引入新的 SQL/CRUD 框架。

### 实际指标（M7）

```sql
SELECT name, cpu_millicores, memory_bytes FROM pod_metrics WHERE name = 'measure';
SELECT name, cpu_millicores, memory_bytes FROM node_metrics WHERE name = 'kubesql-test';
```

| 只读虚拟表 | 公开列（SELECT *） |
| --- | --- |
| pod_metrics | name、namespace、cpu_millicores、memory_bytes |
| node_metrics | name、cpu_millicores、memory_bytes |

- 从真实 `metrics.k8s.io/v1beta1` API 读取 PodMetrics/NodeMetrics，复用官方 dynamic client；不是 Pod 的 requests/limits，也不读 kubectl 输出。
- 每 Pod 一行，先用 Quantity 累加全部容器的 CPU/内存，再转换整数 millicores/bytes；例如两容器各 400u CPU，合计输出 1m 而不是分别舍入成 2m。Node 使用节点实际 usage。
- 固定只读 schema，不暴露 manifest/JSON Pointer，不走普通资源 Discovery；UPDATE/DELETE/INSERT、未知列和已知 WHERE 类型错误在连接前拒绝。WHERE、AS、投影和三值逻辑复用原查询引擎。
- Pod namespace 与原查询规则一致，支持 --all-namespaces；Node 是集群级查询，不受 --namespace 限制，没有 namespace 列。
- Metrics Server 未安装/不可用返回 E_METRICS_UNAVAILABLE、退出 1；403 保留 Forbidden，错误不回显 API 字段值。空样本返回 `[]`，不伪造零值；畸形/缺失/负数/越界样本返回 E_METRICS_DATA。
- 固定样本已验证 150m/83886080 bytes 和 250m/536870912 bytes；真实用量会变化，只验证身份、一行及非负整数。
- E2E 为双容器 Pod Ready 和 Metrics 样本共设置最长 180 秒等待；读取 Node 也确认一行非负整数。测试清理业务 Namespace，保留测试集群 Metrics Server 供后续使用。

在独立测试 profile 启用 Metrics Server：

```sh
MINIKUBE_HOME="$PWD/tmp/minikube" KUBECONFIG="$PWD/tmp/kube/config" \
  ./tmp/tools/minikube addons enable metrics-server -p kubesql-test
./tmp/tools/kubectl --kubeconfig "$PWD/tmp/kube/config" --context kubesql-test \
  rollout status deployment/metrics-server -n kube-system --timeout=180s
```

本次 addon 为 Metrics Server v0.9.0，官方镜像拉取超时，从 `k8s.m.daocloud.io/metrics-server/metrics-server:v0.9.0` 拉取（digest 与 addon 指定值一致），重标记并加载到测试 profile。为使用已加载镜像，仅把本次 addon 的镜像引用从带 digest 改为同版本本地标签；未修改全局镜像源。busybox:1.36 同样经镜像源下载并加载。镜像源为第三方，不保证后续可用。

### CRD、CR 与请求对象（M8）

```sql
SELECT name, "/spec/size" AS size FROM "lab.example.com/v1/gadgets"
WHERE "/spec/size" >= 2;
UPDATE "lab.example.com/v1/gadgets" SET "/spec/size" = 3 WHERE name = 'sample';
SELECT name, namespace, "/spec/owner" AS owner FROM "lab.example.com/v1/clusternotes";

INSERT INTO "authorization.k8s.io/v1/selfsubjectaccessreviews" (manifest)
VALUES ('{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","spec":{"resourceAttributes":{"verb":"list","resource":"pods"}}}');
```

- CRD 本身、陌生 CR 和普通内置资源使用同一 Discovery/dynamic CRUD，不增加专用 Go 类型、不猜 plural，也不假定 spec.replicas 存在。
- CRD 与实例必须分步创建：等待 CRD Established，再确认目标版本 Discovery 可见，才能创建实例。CLI 不自动编排注册/等待；一条 INSERT 只执行一次 Create。无常驻缓存，每个新 CLI 调用重新发现资源。
- 测试 8-1 使用 plural=gadgets、kind=Widget，验证过滤、精确更新、未指定 message 保留、INSERT extra 与 DELETE；目标 extra YAML 不提前创建。
- 测试 8-2 验证 ClusterNote 和 CRD 查询不受无关 namespace 影响，namespace 返回 NULL；另通过 SQL 完成 CRD 本身创建、annotation/versions 更新和删除，以及集群级 CR 创建/更新/删除。
- 同组多 served version 仍按 preferred/发现顺序选择，显式版本绝不回退。实测在 CRD 上增加 v1beta1 served version（仍只有一个 storage version），简单名返回 v1，精确 v1beta1 返回该版本并可创建实例。
- schema/不可变字段/RBAC/版本冲突由 API Server 拒绝，保留 Invalid、Forbidden、Conflict 等安全 reason、对象身份及非零退出码，不归类为 SQL 语法错误、不回显字段值。真实测试拒绝 size=0、修改 CRD scope、无权限账号创建和过期 resourceVersion patch；HTTP 模拟逐对象失败后继续。
- 对 Discovery 宣告 create、但没有 get/list 的请求型 API，不强制 metadata/name，不凭 Kind 名字硬编码 Review 清单。其他可 get/list 的持久化资源仍要求 name；未知资源在预检中暂缓这项判断，实际 Discovery 后、Create 前校验。
- Review 请求仍检查 JSON、GVK、scope、服务器管理字段及所需 verb。SELECT/UPDATE/DELETE 缺 verb 时返回 E_UNSUPPORTED_VERB；INSERT 返回已有写入汇总（成功是 affected_rows=1），不会把 Review 响应转换为持久化查询结果或返回 allowed 决策。
- 聚合 API 部分失败使用原 M6 规则：健康精确版本可用，失败组不能假装不存在，简单名不猜歧义；HTTP 验证此边界。没有在真实集群安装额外聚合 API 服务。
- CRD 测试仅清理自己的定义，UID 前置条件防止误删同名重建对象；不清理已有用户 CRD。RBAC 实测使用本套件 Namespace 中的 ServiceAccount，通过临时 kubeconfig impersonation，不创建全局权限绑定。

第 8 章夹具分别保存在 `testdata/hard-crd/` 与 `testdata/hard-cluster-crd/`，自动 E2E 会按上述顺序准备、验收和清理。默认离线测试不会接触集群；全套 E2E 同样使用前述显式 context/二进制环境变量。
