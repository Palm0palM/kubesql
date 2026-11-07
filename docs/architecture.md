# 技术栈与核心架构

## 1. 目标与边界

依据 `KubeSQL.pdf` 第 1–37 页设计。项目名为 KubeSQL，二进制名为 `ksql`。
这是时间紧迫的冰岩后端实习任务，优先交付可验收、可理解的功能闭环，不以完成全部八章为无条件目标。
本文保留后续能力的设计方向，不代表必须立即实现；实际范围按 `plan.md` 的 P0–P3 优先级、剩余时间和用户确认决定。
SQL 是 Kubernetes API 的前端，不引入数据库、存储服务或后台控制器。
首版一次从 stdin 读取一条语句，接受可选末尾分号，必须消费到 EOF；不做多语句脚本、REPL、JOIN、事务或通用 SQL 函数系统。

不可改变的约束：全程 Go、逐字符 lexer、递归下降 parser、官方 client-go、程序不调用 kubectl。
kubectl 可以作为外部验收辅助工具，不属于程序执行路径。

## 2. 技术选型

| 领域 | 决定 | 理由 |
| --- | --- | --- |
| 语言 | Go，项目基线计划为 Go 1.27 | 已观测本机版本输出为 1.27.1；工具链构建能力未验证，M0 检查，不依赖本机实验选项 |
| CLI | 标准库 `flag`、`io`、`context`、`os/signal` | 单一命令，避免引入 CLI 框架 |
| SQL | 手写 rune 扫描器、递归下降、显式 AST | 符合任务约束，语法与语义分离 |
| Kubernetes | `k8s.io/client-go` 的 clientcmd、dynamic、discovery | 统一普通资源访问，后续无需增加 CR 的 Go 类型 |
| 对象与指标 | `k8s.io/apimachinery`、`k8s.io/api`、`k8s.io/metrics` | unstructured、API 错误、Quantity、官方 Metrics 客户端 |
| JSON | `encoding/json`，精确保留整数 | 不把全部数字转换为 float64 |
| 字段定位/修改 | RFC 6901 JSON Pointer、RFC 6902 JSON Patch | 精确修改目标字段，支持整对象替换和版本前置检查 |
| 测试 | 标准库 testing、表驱动，按需 fake client/httptest | 保留实集群 E2E；全面 fuzz 和额外边界覆盖后置 |
| 实集群 | 独立 Kubernetes 测试环境，优先 minikube | WSL 中按可用运行时选择 driver；Metrics Server 仅进入 M7 时准备 |
| 质量 | gofmt、test、vet、staticcheck、fix、modernize、deadcode | 对齐任务书提交要求 |
| 自动化 | 先用直接构建/测试命令，按需 Makefile；CI 后置 | 不让自动化工程阻塞功能闭环，测试逻辑仍由 Go 实现 |

Kubernetes 模块计划采用成熟的 v0.35.x 同版本系列，实集群以 Kubernetes 1.35 为基线。
在实际接入依赖时核实兼容性及补丁版本，再在 `go.mod`、`go.sum` 固定依赖；工具在需要时选兼容版本，不使用浮动 `latest`。
这些是实施基线，不声称已下载依赖或已验证集群。Go module 路径使用现有远端 `github.com/Palm0palM/kubesql`。

## 3. 核心执行链

```text
stdin + CLI flags
      ↓
逐字符 Lexer → Tokens（起止位置）
      ↓
递归下降 Parser → AST（保留 *，不访问集群）
      ↓
绑定与校验 → 资源描述 / 列 / 类型 / scope / verbs / 写保护
      ↓
执行计划
      ├─ 普通资源 → dynamic client → Kubernetes API
      └─ 指标虚拟表 → Metrics client → Metrics API
      ↓
统一 WHERE 求值 → 投影 或 逐对象写入
      ↓
JSON 结果 / 写入汇总 / 退出码
```

采用统一 dynamic CRUD，而不是先写三套 typed CRUD 再替换。
easy/medium 先用静态资源描述绑定三个固定 GVR；hard 再接入 Discovery 解析器。
Deployment 和 Ingress 的便捷列是资源描述中的映射/校验策略，不是独立执行引擎。
指标表使用单独读取适配器，复用 WHERE 与投影，不勉强模拟完整资源 CRUD。

### 包划分

```text
cmd/ksql/             main 与退出码
internal/cli/         flags、输入输出、依赖装配；Run 可测试
internal/sql/         token、lexer、AST、parser；不依赖 Kubernetes
internal/engine/      绑定、类型值、三值逻辑、字段访问、执行与结果汇总
internal/kube/        kubeconfig、资源描述、Discovery、dynamic/metrics 适配
test/e2e/             显式启用的实集群验收测试
testdata/             版本控制内的可复用 YAML、SQL、预期结果
docs/                 正式交付文档
tmp/                  忽略的本地工作产物
```

