# M9 冻结验收记录

## 结论与范围

重新阅读 `docs/KubeSQL.pdf` 全文（37页），按任务书的实现约束、功能规则、1–8章全部规定用例核对最终源码与现有测试。当前项目具备任务书要求的完整CLI功能闭环；规定用例与本轮补充验收全部通过。

这表示对当前代码、明确测试集群和列出的检查有实际通过证据，不是对所有Kubernetes版本、资源schema、准入策略或任意输入的无缺陷保证。

## 实现规则复核

| 要求 | 核对结果/证据 |
| --- | --- |
| 全Go、逐字符lexer、递归下降parser | internal/sql；不使用正则/空格切分解析SQL |
| 官方client-go、不通过kubectl工作 | internal/kube dynamic/discovery；kubectl只用于环境检查 |
| kubeconfig/context/namespace优先级 | kube/client_test.go、CLI HTTP测试；cluster scope忽略namespace |
| JSON数组、数字/null保持类型 | Query投影、canonicalRows；当前SELECT *包含后续公开列 |
| SELECT/AST/EOF/错误位置 | sql/parser_test.go：1-1 AST、1-2 line=1 column=14 |
| WHERE优先级、类型、三值逻辑 | expression与value测试；真实第3章全部输入及NOT UNKNOWN |
| UPDATE/DELETE条件闸门、精确patch、字段保留 | BindWrite/WHERE完整候选检查；第4章实集群、HTTP部分失败继续 |
| replicas/Ingress已有后端/端口保留 | bindAssignment与w.patch；实集群spec修改前后比较 |
| 映射整体替换 | JSON Patch add/replace；用户旧键消失、服务端自动键不隐藏 |
| 普通DELETE、不清finalizer | UID/resourceVersion前置条件；实集群finalizer保持、API接受即成功 |
| 单manifest INSERT、分层SQL/JSON | parser与BindInsert；第5章prepare/target分开、AlreadyExists原对象不变 |
| Discovery路由/版本/scope/verbs | Snapshot及测试；无shortName猜测、按group判歧义、精确无回退 |
| JSON Pointer/AS/CAST/NULL | 严格转义/输出键/重叠赋值；已有父对象写入、数组已有索引替换 |
| 更新保护及Secret错误脱敏 | protectedPath解码后按段检查；禁止根/metadata祖先/服务器字段；错误只reason/身份 |
| 精确整数、不统一float64 | big.Rat比较与int64写入；本轮实集群>2^53创建/SQL及CAST更新/WHERE |
| 真正Metrics/Quantity先汇总 | 固定150m/83886080与250m/536870912；亚毫核先加后舍入；实Pod Ready/采样 |
| 陌生CRD/CR及CRD本身 | 第8章SQL创建/更新/删除，Established→Discovery→实例，多served version |
| API schema/RBAC/不可变/冲突 | 实际Invalid/Forbidden/旧版本patch拒绝；HTTPConflict/逐对象继续 |
| 聚合组部分失败/Review能力 | HTTP部分组失败、健康精确资源可用；真实无name SelfSubjectAccessReview |

无新产品功能、框架、依赖或源码逻辑改动；仅补强冻结验收测试、夹具nullable字段和用户文档。

## 本轮增加的验收证据

1. `TestHardNative/catalog` 对任务书列出的18个内置资源族逐个执行精确SELECT name/namespace，并与官方dynamic List结果比较：Pod、Node、Service、ReplicaSet、StatefulSet、DaemonSet、Job、CronJob、ConfigMap、Secret、PV、PVC、StorageClass、ServiceAccount、Role、ClusterRole、RoleBinding、ClusterRoleBinding。
   - 这是实际API读取路由/scope覆盖；空集合合法，不等于每种工作负载部署或完整生命周期全部验证。
2. CRD加入一个nullable的optional字段，真实API验证CAST JSON null键保留，SQL NULL键移除，再恢复普通字符串。
3. 实际CR创建9007199254740993、WHERE比较、普通SQL更新9007199254740995、CAST JSON更新9007199254740997，逐次确认精确JSON整数。

## 实际执行与结果

Go运行使用GOEXPERIMENT=、GOTOOLCHAIN=local；GOCACHE/GOTMPDIR/GOMODCACHE/TMPDIR均指向仓库tmp/，没有更改全局配置。

- `go test -count=1 -coverprofile=tmp/m9-coverage.out ./...`：通过。cli覆盖92.5%、engine89.6%、kube87.4%、sql97.8%；没有以覆盖率数字代替功能验收。
- `go test -race -count=1 ./...`：通过（离线及HTTP测试；不宣称E2E race已运行）。
- `go vet ./...`、`go vet -tags=e2e ./...`：通过。
- staticcheck普通包及`-tags=e2e`：通过，无报告。
- `go fix -diff`普通包/E2E：无建议。
- modernize普通包/E2E、deadcode普通/-test：无报告。
- 构建真实CLI与`GOBIN=$PWD/tmp/readme-bin go install ./cmd/ksql`：通过；`--help`可用。
- `go mod verify`：通过；go.mod/go.sum未变。
- `go test -tags=e2e -count=1 -v ./test/e2e`，显式kubesql-test/kubeconfig/真实二进制：全套通过，206.403s，日志`tmp/m9-e2e.log`。
- 第1章离线AST/错误位置通过；第2–8章全部真实CLI验收再次通过。
- README安装得到的二进制额外执行Deployment过滤、精确StatefulSet查询、Node指标，成功；Node真实样本115m/879697920bytes（可变化）。
- `git diff --check`：通过；源码gofmt与质量工具检查已运行。
- tracked文件凭据特征检查未发现真实私钥/证书/token；fixture密码为明确虚构not-a-real-secret。
- 最后集群只剩系统Namespace与已有k8s-lab；CRD列表为空；本套件测试Namespace/CR/CRD均已清理，未触碰其他资源。

## 对任务样例的必要解释

- 第2章早期SELECT *列集随后按任务要求扩展labels/annotations/manifest/通用namespace；最终CLI不能假装仍只输出最初三列。E2E单独验证manifest后比较早期便捷列。
- Deployment controller自动revision注解、Namespace标准标签、kube-root-ca.crt ConfigMap均来自真实服务端；CLI不隐藏，测试只处理明确的服务器生成项。
- INSERT目标只比较明确字段，不比较服务端时间戳等默认值；真实Metrics只验证身份、一行、非负整数，不要求固定样例用量。

## 文档交付

- 原README完整保留到`docs/development-history.md`；与HEAD原README逐字节cmp一致，不丢开发历史。
- 根README改为产品用户文档：安装、stdin/连接参数、查询/过滤/Pointer、CRUD、安全风险、Metrics、CRD/Review、输出/退出码、限制。
- 新`test/README.md`为可随仓库提交的测试准备/验收映射，README链接指向此实际可交付文件。
- `docs/`保持原忽略规则，因此历史归档与本记录只在本机，不创建远端不可访问的链接，也不擅自强制纳入Git。

## 尚不承诺的内容

无JOIN/ACID/多语句/批量INSERT/generateName/子资源/流式接口，不添加任务书外功能。未实现CI/全面fuzz；不声称所有版本/schema全面覆盖。真实自定义聚合服务未部署，部分发现失败由HTTP模拟；Review输出沿用写入汇总，不提供allowed决策查询。

本轮未提交或推送，等待用户审查和明确提交授权。
