#!/usr/bin/env bash
export PATH="$PWD/tools:$PATH"
mkdir -p kubesql-test/kube kubesql-test/bin kubesql-test/go-cache kubesql-test/go-tmp kubesql-test/go-mod-cache kubesql-test/samples
export MINIKUBE_HOME="$PWD/kubesql-test/minikube"
export KUBECONFIG="$PWD/kubesql-test/kube/config"
export GOEXPERIMENT=
export GOTOOLCHAIN=local
export GOCACHE="$PWD/kubesql-test/go-cache"
export GOTMPDIR="$PWD/kubesql-test/go-tmp"
export GOMODCACHE="$PWD/kubesql-test/go-mod-cache"
export TMPDIR="$PWD/kubesql-test/go-tmp"

minikube start -p kubesql-test --driver=docker --kubernetes-version=v1.35.0 --cpus=2 --memory=3072
chmod 600 "$KUBECONFIG"
minikube status -p kubesql-test
