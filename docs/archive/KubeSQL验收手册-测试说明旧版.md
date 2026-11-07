# KubeSQL 验收手册

> 面向现场展示、命令练习和代码答辩。以当前已验收实现为准，不把计划或模拟测试当作真实集群结果。
>
> 所有写入仅在专用 Minikube 测试集群执行。本文手工演示使用 `ksql-demo-accept`，不要占用自动测试的 `sql-*` Namespace 或 `lab.example.com` CRD。

### 阅读路线

- **现场照着操作**：第 3–5 节准备与构建，第 6–10 节功能演示，第 11 节跑测试，第 15 节清理。
- **准备代码讲解**：第 12 节按源码文件逐层展开，第 13 节练习答辩，第 16 节快速复习。
- **遇到环境故障**：第 14 节。第一次练习建议完整执行一遍，记录自己机器上的耗时。

## 1. 验收怎么讲：先给结论，再给证据

开场可以这样说：

> KubeSQL 是 Go 编写的 Kubernetes SQL CLI，不是关系型数据库。一个表对应一种 Kubernetes 资源，一行对应一个 API 对象，列是对象字段。SQL 经逐字符词法分析和递归下降语法分析生成 AST，再绑定 Discovery 资源和列，最后通过官方 client-go 执行。查询在客户端做 WHERE 和投影，写入使用带前置条件的精确 API 请求。下面我会现场构建、展示与 kubectl 一致的实际结果，并运行任务书测试。

### 建议现场顺序（约 25–35 分钟）

| 时间 | 内容 | 现场证据 |
| --- | --- | --- |
| 2 分钟 | 项目定位、目录与处理链路 | 源码、架构图 |
| 3 分钟 | 版本、集群身份、编译与静态检查 | 退出码 0；无构建/检查诊断 |
| 8 分钟 | Namespace、查询、WHERE、更新与删除 | KubeSQL 结果 + kubectl 对照 |
| 5 分钟 | Pointer、CRD、JSON null/整数、Metrics | API 实际对象和实际指标 |
| 4 分钟 | 单测和真实 E2E | 当场 PASS，不使用截图代替执行 |
| 5–10 分钟 | 核心代码与答辩 | lexer/parser、绑定、三值逻辑、patch、Discovery |

时间紧时：先启动 E2E，运行期间讲源码；手工演示 Namespace 与测试资源隔离，避免干扰。不要为了节省时间假称跳过的用例已经当场运行。

## 2. 三个工具各自做什么

| 工具 | 作用 | 不是什么 |
| --- | --- | --- |
| minikube | 在本机运行 Kubernetes 测试集群；管理 profile、节点和 addon | 不是 SQL CLI |
| kubectl | 通用 Kubernetes CLI，用于环境检查、API 对照、等待状态 | 不是 KubeSQL 的底层子进程 |
| ksql（项目产物） | 从 stdin 读取一条 SQL，通过 client-go 访问 API Server | 不是交互式数据库 shell，也不接收 SQL 位置参数 |

```text
minikube → 启动本机 Kubernetes

kubectl ───────────→ API Server ←────────── ksql
                                              ↑
                                     stdin 的一条 SQL
```

`kubectl get` 与 `SELECT` 是两条独立客户端访问同一 API 的证据。验收脚本调用 kubectl 合法；产品代码不通过执行 kubectl 工作。

### Kubernetes 术语速记

- **context**：kubeconfig 中的一组集群、用户、默认 namespace 选择，不是 Kubernetes 对象本身。
- **Namespace**：集群内的命名空间，隔离同类资源名称与操作范围，不等于独立集群。
- **Pod**：实际运行容器的单位；一个 Pod 可以有多个容器。
- **Deployment**：声明目标副本数与 Pod 模板，由控制器经 ReplicaSet 管理 Pod。
- **Service / Ingress**：前者提供服务寻址，后者描述 HTTP 路由；仅创建 Ingress 对象不代表已经安装路由控制器。
- **spec / status**：spec 是期望状态，status 是服务端/控制器报告的状态。KubeSQL 不允许 UPDATE status。
- **CRD / CR**：CRD 注册自定义类型，CR 是该类型实例；不能把两者创建时序倒过来。

## 3. 验收前一天：环境准备与预演

### 3.1 必备软件

已验证组合：Go 1.27.2、client-go v0.35.9、minikube 1.39.0、Docker driver、Kubernetes 1.35.0、Metrics Server 0.9.0。工具版本可能不同，提前核验，不在现场升级依赖。

WSL/Arch 环境要确认 Docker daemon 可访问；仅安装 docker 命令不代表 daemon 已启动。Go 必须满足项目版本，`jq` 仅供可选的 JSON 格式化，不是产品依赖。

以下命令均从**仓库根目录**运行。已经安装系统工具时使用系统 PATH；本项目先前准备的二进制在 `tmp/tools/`，可在当前终端加入：

```sh
export PATH="$PWD/tmp/tools:$PATH"
go version
minikube version
kubectl version --client
docker info
git status --short
git log -1 --oneline
```

解释：`git status --short` 为空表示跟踪文件没有待提交改动；忽略的 `tmp/` 和 `docs/` 不会显示。

### 3.2 所有临时产物留在 tmp/

```sh
mkdir -p tmp/kube tmp/bin tmp/go-cache tmp/go-tmp tmp/go-mod-cache tmp/acceptance
export MINIKUBE_HOME="$PWD/tmp/minikube"
export KUBECONFIG="$PWD/tmp/kube/config"
export GOEXPERIMENT=
export GOTOOLCHAIN=local
export GOCACHE="$PWD/tmp/go-cache"
export GOTMPDIR="$PWD/tmp/go-tmp"
export GOMODCACHE="$PWD/tmp/go-mod-cache"
export TMPDIR="$PWD/tmp/go-tmp"
```

如果把 module 缓存放进仓库 tmp/，需要嵌套 module 防止根 `./...` 扫描缓存源码。当前环境已有 `tmp/go.mod`；新环境只在它不存在时建立隔离文件：

```sh
if [ ! -e tmp/go.mod ]; then
  printf 'module local.invalid/ksql-tmp\n\ngo 1.27.0\n' > tmp/go.mod
fi
go mod download
go mod verify
```

不要在 tmp/ 运行 `go mod tidy`。第一次下载依赖可能需要网络；现场前预热缓存，但现场仍实际编译。

