#!/usr/bin/env bash
# 兼容旧入口。不再运行旧的建集群流程。
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
exec bash "$root/tools/minikube-launch.sh"
