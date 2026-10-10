# KubeSQL

用 SQL 查询和操作 Kubernetes 的命令行工具，程序名为 `ksql`。

```sql
SELECT name, namespace, replicas FROM deployments WHERE replicas >= 3;
SELECT name, "/spec/replicas" AS replicas FROM "apps/v1/statefulsets";
SELECT name, cpu_millicores, memory_bytes FROM pod_metrics;
```

支持内置资源、CRD/自定义资源和 Metrics API。通过官方 client-go 直接访问集群，不依赖 kubectl 子进程；不提供数据库、事务或 JOIN。

> 写语句会直接修改集群。先确认 context、namespace 和查询范围，优先使用最小权限 kubeconfig；不要直接在生产环境试运行示例。

## 安装

需要 **Go 1.27 或更新版本**，以及能够访问 Kubernetes API 的 kubeconfig。Metrics 查询还需要可用的 Metrics Server。

从源码安装：

```sh
git clone https://github.com/Palm0palM/kubesql.git
cd kubesql
go install ./cmd/ksql
ksql --help
```

将 `$(go env GOPATH)/bin`（或设置的 `GOBIN`）加入 PATH。不想安装到 PATH，可以只构建本地二进制：

```sh
mkdir -p tmp/bin
go build -o tmp/bin/ksql ./cmd/ksql
./tmp/bin/ksql --help
```

已在 Linux/WSL、Go 1.27.2、Kubernetes 1.35.0 上验证，client-go 固定为 v0.35.9。其他平台和服务端版本需自行验证兼容性。

## 快速开始

每次从 **stdin 读取一条 SQL**，分号可省略。以下 `my-context` 请替换成自己的 context：

```sh
ksql --context my-context --namespace default --output json <<'SQL'
SELECT name, namespace, replicas FROM deployments WHERE replicas >= 3;
SQL
```

也可以读取文件：

```sh
ksql --kubeconfig "$HOME/.kube/config" --context my-context < query.sql
```

查询始终输出 JSON 数组；没有匹配对象时为 `[]`。示例结果：

```json
[{"name":"web","namespace":"default","replicas":3}]
```

### 连接参数

| 参数 | 行为 |
| --- | --- |
| `--kubeconfig PATH` | 指定 kubeconfig；不传时使用 client-go 默认规则，包括 KUBECONFIG |
| `--context NAME` | 选择 context；不传时使用 kubeconfig 当前 context |
| `--namespace NAME` | 命名空间；默认依次取所选 context 的 namespace、default |
| `--all-namespaces` | 仅查询时可用，读取全部命名空间 |
| `--output json` | 输出 JSON，也是默认且唯一支持的格式 |
| `--help` | 查看帮助，无需连接集群 |

集群级资源（Namespace、Node、CRD 等）忽略 namespace，公开列 `namespace` 为 JSON null。单条语句的 API 执行上限为 30 秒，Ctrl+C/SIGTERM 取消请求。

## 查询

### 表与列

普通资源以 Discovery 返回的复数名称作为表名，如 `pods`、`configmaps`、`jobs`、`clusterroles` 或 `gadgets`；不支持 `po`、`deploy` 等缩写。

所有普通表均有 `name`、`namespace`、`labels`、`annotations`、`manifest`。其中 manifest 是完整 API 对象，包含服务器元数据、spec 和 status；`SELECT *` 返回所有公开列，不递归展开 manifest。

额外便捷列：

| 表 | 额外列 |
| --- | --- |
| deployments | replicas |
| ingresses | default_backend_service（仅默认 Service 后端名称；不存在时为 null） |

表名存在组间歧义时，用双引号精确指定：

```sql
SELECT name FROM "v1/configmaps";
SELECT name FROM "apps/v1/deployments";
SELECT name FROM "lab.example.com/v1/gadgets";
```

简单名称优先选择该组提供资源的 preferred version，否则按 Discovery 版本顺序回退；精确表名绝不回退。部分 API 组发现失败时，使用健康资源的精确表名，避免无法确认的歧义。

### JSON Pointer 与别名

```sql
SELECT name, "/spec/template/spec/containers/0/image" AS image FROM deployments;
SELECT name, "/data/mode" AS mode FROM configmaps;
SELECT name, "/metadata/annotations/example.com~1owner" AS owner FROM pods;
```