### 3.3 启动或复用专用 profile

```sh
minikube profile list
minikube start -p kubesql-test --driver=docker --kubernetes-version=v1.35.0 \
  --cpus=2 --memory=3072
chmod 600 "$KUBECONFIG"
minikube status -p kubesql-test
kubectl --context kubesql-test get nodes -o wide
kubectl config current-context
kubectl config view --minify
```

确认 context 是 `kubesql-test`，节点名也是 `kubesql-test`；E2E 对节点名有明确断言。查看配置时不要使用 `--raw` 投屏，不要展示证书/私钥/token。

**context 名称不构成安全证明。** 同时确认配置中的 API 地址、minikube profile 和节点确实属于本机专用集群。不要用生产凭据运行测试。

### 3.4 常用 Minikube 命令速记

```sh
minikube status -p kubesql-test             # 看集群状态
minikube profile list                      # 看所有 profile
minikube addons list -p kubesql-test        # 看插件状态
minikube ip -p kubesql-test                 # 看节点 IP
minikube logs -p kubesql-test               # 环境故障时查日志
minikube image ls -p kubesql-test           # 查看节点镜像
minikube image load busybox:1.36 -p kubesql-test  # 从宿主加载已存在的镜像
minikube stop -p kubesql-test               # 停止，保留集群数据
```

`minikube image load` 前要有宿主本地镜像；可提前 `docker pull busybox:1.36`。不要把 `minikube delete` 当作日常准备操作，它会删除该 profile 的集群。

### 3.5 准备 Metrics

```sh
minikube addons enable metrics-server -p kubesql-test
kubectl --context kubesql-test -n kube-system rollout status deployment/metrics-server --timeout=180s
kubectl --context kubesql-test get apiservice v1beta1.metrics.k8s.io
kubectl --context kubesql-test top nodes
```

APIService 应为 Available=True。指标可能要等采样；Deployment Ready 并不立即保证所有 Pod 都有指标。

之前环境遇过上游镜像下载超时并加载同版本镜像，不能据此假设新机器一定可拉取。先排查镜像/网络；不要临时随意禁用 TLS 校验或把假的 requests/limits 当成 usage。

### 3.6 验收前检查单

- [ ] 工具可运行、Docker 可用、集群节点 Ready。
- [ ] kubeconfig 保留在 tmp/，权限 600，明确 API 地址与 context。
- [ ] module 缓存已预热、busybox:1.36 可拉取或已加载。
- [ ] Metrics API 可用，真实 E2E 至少完整预演一次。
- [ ] 手工演示名字未被其他人使用；不预先 apply 自动测试 fixtures。
- [ ] 源码导航和 SQL 命令练熟；错误演示单独执行，不使用全局 `set -e`。
- [ ] 知道怎样清理自己创建的资源；不删除既有 `k8s-lab`。

## 4. 现场构建：如何证明没有 Warning/Error

Go build 通常成功时没有输出，不会主动打印“0 Warning”。构建、vet、staticcheck 分开证明，不能说编译通过就代表没有任何 bug。

```sh
go version
go build -o tmp/bin/ksql ./cmd/ksql
echo "build exit=$?"
go vet ./...
echo "vet exit=$?"
go vet -tags=e2e ./...
echo "vet-e2e exit=$?"
./tmp/bin/ksql --help
```

每个 `echo` 紧跟对应命令，0 表示该命令成功。构建成功且 stderr 无诊断，是编译无错误的证据；vet 无诊断是静态检查证据。

staticcheck 在当前环境已安装到 tmp/tools/，用**兼容 Go 1.27 的构建**运行：

```sh
./tmp/tools/staticcheck ./...
echo "staticcheck exit=$?"
./tmp/tools/staticcheck -tags=e2e ./...
echo "staticcheck-e2e exit=$?"
```

新机器若没有该工具，不要声称已运行。旧工具可能因 Go export-data 不兼容失败，这属于工具链问题，仍要提前解决，不能把报错隐藏。

可选的附加检查：

```sh
go mod verify
git diff --check
go fix -diff ./...
./tmp/tools/modernize ./...
./tmp/tools/deadcode ./...
```

检查建议仅审阅，现场不自动重写/删代码。源文件/依赖内容变动后再构建，否则演示二进制可能不是展示的源码。

## 5. KubeSQL 用法先练熟

### 5.1 一条 SQL 来自 stdin

```sh
./tmp/bin/ksql --kubeconfig "$KUBECONFIG" --context kubesql-test <<'SQL'
SELECT name FROM namespaces;
SQL
```

也可以用文件或管道：

```sh
printf '%s\n' 'SELECT name FROM namespaces;' > tmp/acceptance/query.sql
./tmp/bin/ksql --kubeconfig "$KUBECONFIG" --context kubesql-test < tmp/acceptance/query.sql
printf '%s\n' 'SELECT name FROM namespaces;' | ./tmp/bin/ksql --context kubesql-test
```

第二种省略 kubeconfig 时仍受已导出的 KUBECONFIG 控制。**没有 `ksql -e`，也没有 `ksql "SELECT ..."`**。每次只能一条语句，分号可选，不能一次送一个多语句脚本。

`<<'SQL'` 的单引号让 shell 不展开正文；正文依然正常使用 SQL 单引号字符串和双引号 Pointer。不要把两套引号规则混淆。

### 5.2 连接与输出规则

| 选项 | 练习重点 |
| --- | --- |
| --kubeconfig | 明确配置文件，默认遵循 client-go 加载规则 |
| --context | 明确集群/context，不传取 current-context |
| --namespace | 不传取选定 context namespace，否则 default |
| --all-namespaces | 仅 SELECT；写入一律拒绝 |
| --output json | 默认且唯一输出格式；不是 kubectl 的 `-o` |

查询 stdout 是 JSON 数组，空结果 `[]`；写入 stdout 是 affected_rows 汇总。语句级错误写 stderr，部分对象失败写 stdout 汇总。退出码 0=成功、1=配置/API/I/O/对象失败、2=参数/语法/类型/安全错误。

### 5.3 定义本手册的命令快捷函数

在已设置环境、已构建的同一个终端执行：