以少量清晰包开始，不另建 lexer/parser/AST 各自的包，不建通用插件系统。
engine 在使用处定义少量可注入接口，例如资源解析和 List/Create/Patch/Delete；kube 实现它们。
依赖方向为 CLI → engine/sql/kube，engine → sql，kube 不反向依赖 engine。
具体接口在首个使用场景出现时定义，不提前搭建大型框架。
目录按当前功能需要创建，不预建完整结构；优先具体类型和直接调用，仅在实际解耦或测试需要时引入小接口。
不为架构完整性预建通用执行计划框架、资源策略系统或超出任务书的 CLI 功能。
每完成一个独立小功能必须说明数据结构、调用链和设计取舍，暂停等待用户代码审查及明确确认，不能自动进入下一个功能或阶段。

## 4. SQL 与值模型

- 关键字忽略大小写；未引用标识符归一化，双引号标识符保留内容及引用标记。
- 单引号字符串按 SQL 的 `''` 转义；双引号标识符按 `""` 转义。SQL 与 JSON 分层解析。
- token/AST 保留 1-based 行列及 byte offset，列号按字符计算；错误定位到遇到的 token。
- 星号有独立节点，绑定阶段根据该阶段公开的表列展开，不递归展开 manifest。
- AST 分为 Select、Update、Delete、Insert；表达式包含列、字面量、比较、逻辑、IS NULL，以及后续受限的 CAST JSON。
- WHERE 优先级：比较/IS NULL > NOT > AND > OR；括号改变结合顺序。
- UPDATE/DELETE/SELECT 共用同一表达式解析和求值代码。
- 值显式区分 SQL NULL、字符串、数字、布尔、JSON 对象/数组；赋值额外区分 Remove 与 Set(JSON null)。
- SQL 数字及 JSON 数字保留原精确表示；比较可用标准库 `math/big.Rat`，整数进入 unstructured 时保持 int64，并对越界明确报错。
- 检查 client-go 的 unstructured 解码确实保留 int64；JSON 输入用 UseNumber 或 Kubernetes 的保整数解码器，测试覆盖大于 2^53 的整数。
- 数字和字符串不做隐式互转；对象/数组不能参与 WHERE 比较。已知类型尽量在绑定期检查，动态字段在求值期检查。
- 比较遇到 NULL 得 UNKNOWN；NOT UNKNOWN 仍是 UNKNOWN；AND/OR 使用完整三值真值表，WHERE 仅保留 TRUE。
- 未知列是错误，合法路径缺失才是 NULL。输出 JSON 数字、布尔、对象、数组及 null，绝不全部字符串化。

客户端过滤是首版的正确性基线。Kubernetes fieldSelector 不支持完整 SQL 的 OR、数值比较、NULL 与任意路径，不能直接等价翻译。
先分页 List 再客户端求值；不做过滤下推优化。未知列和无效赋值必须在资源 List/写请求前报错。
hard 阶段未知表解析允许 Discovery 请求，这是与 easy 阶段静态表校验的区别。

## 5. 资源模型与 Discovery

资源描述包含 GVR、Kind、scope、verbs、公开列和可写策略。通用列为 name、namespace、labels、annotations、manifest。
集群级资源 namespace 为 NULL，API 调用不带 namespace。前三张表保留任务书便捷列。
labels/annotations 的 SQL 字符串按已知 JSON 列解析为字符串映射；manifest 解析为完整 JSON 对象。
其他路径的普通字符串不猜测 JSON；对象/数组赋值使用 `CAST('...' AS JSON)`。

解析规则：

1. 精确表名 `"v1/configmaps"` 或 `"group/version/resource"` 仅访问指定版本。
2. 简单复数表名按实际 APIResource.Name 查找，不猜复数、不使用 shortNames。
3. 先按 API group 判歧义，同组多版本不算歧义。
4. 无歧义时选实际提供该资源的 preferred version；否则按 Discovery 的版本顺序回退。
5. 只使用请求内快照，不设磁盘/跨进程缓存；显式版本可定向发现，简单表名需检查组间歧义。
6. 部分组发现失败时保留健康组结果及失败信息；精确健康组可用。简单名若不能排除失败组中的歧义，则报告发现失败而不猜选。
7. SELECT 需 list；UPDATE 需 list+patch；DELETE 需 list+delete；INSERT 需 create，否则 E_UNSUPPORTED_VERB。
8. 识别带 `/` 的 API 子资源，明确不支持 status、scale、exec、logs、portforward 等普通表操作。