Pointer 必须用双引号包裹；`~1` 表示 `/`，`~0` 表示 `~`。缺失路径为 null，对象/数组原样输出为 JSON；数组索引从 0 开始，不接受前导零或 `-`。AS 只改变输出键，重复输出键会报错，WHERE 使用源列而不是别名。

### WHERE

```sql
SELECT name FROM deployments
WHERE name = 'web' OR (replicas >= 3 AND NOT name = 'worker');
SELECT name FROM ingresses WHERE default_backend_service IS NULL;
```

- 比较：`=`、`<>`、`>`、`>=`、`<`、`<=`；逻辑：AND、OR、NOT、括号、IS NULL / IS NOT NULL。
- 优先级：比较/IS NULL > NOT > AND > OR。WHERE 在客户端对分页读取的候选资源求值。
- 支持字符串、整数、小数、TRUE、FALSE、NULL 和列引用；不隐式转换字符串与数字，对象/数组不能比较。
- NULL 比较产生 UNKNOWN，WHERE 仅保留 TRUE；判断空值请用 IS NULL，而不是 `= NULL`。
- 单引号字符串用 `''` 表示一个单引号，反斜杠不作 SQL 转义；双引号标识符用 `""` 转义。关键字和未引用名称忽略大小写，引用名称保留大小写。

## 创建、更新与删除

### 创建资源

INSERT 只接受一个 manifest 字符串和一个 VALUES 元组。以下完整示例会创建 Namespace 和 ConfigMap：

```sh
ksql --context my-context <<'SQL'
INSERT INTO namespaces (manifest)
VALUES ('{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"ksql-demo"}}');
SQL
# 等待该 Namespace Active 后再创建其中的资源。
ksql --context my-context --namespace ksql-demo <<'SQL'
INSERT INTO configmaps (manifest)
VALUES ('{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings"},"data":{"mode":"dev"}}');
SQL
```

manifest 必须是 JSON 对象，apiVersion/kind 与实际表匹配。持久化资源必须有 metadata.name，不支持 generateName。未写 namespace 时补入 CLI 解析值；非空且不同则拒绝；集群级资源不得指定非空 namespace。拒绝 status、uid、resourceVersion、managedFields 等服务器管理字段，其他约束由 API Server 校验。

同名创建返回 AlreadyExists，不会更新、删除重建或覆盖对象。查询返回的完整 manifest 含服务器字段，不能不经处理直接用于 INSERT。

### 精确更新

以下 SQL 需用 `--namespace ksql-demo` 执行：

```sql
UPDATE configmaps SET "/data/mode" = 'prod' WHERE name = 'settings';
UPDATE configmaps SET "/data" = CAST('{"mode":"prod","region":"test"}' AS JSON)
WHERE name = 'settings';
UPDATE configmaps SET "/data/region" = NULL WHERE name = 'settings';
```

普通字符串不猜测 JSON；对象/数组等 JSON 值使用受限的 `CAST('...' AS JSON)`。SQL NULL 表示移除可选字段；`CAST('null' AS JSON)` 表示写入 JSON null，是否接受由服务器 schema 决定。

也可以更新便捷列：

```sql
UPDATE deployments SET replicas = 2 WHERE name = 'web';
UPDATE namespaces SET annotations = '{"owner":"alice"}' WHERE name = 'ksql-demo';
UPDATE ingresses SET default_backend_service = 'web-v2' WHERE name = 'temporary';
```

支持逗号分隔多个 SET。labels/annotations 整体替换，不合并旧键；replicas 限非负 int32 整数；Ingress 便捷列只改已有 Service 默认后端的名称，保留端口，不自动创建后端。

### 删除资源

```sql
DELETE FROM configmaps WHERE name = 'settings';
DELETE FROM namespaces WHERE name = 'ksql-demo';
```

删除是普通 API 请求：服务器接受即计成功，不等待最终消失，不清除 finalizer。

### 写入安全边界