```sh
export KSQL_BIN="$PWD/tmp/bin/ksql"
export DEMO_NS=ksql-demo-accept
kc() { kubectl --kubeconfig "$KUBECONFIG" --context kubesql-test "$@"; }
ks() { "$KSQL_BIN" --kubeconfig "$KUBECONFIG" --context kubesql-test --namespace "$DEMO_NS" "$@"; }
```

后文 `ks` 是终端函数，不是新增产品命令；正文 SQL 通过 stdin 传入。`kc` 是独立对照工具，`ks` 内没有执行 kubectl。

### 5.4 普通 kubectl 怎么用

基本形态：`kubectl [连接参数] [动作] [资源类型] [名字] [选项]`。

| 命令 | 含义 |
| --- | --- |
| `kc get namespaces` | 列出集群级 Namespace |
| `kc -n "$DEMO_NS" get pods` | 列出指定 namespace 的 Pod |
| `kc get pods -A` | 列出全部 namespace 的 Pod；对应 ksql 查询的 --all-namespaces |
| `kc -n "$DEMO_NS" get deployment web -o yaml` | 看单个对象完整 YAML |
| `kc -n "$DEMO_NS" get deployment web -o json` | 看完整 JSON；便于与 SELECT manifest 对照 |
| `kc -n "$DEMO_NS" describe deployment web` | 看摘要、控制器状态与事件 |
| `kc -n "$DEMO_NS" logs pod/measure -c one` | 看指定容器日志；本手册 sleep 容器没有业务日志属正常 |
| `kc api-resources` | 看 Discovery 可用资源及 scope |
| `kc explain deployment.spec.replicas` | 查资源字段说明 |
| `kc -n "$DEMO_NS" rollout status deployment/web` | 等 Deployment 就绪 |
| `kc -n "$DEMO_NS" delete configmap settings` | 删除一个明确对象（会实际修改集群） |

`kubectl apply -f file.yaml` 是声明式创建/更新，会对同名对象尝试更新；KubeSQL INSERT 只 Create，二者不能被讲成相同语义。`kubectl create -f file.yaml` 更接近 INSERT。

kubectl 的 `-n/-A/-o` 不可照搬给 ksql；ksql 使用 `--namespace/--all-namespaces/--output json`。kubectl 可支持资源缩写，但 ksql 普通表使用 Discovery 复数名称，不接受 po/deploy 等短名。

context 查询与切换的常见形式：

```sh
kubectl config get-contexts
kubectl config current-context
```

平时 `kubectl config use-context NAME` 会改变默认选择，`kubectl config set-context --current --namespace=NAME` 会改变默认 namespace。现场建议**不要依赖这些隐式切换**，统一使用 kc/ks 中的显式 context 和 namespace。

## 6. 手工演示一：查询、WHERE、写入与 kubectl 对照

### 6.1 检查名字并创建 Namespace

```sh
kc get namespace "$DEMO_NS"
```

应得到 NotFound（预期的环境检查错误）。若存在，**停止并核实所有权**，不要直接删除或覆盖它。确认不存在后：

```sh
ks <<'SQL'
INSERT INTO namespaces (manifest) VALUES ('{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"ksql-demo-accept"}}');
SQL
kc wait --for=jsonpath='{.status.phase}'=Active namespace/ksql-demo-accept --timeout=60s
ks <<'SQL'
SELECT name, namespace FROM namespaces WHERE name = 'ksql-demo-accept';
SQL
kc get namespace "$DEMO_NS" -o jsonpath='{.metadata.name}{"\n"}'
```

期待 INSERT `{"affected_rows":1}`；查询一行，namespace=null，因为 Namespace 是集群级资源。

### 6.2 创建 Deployment 和 Ingress

```sh
ks <<'SQL'
INSERT INTO deployments (manifest) VALUES ('{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","labels":{"demo":"ksql"}},"spec":{"replicas":2,"selector":{"matchLabels":{"app":"ksql-web"}},"template":{"metadata":{"labels":{"app":"ksql-web"}},"spec":{"containers":[{"name":"worker","image":"busybox:1.36","command":["sh","-c","sleep 3600"]}]}}}}');
SQL
ks <<'SQL'
INSERT INTO ingresses (manifest) VALUES ('{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"route"},"spec":{"defaultBackend":{"service":{"name":"web","port":{"number":80}}}}}');
SQL
kc -n "$DEMO_NS" get deployments,ingresses
kc -n "$DEMO_NS" rollout status deployment/web --timeout=180s
```

Ingress 只用来展示 API 字段，不安装 Ingress controller，也不承诺流量可达。默认后端的 Service 不需要真实存在即可验证这些对象字段；因此不要把它作为网站访问演示。

Deployment 的 replicas 是期望副本数，不等于 Ready 副本数。查询 spec 的任务与工作负载就绪是不同证据。

### 6.3 SELECT 与 WHERE

```sh
ks <<'SQL'
SELECT name, namespace, replicas FROM deployments WHERE replicas >= 2;
SQL
kc -n "$DEMO_NS" get deployments -o custom-columns=NAME:.metadata.name,NAMESPACE:.metadata.namespace,REPLICAS:.spec.replicas
ks <<'SQL'
SELECT name FROM deployments WHERE name = 'web' OR (replicas >= 3 AND NOT name = 'worker');
SQL
ks <<'SQL'
SELECT name, default_backend_service FROM ingresses;
SQL
kc -n "$DEMO_NS" get ingress route -o jsonpath='{.spec.defaultBackend.service.name}{"\n"}'
```

解释：SQL 返回 JSON 而 kubectl 默认表格；比较语义字段，不比较空格或键顺序。未指定 ORDER BY，行序不是承诺。

### 6.4 UPDATE：只改指定字段

先看对象，然后更新：

```sh
kc -n "$DEMO_NS" get deployment web -o jsonpath='{.spec.replicas}{" "}{.spec.template.spec.containers[0].image}{"\n"}'
ks <<'SQL'
UPDATE deployments SET replicas = 3 WHERE name = 'web';
SQL
kc -n "$DEMO_NS" get deployment web -o jsonpath='{.spec.replicas}{" "}{.spec.template.spec.containers[0].image}{"\n"}'
ks <<'SQL'
UPDATE ingresses SET default_backend_service = 'web-v2' WHERE name = 'route';
SQL
kc -n "$DEMO_NS" get ingress route -o jsonpath='{.spec.defaultBackend.service.name}{" "}{.spec.defaultBackend.service.port.number}{"\n"}'
```