CRD、CR 和聚合 API 使用这条通用路径。创建 CRD 后由用户或测试等待 Established 及 Discovery 可见，再提交 CR，CLI 不隐式编排两步注册流程。
Review 类 create-only API 走能力策略，不强加持久化对象 metadata.name 规则，也不虚构 list 能力。

JSON Pointer 解码严格处理 `~0`、`~1` 和数组索引。读取路径缺失返回 NULL，错误转义是错误。
写入只允许在已有父对象中添加缺失叶子，不自动创建中间节点；禁止利用数组插入或重叠赋值造成含糊修改。
重复输出键、重复赋值及相互覆盖的祖先/后代赋值在绑定期拒绝，避免 JSON 键覆盖和 patch 顺序依赖。

## 6. 写入安全

UPDATE/DELETE 缺 WHERE 返回 E_WHERE_REQUIRED。namespaced 写操作仅使用解析出的单一 namespace，拒绝 --all-namespaces；集群级资源仍按 scope 执行。
namespace 优先级是显式 flag > 所选 context 的 namespace > default。

UPDATE/DELETE 先完成整条语句校验，再 List 全部候选、求值并确定匹配集合，然后开始写入。
这样后面一行出现 WHERE 类型错误时，不会让前面的对象已被修改；这不是事务保证。
对象状态相关的 patch 构造错误属于逐对象失败，与 API 错误一样汇总，并继续处理其余对象。

- UPDATE 用 JSON Patch：首先 test `/metadata/resourceVersion`，再 add/replace/remove 精确路径。
- labels/annotations 整列替换，绝不用 Merge Patch 残留旧键；未指定的字段保持不变。
- replicas 必须为非负、适合 API 字段范围的整数。
- Ingress 便捷列只修改已有 Service 默认后端的名称，保留端口；名称合法且非空，不接受 NULL。
- SQL NULL 移除可选字段；CAST JSON null 保留 JSON null，能否接受交给服务端 schema。对已缺失字段移除可视为无变化。
- 禁止更新根对象、apiVersion、kind、status、metadata.name/namespace 及服务器管理字段，以及它们的祖先或后代路径。
- 保护路径按解码后的段判断，而不是字符串前缀；允许安全地整替换 labels/annotations。
- INSERT 检查 JSON 对象、目标 GVK、namespace 和禁止字段；持久化资源要求 name，拒绝 generateName；create-only 请求对象另按 API 能力校验。
- 不使用 apply/upsert，不覆盖同名对象，不自动重建；AlreadyExists 保持可检查。
- DELETE 普通删除，不清理 finalizer；API 接受即计成功。可带 UID/resourceVersion 前置条件保护已读取对象，不等待最终消失。
- 多对象逐个处理，不自动重试冲突、不回滚；部分成功输出汇总并返回非零退出码。

## 7. Metrics、输出与错误

pod_metrics：name、namespace、cpu_millicores、memory_bytes；node_metrics：name、cpu_millicores、memory_bytes。
Pod 全部容器 CPU Quantity 相加后调用 MilliValue，内存相加后转 bytes；节点直接读取实际指标。
Metrics Server 不可用为 E_METRICS_UNAVAILABLE，403 保留 Forbidden 权限错误，无样本返回空数组，不填充虚假的零值。

CLI 默认 JSON，也明确接受 --output json；不引入其他输出格式。
SELECT stdout 始终为数组，包括空 `[]`；写入成功 stdout 为 `{"affected_rows":N}`，失败为任务书规定的汇总对象。
语法、绑定、连接等整语句错误输出到 stderr，带 code/message，语法错误额外带 line/column；stdout 不混入日志。
退出码约定：0 成功，1 执行/API/部分失败，2 输入/语法/语义校验错误。
对象失败保留 resource、namespace、name、reason；Kubernetes StatusReason 不伪装成 SQL 错误。
除任务书必需错误码外，补充稳定的未知表/列、类型、发现失败、歧义及写保护错误码，并在实现时测试与文档化。

错误消息不回显整条 SQL、manifest、patch 或 Secret 内容。API 校验详情也可能含敏感值，需要脱敏，只保留安全的原因、字段路径与对象身份。
kubeconfig 通过 clientcmd 默认加载规则及 overrides 读取，支持 KUBECONFIG、--kubeconfig、--context、--namespace；不静默回退到其他集群。
请求支持取消和明确超时，不引入后台 watch/informer。
