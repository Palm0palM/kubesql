# 现场辅助文件

从仓库根目录运行，不需 chmod，也不需修改全局配置：

```sh
bash docs/acceptance/build.sh
source docs/acceptance/env.sh
ksql --help
bash docs/acceptance/cluster.sh
```

已有专用 profile 时加 `--reuse`；需要指标插件时加 `--metrics`。脚本从不删除 profile，也不覆盖演示 Namespace。

- build.sh：本地缓存/临时目录、实际 build + vet；`--checks` 增加 staticcheck 与单测。
- env.sh：将本次二进制加入 PATH，设置独立 kubeconfig 与真实 E2E 配置；只影响当前 shell。
- cluster.sh：Docker driver 启动 kubesql-test、等待 Node Ready；可选安装 Metrics Server。
- samples.sh：按任务章节挑选现有自动测试，失败即返回非零；不会预先 apply fixture。
- metrics-pod.yaml：仅属于手工演示 ksql-live，不与 E2E 混用。
- live-crd.yaml：仅属于手工演示 live.ksql.example.com；演示陌生类型、nullable 字段与 int64，不占用自动测试的 lab.example.com。

演示产物在 tmp/live/；Go 缓存沿用 tmp/go-*，Minikube 数据沿用 tmp/minikube。现有 tmp/kube/config 不被覆盖。

完整操作、讲解与答疑顺序见上级 `KubeSQL验收手册.md`；按 PDF 手工使用所有夹具见上级 `任务书测试样例操作手册.md`。

本目录随 docs/ 被 Git 忽略，仅本机保存。不要将这里的脚本误认为已经推送给验收方。