期待 replicas 从 2 到 3，image 不变；Ingress 名称变为 web-v2，端口仍是 80。

对应的普通 kubectl 修改方式（**可选，会改变状态**）：

```sh
kc -n "$DEMO_NS" scale deployment web --replicas=2
ks <<'SQL'
SELECT name, replicas FROM deployments WHERE name = 'web';
SQL
```

这证明 kubectl 写入可由 ksql 读到，ksql 写入可由 kubectl 读到，不是本地伪造结果。kubectl scale 与 ksql JSON Patch 的实现细节不同，只比较最终字段语义。

### 6.5 标签、注解是替换，不是合并

```sh
ks <<'SQL'
UPDATE deployments SET labels = '{"owner":"alice"}', annotations = '{"purpose":"acceptance"}' WHERE name = 'web';
SQL
ks <<'SQL'
SELECT name, labels, annotations FROM deployments WHERE name = 'web';
SQL
kc -n "$DEMO_NS" get deployment web -o json
```

之前 metadata.labels 的 demo 键应消失，owner 出现。只改 Deployment metadata，不改 Pod template labels，也不破坏 selector。Deployment controller 可能补回 revision 注解，不能把它误判为“替换没有成功”。

## 7. 手工演示二：通用资源、Pointer、CAST、创建与删除

### 7.1 用 ConfigMap 展示完整 CRUD

```sh
ks <<'SQL'
INSERT INTO configmaps (manifest) VALUES ('{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings"},"data":{"mode":"dev","region":"local"}}');
SQL
ks <<'SQL'
SELECT name, "/data/mode" AS mode FROM "v1/configmaps" WHERE name = 'settings';
SQL
kc -n "$DEMO_NS" get configmap settings -o jsonpath='{.data.mode}{"\n"}'
ks <<'SQL'
UPDATE configmaps SET "/data/mode" = 'prod' WHERE name = 'settings';
SQL
kc -n "$DEMO_NS" get configmap settings -o jsonpath='{.data.mode}{" "}{.data.region}{"\n"}'
```

期待 prod local，说明 sibling 字段保留。对应 kubectl 可用 `patch --type=json`，而不是重建整个对象：

```sh
kc -n "$DEMO_NS" patch configmap settings --type=json -p='[{"op":"replace","path":"/data/mode","value":"dev"}]'
ks <<'SQL'
SELECT "/data/mode" AS mode FROM configmaps WHERE name = 'settings';
SQL
```

整体写入对象时用 CAST；普通字符串不猜测为 JSON：

```sh
ks <<'SQL'
UPDATE configmaps SET "/data" = CAST('{"mode":"prod","region":"demo"}' AS JSON) WHERE name = 'settings';
SQL
ks <<'SQL'
UPDATE configmaps SET "/data/region" = NULL WHERE name = 'settings';
SQL
kc -n "$DEMO_NS" get configmap settings -o json
```

SQL NULL 移除 region 键。ConfigMap data 不适合演示 nullable JSON null；下一节使用声明 nullable 的 CRD。

### 7.2 数组与 Pointer 转义

```sh
ks <<'SQL'
SELECT name, "/spec/template/spec/containers/0/image" AS image FROM "apps/v1/deployments";
SQL
ks <<'SQL'
UPDATE configmaps SET annotations = '{"example.com/owner":"alice"}' WHERE name = 'settings';
SQL
ks <<'SQL'
SELECT "/metadata/annotations/example.com~1owner" AS owner FROM configmaps WHERE name = 'settings';
SQL
```

`~1` 解码为 `/`，`~0` 解码为 `~`。数组只允许替换已有索引，不插入/删除；缺失读取路径为 null，写入缺少中间父路径则报错。

### 7.3 重复创建不会覆盖

单独执行，预期失败，不要在 `set -e` 下串行执行：

```sh
ks <<'SQL'
INSERT INTO configmaps (manifest) VALUES ('{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings"},"data":{"mode":"SHOULD-NOT-REPLACE"}}');
SQL
echo "expected failure exit=$?"
kc -n "$DEMO_NS" get configmap settings -o jsonpath='{.data.mode}{"\n"}'
```

期待 failed_rows=1，reason=AlreadyExists，退出码 1；mode 仍 prod。INSERT 不是 upsert。

### 7.4 DELETE 与零匹配

```sh
ks <<'SQL'
DELETE FROM configmaps WHERE name = 'settings';
SQL
kc -n "$DEMO_NS" get configmap settings
ks <<'SQL'
DELETE FROM configmaps WHERE name = 'settings';
SQL
```

第一次 affected_rows=1；kubectl NotFound 是删除验证的预期结果；第二次 affected_rows=0，仍成功。计数表示 API 接受，不保证所有带 finalizer 资源已经消失。

## 8. 手工演示三：陌生 CRD/CR，不改 Go 类型

使用与 E2E 不同的组 `acceptance.ksql.example.com`。先确认 CRD 不存在；存在时先停止核实，不覆盖它。

```sh
kc get crd widgets.acceptance.ksql.example.com
```

### 8.1 SQL 创建 CRD，再等待注册

```sh
ks <<'SQL'
INSERT INTO "apiextensions.k8s.io/v1/customresourcedefinitions" (manifest) VALUES ('{"apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition","metadata":{"name":"widgets.acceptance.ksql.example.com"},"spec":{"group":"acceptance.ksql.example.com","names":{"plural":"widgets","singular":"widget","kind":"Widget"},"scope":"Namespaced","versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{"type":"object","properties":{"spec":{"type":"object","properties":{"size":{"type":"integer","format":"int64"},"optional":{"type":"string","nullable":true}}}}}}}]}}');
SQL
kc wait --for=condition=Established crd/widgets.acceptance.ksql.example.com --timeout=60s
kc api-resources --api-group=acceptance.ksql.example.com --cached=false
```

看到 widgets、APIVERSION acceptance.ksql.example.com/v1、NAMESPACED true 才继续。Established 与 Discovery 可见不是同一件事，若仍未出现在 api-resources，等待后重查；不要立即认定 SQL 错误。

### 8.2 精确表名创建与查询 CR