- UPDATE/DELETE **必须有 WHERE**，但 `WHERE TRUE` 仍会匹配全部对象；执行前先用 SELECT 检查范围。
- 所有写语句拒绝 `--all-namespaces`；namespaced 写入只操作解析出的一个命名空间。
- 先检查完整语句并筛选全部候选，再逐对象写入；失败后继续后续对象，无事务、回滚或自动冲突重试。
- JSON Patch 先检查 resourceVersion，再修改指定路径；DELETE 使用 UID/resourceVersion 前置条件。未指定字段保留；Controller 仍可能补回自己管理的字段。
- 禁止修改根对象、apiVersion、kind、status、name、namespace、服务器管理元数据以及覆盖它们的祖先路径；UPDATE manifest 也禁止。
- 只在已有父对象下添加叶子，不自动创建中间路径；拒绝重叠赋值。数组只允许已有索引替换，不支持插入/删除。
- 整数写入保持 int64 精度，越界拒绝；非整数 JSON 值遵循 Kubernetes float64 表示。SQL 数字不支持科学计数法，CAST 内的 JSON 可以。
- 超时/取消不代表服务器一定未执行：先查询确认状态，特别是创建操作，不要盲目重试。

## 实际 CPU 与内存

```sql
SELECT name, namespace, cpu_millicores, memory_bytes FROM pod_metrics;
SELECT name, cpu_millicores, memory_bytes FROM node_metrics;
```

两张表只读。Pod 每行汇总所有容器的实际 usage，先累加 Quantity 再转换 millicores/bytes；Node 使用节点 usage，不以 requests/limits 代替。pod_metrics 支持 --all-namespaces，node_metrics 不受 namespace 影响且没有 namespace 列。

Metrics Server 未安装或不可用时返回 E_METRICS_UNAVAILABLE，403 保留 Forbidden；无样本为 `[]`，不填充虚假的零。指标随采样变化。

## CRD 与请求型 API

CRD 本身和 CR 走相同的通用 CRUD，无需添加 Go 类型或重新编译。创建 CRD 后，必须先等待 Established 和目标版本 Discovery 可见，再创建 CR；ksql 不自动编排这两个步骤。

Discovery 仅提供 create、没有 get/list 的请求型资源不强制 metadata.name，例如：

```sql
INSERT INTO "authorization.k8s.io/v1/selfsubjectaccessreviews" (manifest)
VALUES ('{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","spec":{"resourceAttributes":{"verb":"list","resource":"pods"}}}');
```

这里的成功汇总表示请求被接受，不输出 Review 的 allowed 决策。不具备所需 verb 时返回 E_UNSUPPORTED_VERB。Discovery verbs 是 API 能力，不代表用户有 RBAC 权限。

## 输出与排错

查询的 stdout 为 JSON 数组，写入的 stdout 为汇总：

```json
{"affected_rows":1}
```

部分失败：

```json
{"affected_rows":1,"failed_rows":1,"errors":[{"resource":"configmaps","namespace":"default","name":"settings","reason":"Conflict"}]}
```

整语句错误输出到 stderr；对象级 API 失败包含在 stdout 汇总中。错误不会回显完整资源、manifest、patch 或 API 详细字段值。

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功，包括空查询或零匹配写入 |
| 1 | 配置、API、I/O 错误或至少一个对象失败 |
| 2 | 参数、SQL 语法、列、类型或安全校验错误 |

常见错误：E_PARSE（含行列位置）、E_WHERE_REQUIRED、E_NAMESPACE、E_AMBIGUOUS_TABLE、E_DISCOVERY、E_UNSUPPORTED_VERB、E_METRICS_UNAVAILABLE。服务端失败保留 AlreadyExists、Forbidden、Invalid、Conflict 等 reason。

遇到 E_DISCOVERY 可先尝试健康资源的精确表名；Forbidden 应检查凭据和 RBAC；Invalid 应检查资源 schema/不可变字段；Conflict 应重新查询再决定是否重试。Secret 的 data 保持 API Base64 表示，**不是加密**；SELECT manifest 可以输出敏感数据，请保护 stdout、SQL 文件与日志。

## 验证与限制

已覆盖解析、三值逻辑、三表写入、通用资源、Metrics、CRD/CR 和 Review。任务书规定的第 1 章由离线测试验证，第 2–8 章通过真实集群 CLI 验收；额外覆盖 scope/verbs、写保护、部分失败、精确整数、nullable JSON 字段及所有列出的内置资源族路由。

```sh
go test ./...
go vet ./...
```

默认测试不连接集群。真实 E2E 会创建/修改/删除测试资源，需要独立集群和明确授权的测试凭据，准备方法及验收映射见 [测试说明](test/README.md)。

不支持 JOIN、事务、排序/聚合、算术、SQL 注释、多语句脚本、批量 INSERT、generateName、子资源（status/scale 等）或流式接口。已有功能通过测试不等于对所有 Kubernetes 版本、准入策略和资源 schema 的无条件兼容保证。