```sh
ks <<'SQL'
INSERT INTO "acceptance.ksql.example.com/v1/widgets" (manifest) VALUES ('{"apiVersion":"acceptance.ksql.example.com/v1","kind":"Widget","metadata":{"name":"sample"},"spec":{"size":9007199254740993,"optional":"plain"}}');
SQL
ks <<'SQL'
SELECT name, namespace, "/spec/size" AS size FROM "acceptance.ksql.example.com/v1/widgets" WHERE "/spec/size" > 9007199254740992;
SQL
kc -n "$DEMO_NS" get widgets.acceptance.ksql.example.com sample -o json
ks <<'SQL'
UPDATE "acceptance.ksql.example.com/v1/widgets" SET "/spec/size" = CAST('9007199254740997' AS JSON) WHERE name = 'sample';
SQL
ks <<'SQL'
SELECT "/spec/size" AS size FROM "acceptance.ksql.example.com/v1/widgets" WHERE name = 'sample';
SQL
```

期待整数逐位保留，不被 double 的 2^53 边界舍入。不要用把数字转 float64 的外部显示工具作为精度证据，直接看 API/CLI 原始 JSON。

讲解重点：没有给 Widget 新增 Go struct、没有重编译，Discovery 取得 GVR/Kind/scope/verbs 后，dynamic client 即可操作 unstructured 对象。

### 8.3 JSON null 与 SQL NULL 的真实区别

```sh
ks <<'SQL'
UPDATE "acceptance.ksql.example.com/v1/widgets" SET "/spec/optional" = CAST('null' AS JSON) WHERE name = 'sample';
SQL
kc -n "$DEMO_NS" get widgets.acceptance.ksql.example.com sample -o json
ks <<'SQL'
UPDATE "acceptance.ksql.example.com/v1/widgets" SET "/spec/optional" = NULL WHERE name = 'sample';
SQL
kc -n "$DEMO_NS" get widgets.acceptance.ksql.example.com sample -o json
```

第一次 spec 中仍有 `"optional": null`，第二次键消失。两次 SELECT Pointer 都可能显示 null，所以要用 kubectl 完整 JSON 区分键是否存在。

## 9. 手工演示四：实际 Metrics 与 kubectl top

### 9.1 创建双容器 Pod

```sh
ks <<'SQL'
INSERT INTO pods (manifest) VALUES ('{"apiVersion":"v1","kind":"Pod","metadata":{"name":"measure"},"spec":{"containers":[{"name":"one","image":"busybox:1.36","command":["sh","-c","sleep 3600"]},{"name":"two","image":"busybox:1.36","command":["sh","-c","sleep 3600"]}]}}');
SQL
kc -n "$DEMO_NS" wait --for=condition=Ready pod/measure --timeout=180s
kc -n "$DEMO_NS" get pod measure
kc -n "$DEMO_NS" top pod measure --containers
```

如果 Ready 但 top 提示 metrics not available，等待 15–30 秒再试，最多预留约 180 秒采样。真实空指标不是假数据，也不填 0。

### 9.2 KubeSQL 查询与原始 API 对照

```sh
ks <<'SQL'
SELECT name, namespace, cpu_millicores, memory_bytes FROM pod_metrics WHERE name = 'measure';
SQL
ks <<'SQL'
SELECT name, cpu_millicores, memory_bytes FROM node_metrics;
SQL
kc -n "$DEMO_NS" top pod measure
kc top nodes
kc get --raw /apis/metrics.k8s.io/v1beta1/namespaces/ksql-demo-accept/pods/measure
```

说明：Pod 每行是两个容器 usage 的总和，node_metrics 没有 namespace 列。1000 millicores=1 CPU 核，1 MiB=1048576 bytes。kubectl top 常显示 Mi，KubeSQL 显示 bytes；采样时刻/取整可能不同，不要求连续请求数值逐字相等。

读取来自 metrics.k8s.io，不是 spec.resources.requests/limits。代码使用 Quantity 累加后转换，避免先逐容器取整导致偏差。

## 10. 安全与错误：失败是需要展示的设计

这些都是**预期非零退出码**；与“现场编译没有 Error”不矛盾。逐条执行并马上查看退出码。

### 10.1 语法错误（无需可用集群）

```sh
ks <<'SQL'
SELECT name, FROM namespaces;
SQL
echo "exit=$?"
```

期待 E_PARSE、行列位置、退出码 2。这也能说明 Parse 在连接集群前发生。

### 10.2 缺 WHERE 与跨命名空间写入

```sh
ks <<'SQL'
UPDATE deployments SET replicas = 0;
SQL
echo "exit=$?"
ks --all-namespaces <<'SQL'
DELETE FROM deployments WHERE name = 'web';
SQL
echo "exit=$?"
```

分别期待 E_WHERE_REQUIRED 和 E_NAMESPACE，退出码 2，无实际写入。**WHERE TRUE 仍能匹配全部候选，保护是安全边界而非绝对防误操作。**

### 10.3 写保护与只读表

```sh
ks <<'SQL'
UPDATE deployments SET "/metadata/name" = 'renamed' WHERE name = 'web';
SQL
echo "exit=$?"
ks <<'SQL'
UPDATE pod_metrics SET cpu_millicores = 0 WHERE name = 'measure';
SQL
echo "exit=$?"
```

期待拒绝身份修改和 E_READ_ONLY。禁止根对象、status、服务器管理元数据及其祖先路径；不能用整体 metadata 绕过限制。

### 10.4 NULL 三值逻辑

任意资源上缺失的 Pointer 读取为 null，可用同一对象证明：

```sh
ks <<'SQL'
SELECT name FROM deployments WHERE "/spec/noSuchField" IS NULL;
SQL
ks <<'SQL'
SELECT name FROM deployments WHERE "/spec/noSuchField" = NULL;
SQL
ks <<'SQL'
SELECT name FROM deployments WHERE NOT ("/spec/noSuchField" = NULL);
SQL
```

期待第一条有 web，后二条 `[]`。`NULL = NULL` 是 UNKNOWN，NOT UNKNOWN 仍 UNKNOWN；WHERE 只保留 TRUE。

## 11. 测试样例：完整复现任务书验收

### 11.1 离线测试

```sh
go test -count=1 -v ./...
go test -race -count=1 ./...
```

默认测试不连接真实集群；HTTP 测试模拟 API 协议，用于稳定制造 Forbidden/Conflict/部分 Discovery 失败等场景。

单独展示第一章 AST 和错误位置：

```sh
go test -count=1 -v ./internal/sql -run 'TestParseSelectAST|TestParseErrorJSON'
```

打开 `internal/sql/parser_test.go` 展示期望 AST 与行列断言。CLI 直接执行 SQL，不是 AST 打印工具；不要现场使用不存在的 `--ast` 参数。

### 11.2 真实 E2E：只使用显式测试连接

```sh
export KSQL_E2E_KUBECONFIG="$PWD/tmp/kube/config"
export KSQL_E2E_CONTEXT=kubesql-test
export KSQL_E2E_BINARY="$PWD/tmp/bin/ksql"
go test -tags=e2e -count=1 -v ./test/e2e
```

必须先 build；二进制路径用绝对路径。`-count=1` 禁用测试结果缓存。缺测试配置会失败，不默默跳过，也不连接默认集群。

保存证据（不使用会掩盖退出码的裸 `| tee`）：

```sh
go test -tags=e2e -count=1 -v ./test/e2e > tmp/acceptance/e2e.log 2>&1
result=$?
cat tmp/acceptance/e2e.log
echo "e2e exit=$result"
```

上一次 M9 全套实测通过耗时约 206 秒，仅供排期，现场以实际输出为准。Metrics 等待是主要耗时之一。

### 11.3 按任务章节单独演示

| 章节 | 核心要求 | 对应测试 |
| --- | --- | --- |
| 1 | 逐字符扫描、递归下降、AST、定位 | TestParseSelectAST / TestParseErrorJSON |
| 2 | Namespace、Deployment、Ingress 查询 | TestEasySelect |
| 3 | WHERE、优先级、NULL | TestEasyWhere |
| 4 | 条件 UPDATE/DELETE、精确修改 | TestMediumUpdate / TestMediumDelete |
| 5 | manifest 创建、同名拒绝 | TestMediumInsertNamespaceDeployment / TestMediumInsertIngressAlreadyExists |
| 6 | Discovery、通用 CRUD、Pointer/AS/CAST | TestHardNative |
| 7 | 真正 CPU/内存、双容器汇总 | TestHardMetrics |
| 8 | CRD/陌生 CR、scope、版本和约束 | TestHardCRDNamespaced / TestHardCRDCluster |

例如只跑章节 6 或 8（仍用上面的测试环境变量）：

```sh
go test -tags=e2e -count=1 -v ./test/e2e -run '^TestHardNative$'
go test -tags=e2e -count=1 -v ./test/e2e -run '^TestHardCRD'
```

Fixture 在 `testdata/`：准备资源、SQL、期望结果分别存放。**不要先把所有 YAML apply 到集群再启动 E2E**：测试拒绝覆盖预存在的对象，且 INSERT 用例目标必须由 CLI 创建。

### 11.4 结果为什么不是与 PDF 每个字符相同

- 最终 SELECT * 多了后续任务要求的 labels/annotations/manifest 等列；早期章节只核对其阶段列，另测完整 manifest。
- Kubernetes 加入 revision 注解、Namespace 标准标签、kube-root-ca.crt ConfigMap；产品保留真实对象，测试只明确处理已知系统项。
- INSERT 比较明确字段，不比较服务器时间戳和默认值。
- 指标是当时实际 usage，不必等于 PDF 固定样本；固定 Quantity 样本由离线测试验证。
- 普通资源路由覆盖可以返回空集合，不代表每类工作负载都已创建并 Ready。

## 12. 代码核心思想与讲解路线

### 12.1 总体分层：SQL 与 Kubernetes 分离

```text
cmd/ksql/main.go：信号 context、标准输入输出、进程退出码
          ↓
internal/cli/run.go：flags → stdin → Parse → Preflight
          ↓                              ↓
internal/sql：AST                  kube.Connect + Discovery
                                         ↓
internal/resource.Descriptor：GVR / Kind / Namespaced / Verbs
                                         ↓
internal/engine：Bind → 类型/列/安全规则 → WHERE → 投影或写入计划
                                         ↓
internal/kube：官方 dynamic client → API Server → JSON 结果
```

SQL 包不依赖客户端，不负责访问集群；engine 的 Query 不持有 client，通过小接口注入后端；kube 不重新解析 SQL；CLI 负责组装和错误输出。这样解析、语义和协议可分别测试。

Metrics 走单独的只读绑定与读取路径，不假装它是可更新的普通 Kubernetes 表。

### 12.2 Lexer：逐字符扫描而不是拆字符串

打开 `internal/sql/lexer.go`：

1. lexer 保存输入与 Position，使用 utf8.DecodeRuneInString 读取字符。
2. advance 维护 offset/line/column；空白和换行被消费，但不丢掉位置。
3. 按字符分支识别标识符、数字、字符串、双引号标识符、符号和运算符。
4. 扫描完整标识符之后查 keywords map，未引用名称规范化；不是在原 SQL 上切关键词片段。
5. SQL 字符串用两个单引号转义；双引号标识符保留内容。

可用例子解释为何不能按空格切分：`WHERE name='hello world'` 的字符串含空格，`replicas>=3` 的运算符不需要空格。正则拼接也难以正确处理引号、位置和嵌套表达式。

### 12.3 Parser：递归下降体现运算优先级

打开 `internal/sql/parser.go`、`expression_parser.go`、`ast.go`：

```text
Parse → statement → optional semicolon → EOF

parseOr
  └─ parseAnd
       └─ parseNot
            └─ parsePredicate
                 └─ parsePrimary / 括号内再次 parseOr
```

该层级使 AND 比 OR 绑定更紧、比较比 NOT 更紧。括号显式递归，NOT 可以递归嵌套。INSERT/UPDATE/DELETE 各自语法受限，不借助通用数据库 parser。

要求 EOF 可拒绝多个语句和尾随垃圾。AST 保留表达式结构与源位置，后续绑定不需要重新扫描 SQL。

### 12.4 绑定：先验证整条语句，再执行

打开 `internal/engine/preflight.go`、`query.go`、`expression.go`：

- Preflight 对不依赖 Discovery 的语法后语义/安全问题尽早拒绝。
- Discovery 返回 Descriptor 后，Bind 解析实际表、列、Pointer、输出键与 WHERE 类型。
- 不知道的列不能因为结果为空而偷偷通过；验证不依赖有没有对象。
- 通用表公开 name/namespace/labels/annotations/manifest，三表另有便捷列。
- AS 仅定义输出键；WHERE 引用源字段，不引用 AS 名称；重复输出键拒绝。
- dynamic Pointer 类型由运行时对象确定，不能假称所有字段都已从 OpenAPI 推导强类型。

说明两个阶段：表/scope/verbs 需要 Discovery，但缺 WHERE 等可更早拒绝；不是“所有校验都完全离线”。

### 12.5 WHERE：精确数字与三值逻辑

打开 `internal/engine/value.go`、`expression.go`：

- valueKind 区分 string/number/bool/null/object/dynamic；不把数字和字符串隐式混用。
- 数值转换为 big.Rat 做比较，避免整数比较统一转 float64。
- truth 包含 trueTruth/falseTruth/unknown，NULL 或缺失值导致 UNKNOWN。
- AND/OR/NOT 按 SQL 三值语义执行；最终只留下 TRUE。
- 对象/数组不能用普通标量比较；IS NULL 用于空值判断。

答辩时可以写小表：

| 表达式 | 值 |
| --- | --- |
| NULL = 1 | UNKNOWN |
| NOT UNKNOWN | UNKNOWN |
| FALSE AND UNKNOWN | FALSE |
| TRUE OR UNKNOWN | TRUE |

查询过程：官方 client 分页 List → 客户端 WHERE → 投影 JSON。这里没有把任意 WHERE 偷换成 Kubernetes labelSelector，也不是服务端 SQL。

### 12.6 Discovery：从资源名字找到真实 API

打开 `internal/kube/discovery.go`、`internal/resource/descriptor.go`：

GVR = Group/Version/Resource；Kind 是对象类型，不能把 Kind 与复数资源名混为一谈。

```text
"apps/v1/deployments"
    group=apps, version=v1, resource=deployments, kind=Deployment
```

- 使用 Discovery 的资源复数名、scope 和 verbs，不猜短名。
- 简单表名跨组歧义时拒绝；同组优先 preferred version，必要时按发现版本顺序回退。
- 双引号精确版本绝不回退，避免用户请求一个版本却实际改了另一个版本。
- 子资源不纳入普通 CRUD；不把 deployments/status 当常规表。
- 每条语句使用自身发现快照，CRD 注册后下一次调用可发现新资源。
- 部分 API 组失败时保留失败信息；精确健康表可用，不能在不完整信息下随意猜裸表名。

Discovery verbs 是服务端能力，不是 RBAC 授权。真正发请求仍可能 Forbidden。

### 12.7 写操作：精确 JSON Patch，不重建对象

打开 `internal/engine/write.go`、`pointer.go`、`internal/kube/client.go`：

1. BindWrite 检查 WHERE、namespace、赋值类型、保护路径与路径重叠。
2. 完整读取候选并评估 WHERE，再逐对象写入；不是读取一页立刻改一页。
3. patch 第一条 test resourceVersion，随后对指定路径 add/replace/remove。
4. 父路径必须存在，数组只替换已有索引；SQL NULL 生成 remove。
5. API 成功计 affected_rows；失败记安全 reason 并继续后续对象，最终非零退出。
6. DELETE 请求带 UID/resourceVersion Preconditions，降低删错重建对象的风险。

示意，不是硬编码的实际版本：

```json
[
  {"op":"test","path":"/metadata/resourceVersion","value":"12345"},
  {"op":"replace","path":"/spec/replicas","value":3}
]
```

只修改一个叶子，保留未知字段和 sibling。UPDATE 不允许写 manifest，因为整体覆盖会破坏这种安全边界。

没有事务/回滚，不保证整条 SQL 多对象原子性；单对象 JSON Patch 的校验与修改交给 API Server。无自动冲突重试，失败后应重查。

### 12.8 INSERT：SQL 字符串与 JSON 两层处理

打开 `internal/sql/insert_parser.go`、`internal/engine/insert.go`、`internal/engine/json.go`：

- SQL parser 先取出一个 manifest 字符串；JSON Decoder 再解析对象，不能在 JSON 内查找 SQL 关键词。
- UseNumber 保留数字文本；整数字段转换到 int64，溢出拒绝；非整数按 float64 表示。
- 校验 apiVersion/kind 与 Descriptor，处理 namespace，拒绝 status 和服务器字段。
- 持久化对象要求 name、拒绝 generateName；没有 get/list 的 create-only 请求资源允许无 name。
- 最终只调用 Create，同名 AlreadyExists，不 patch、不删后重建。

外部 JSON schema/不可变字段约束仍由 API Server 执行，产品没有假装实现整个 Kubernetes schema validator。

### 12.9 Metrics：Quantity 与真实聚合

打开 `internal/engine/metrics.go`、`internal/kube/metrics.go`：

访问 metrics.k8s.io/v1beta1 的 pods/nodes；每个容器 usage.cpu/memory 使用 resource.ParseQuantity；Pod 先 Add 全部容器，再 MilliValue/Value，检查非负与转换范围。

例如两容器各 0.4m，先逐个转 millicores 再相加可能与先加 0.8m 再转换不同，因此汇总顺序有意义。不能用字符串拼接或仅剥掉 m/Mi 单位。

缺 Metrics API 报 E_METRICS_UNAVAILABLE；Forbidden 保留权限语义；无样本输出空数组，不从 Pod requests 补“使用量”。

### 12.10 错误、取消与测试可用性

打开 `cmd/ksql/main.go`、`internal/cli/run.go`：

主函数用 signal.NotifyContext 接受 Ctrl+C/SIGTERM；CLI 给 API 执行 30 秒上限；context 传到官方客户端。超时只表示客户端结果不确定，不能证明服务端没写入。

stdout 专用于结果，stderr 用于整语句错误；对象级失败在汇总中。API 原始错误可能含敏感字段，因此只输出身份和安全 reason，不回显完整 patch/manifest。

测试小接口与 HTTP 服务能注入可重复失败；真实 E2E 再证明不是仅在 fake client 上通过。

## 13. 常见答辩问答

**Q：为什么不直接用 kubectl？**  
A：kubectl 是对照客户端。本项目提供 SQL 语法、统一投影/三值逻辑和安全写入层，直接使用官方 Go 客户端，不解析 kubectl 文本输出。

**Q：为什么手写 parser？**  
A：任务明确要求逐字符 lexer 和递归下降，支持的 SQL 子集很小；分层函数直观表达优先级，也方便精确错误定位与 AST 测试。

**Q：为何不支持任意 SQL？**  
A：目标是 Kubernetes 资源操作，不是完整数据库；JOIN、事务、ORDER BY、聚合、多语句、批量 INSERT、子资源与流式 API 不在已交付范围。

**Q：新 CRD 为什么不用改代码？**  
A：Discovery 得到 GVR/Kind/scope/verbs，dynamic client 使用 unstructured；Pointer 访问字段。服务器 schema 负责校验。

**Q：有 WHERE 就绝对安全吗？**  
A：不是。WHERE TRUE 仍选中全部；必须先 SELECT 确认、限制 namespace、使用低权限凭据。工具额外阻止身份/状态写入和跨 namespace 写入。

**Q：两个对象，一个失败怎么办？**  
A：另一个仍可成功，返回 affected_rows/failed_rows/errors，退出 1；不回滚，无跨对象原子性。

**Q：resourceVersion 是否就是事务锁？**  
A：不是跨对象锁。对 UPDATE 是 patch test，对 DELETE 是前置条件，用于检测读取后对象版本变化；没有自动重试。

**Q：NULL 和 JSON null 为什么不同？**  
A：UPDATE 中 SQL NULL 明确表示 remove，CAST JSON null 是写 null；读取时二者都可投影为 null，需要完整 API 对象区分键存在性。

**Q：为什么 SELECT * 与早期 PDF 例子多字段？**  
A：后续任务扩展了公开列。最终阶段遵循扩展后的语义，测试分别验证早期核心字段和最终完整字段。

**Q：Metrics 与 top 不完全一样怎么办？**  
A：单位、取整和采样时刻不同。用原始 Metrics API 验证容器 usage，确认先加 Quantity 后转换，不要求两次请求字节级一致。

**Q：你证明了所有 API 都没 bug 吗？**  
A：没有。证明了规定用例、明确边界和所列集群上的实测；任意版本/schema/准入策略未全面覆盖，也未交付全面 fuzz/CI。

**Q：Review 创建成功就是 allowed=true 吗？**  
A：不是。当前 INSERT 汇总仅说明请求接受，不输出 Review 的 allowed 决策。create-only 无名资源已验证，但不是权限决策展示接口。

## 14. 现场故障处理

| 现象 | 优先检查 | 讲解时不要误说 |
| --- | --- | --- |
| docker info 失败 | daemon/WSL Docker 连接、权限 | 不先认定是 SQL 故障 |
| connection refused | minikube status、显式 kubeconfig/context、API 地址 | 不切到其他未知集群凑结果 |
| ImagePullBackOff | describe pod、事件、镜像加载/网络 | 查询期望 replicas 不代表 Pod Ready |
| Metrics 空或 unavailable | APIService、metrics-server 日志、采样等待 | 不把 requests/limits 伪装成指标 |
| CRD Established 但表不存在 | Discovery 再查目标组/版本 | 不把注册时序当 parser bug |
| E_AMBIGUOUS_TABLE | 改精确 group/version/resource | 不任意选一组 |
| Forbidden | RBAC/凭据/目标 namespace | Discovery verb 不代表用户权限 |
| Invalid | schema、类型、不可变字段 | 不承诺本地校验覆盖全部 schema |
| Conflict/版本前置条件失败 | 重查实际对象再决定重试 | 不强制重建或自动覆盖 |
| E2E 说资源已存在 | 上次中断残留/提前 apply/所有权 | 不全局删除 CRD/Namespace |
| staticcheck 工具链失败 | 工具构建版本与 Go export-data 兼容 | 不隐藏报错后声称通过 |

常用排查命令：

```sh
kc -n "$DEMO_NS" get pods -o wide
kc -n "$DEMO_NS" describe pod measure
kc -n "$DEMO_NS" get events --sort-by=.metadata.creationTimestamp
kc -n kube-system logs deployment/metrics-server --tail=80
```

## 15. 演示结束：只清理自己创建的资源

确认本节名字是本次创建后，依次删除实例、定义和演示 Namespace：

```sh
ks <<'SQL'
DELETE FROM "acceptance.ksql.example.com/v1/widgets" WHERE name = 'sample';
SQL
ks <<'SQL'
DELETE FROM "apiextensions.k8s.io/v1/customresourcedefinitions" WHERE name = 'widgets.acceptance.ksql.example.com';
SQL
ks <<'SQL'
DELETE FROM namespaces WHERE name = 'ksql-demo-accept';
SQL
kc wait --for=delete namespace/ksql-demo-accept --timeout=180s
kc get namespaces
kc get crds
```

只执行已创建部分的清理；如果 CRD 演示没做，不必运行前两条。删除 Namespace 会连带删除其中资源，此动作不可回滚。

E2E 正常结束/普通失败会清理自己的资源；进程强制终止或 API 中断可能留下残留，先核实所有权和 UID 再处理。不要删除系统 Namespace、预存 k8s-lab 或任何其他用户资源。

## 16. 最后一分钟复习卡

```text
环境：MINIKUBE_HOME + KUBECONFIG → minikube status → kc get nodes
构建：go build -o tmp/bin/ksql ./cmd/ksql → go vet → staticcheck
用法：stdin 一条 SQL；不是 -e；默认 JSON；显式 context/namespace
查询：SELECT → List 分页 → 客户端 WHERE → JSON 投影
解析：逐字符 lexer → 递归下降 AST → EOF → bind
通用：Discovery GVR/Kind/scope/verbs → dynamic/unstructured
更新：WHERE + 单 namespace + protected paths + resourceVersion test
删除：UID/resourceVersion Preconditions；不清 finalizer
创建：单 JSON manifest；Create only；AlreadyExists 不覆盖
空值：WHERE UNKNOWN 不保留；SQL NULL remove；CAST null 写 JSON null
指标：真实 Metrics API；Quantity 先汇总后转换；不拿 requests 替代
证据：单测 + HTTP 故障 + 真集群 E2E + kubectl 双向对照
边界：无事务/JOIN/批量/子资源；别夸大任意版本兼容
```

本手册在被忽略的 docs/ 中，默认只保留本机。可提交的产品和测试入口分别是根 `README.md` 与 `test/README.md`；不要认为本文已经随 Git 推送给验收方。
