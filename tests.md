cat tools/env-config.sh
source tools/env-config.sh
minikube start -p kubesql-test --kubernetes-version=v1.35.0 --base-image=gcr.io/k8s-minikube/kicbase:v0.0.51 --preload=false --cache-images=false
minikube status -p kubesql-test
kubectl wait --for=condition=Ready pod --all -n kube-system --timeout=180s
kubectl config current-context
kubectl get nodes
kubectl get pods -n kube-system

go version
go build -o ksql ./cmd/ksql
./ksql --help
go vet ./...
staticcheck ./...

cat tools/fixtures/02-select.yaml
kubectl create -f tools/fixtures/02-select.yaml
echo 'SELECT name FROM namespaces;' | ./ksql --namespace sql-easy-select
echo 'SELECT name, namespace, replicas FROM deployments;' | ./ksql --namespace sql-easy-select
echo 'SELECT * FROM ingresses;' | ./ksql --namespace sql-easy-select
echo 'SELECT name, namespace, default_backend_service FROM ingresses;' | ./ksql --namespace sql-easy-select
kubectl get deployment web -n sql-easy-select -o yaml
kubectl get ingress web -n sql-easy-select -o yaml

cat tools/fixtures/03-where.yaml
kubectl create -f tools/fixtures/03-where.yaml
echo "SELECT name FROM deployments WHERE name = 'web' OR name = 'worker' AND replicas >= 3;" | ./ksql --namespace sql-easy-where
echo "SELECT name FROM deployments WHERE (name = 'web' OR name = 'worker') AND replicas >= 3;" | ./ksql --namespace sql-easy-where
echo 'SELECT name FROM ingresses WHERE default_backend_service IS NULL;' | ./ksql --namespace sql-easy-where
echo 'SELECT name FROM ingresses WHERE default_backend_service = NULL;' | ./ksql --namespace sql-easy-where

cat tools/fixtures/04-all.yaml
kubectl create -f tools/fixtures/04-all.yaml
echo "UPDATE deployments SET replicas = 2 WHERE name = 'web';" | ./ksql --namespace sql-medium-write
echo "UPDATE namespaces SET annotations = '{\"owner\":\"alice\"}' WHERE name = 'sql-medium-write';" | ./ksql --namespace sql-medium-write
echo "UPDATE ingresses SET default_backend_service = 'web-v2' WHERE name = 'temporary';" | ./ksql --namespace sql-medium-write
echo "SELECT name, replicas, annotations FROM deployments WHERE name = 'web';" | ./ksql --namespace sql-medium-write
echo "SELECT name, annotations FROM namespaces WHERE name = 'sql-medium-write';" | ./ksql --namespace sql-medium-write
echo "SELECT name, default_backend_service FROM ingresses WHERE name = 'temporary';" | ./ksql --namespace sql-medium-write
kubectl get deployment web -n sql-medium-write -o yaml
kubectl get ingress temporary -n sql-medium-write -o yaml

echo "UPDATE deployments SET replicas = 0 WHERE name = 'web';" | ./ksql --namespace sql-medium-write
echo "UPDATE namespaces SET annotations = '{\"owner\":\"before\"}' WHERE name = 'sql-medium-write';" | ./ksql --namespace sql-medium-write
echo "UPDATE ingresses SET default_backend_service = 'web' WHERE name = 'temporary';" | ./ksql --namespace sql-medium-write
echo "DELETE FROM ingresses WHERE name = 'temporary';" | ./ksql --namespace sql-medium-write
echo "DELETE FROM deployments WHERE name = 'web';" | ./ksql --namespace sql-medium-write
echo "DELETE FROM namespaces WHERE name = 'sql-medium-delete';" | ./ksql --namespace sql-medium-write
kubectl wait --for=delete ingress/temporary -n sql-medium-write --timeout=120s
kubectl wait --for=delete deployment/web -n sql-medium-write --timeout=120s
kubectl wait --for=delete ns/sql-medium-delete --timeout=120s
echo "SELECT name FROM ingresses WHERE name = 'temporary';" | ./ksql --namespace sql-medium-write
echo "SELECT name FROM deployments WHERE name = 'web';" | ./ksql --namespace sql-medium-write
echo "SELECT name FROM namespaces WHERE name = 'sql-medium-delete';" | ./ksql --namespace sql-medium-write

cat tools/sql/05-namespace.sql
./ksql < tools/sql/05-namespace.sql
kubectl wait --for=jsonpath='{.status.phase}'=Active ns/sql-medium-insert --timeout=60s
cat tools/sql/05-deployment.sql
./ksql --namespace sql-medium-insert < tools/sql/05-deployment.sql
echo "SELECT name, namespace, replicas FROM deployments WHERE name = 'api';" | ./ksql --namespace sql-medium-insert
kubectl get deployment api -n sql-medium-insert -o yaml

cat tools/fixtures/05-ingress-prepare.yaml
kubectl create -f tools/fixtures/05-ingress-prepare.yaml
cat tools/sql/05-ingress.sql
./ksql --namespace sql-medium-insert-ingress < tools/sql/05-ingress.sql
cat tools/sql/05-ingress.sql
./ksql --namespace sql-medium-insert-ingress < tools/sql/05-ingress.sql
echo "exit=$?"
kubectl get ingress -n sql-medium-insert-ingress

cat tools/fixtures/06-native.yaml
kubectl create -f tools/fixtures/06-native.yaml
echo 'SELECT name FROM replicasets;' | ./ksql --namespace sql-hard-native
echo 'SELECT name, "/spec/replicas" AS replicas FROM "apps/v1/statefulsets";' | ./ksql --namespace sql-hard-native
echo 'SELECT name, "/data/username" AS username FROM secrets;' | ./ksql --namespace sql-hard-native
echo 'SELECT name, "/spec/resources/requests/storage" AS storage FROM persistentvolumeclaims;' | ./ksql --namespace sql-hard-native
echo 'SELECT name, "/data/mode" AS mode FROM configmaps;' | ./ksql --namespace sql-hard-native
echo "UPDATE configmaps SET \"/data/mode\" = 'prod' WHERE name = 'settings';" | ./ksql --namespace sql-hard-native
echo "SELECT name, \"/data/mode\" AS mode FROM configmaps WHERE name = 'settings';" | ./ksql --namespace sql-hard-native
kubectl get configmap settings -n sql-hard-native -o yaml

cat tools/fixtures/07-metrics.yaml
kubectl create -f tools/fixtures/07-metrics.yaml
kubectl wait --for=condition=Ready pod/measure -n sql-hard-metrics --timeout=180s
kubectl top pod measure -n sql-hard-metrics --containers
echo "SELECT name, cpu_millicores, memory_bytes FROM pod_metrics WHERE name = 'measure';" | ./ksql --namespace sql-hard-metrics
echo "SELECT name, cpu_millicores, memory_bytes FROM node_metrics WHERE name = 'kubesql-test';" | ./ksql
kubectl top nodes

cat tools/fixtures/08-gadgets-namespace.yaml
kubectl create -f tools/fixtures/08-gadgets-namespace.yaml
cat tools/fixtures/08-gadgets-crd.yaml
kubectl create -f tools/fixtures/08-gadgets-crd.yaml
kubectl wait --for=condition=Established crd/gadgets.lab.example.com --timeout=60s
kubectl api-resources --api-group=lab.example.com --cached=false
cat tools/fixtures/08-gadgets-sample.yaml
kubectl create -f tools/fixtures/08-gadgets-sample.yaml
echo 'SELECT name, "/spec/size" AS size FROM "lab.example.com/v1/gadgets" WHERE "/spec/size" >= 2;' | ./ksql --namespace sql-hard-crd
echo "UPDATE \"lab.example.com/v1/gadgets\" SET \"/spec/size\" = 3 WHERE name = 'sample';" | ./ksql --namespace sql-hard-crd
echo "SELECT name, \"/spec/size\" AS size, \"/spec/message\" AS message FROM \"lab.example.com/v1/gadgets\" WHERE name = 'sample';" | ./ksql --namespace sql-hard-crd
cat tools/sql/08-insert.sql
./ksql --namespace sql-hard-crd < tools/sql/08-insert.sql
cat tools/sql/08-delete.sql
./ksql --namespace sql-hard-crd < tools/sql/08-delete.sql
kubectl wait --for=delete gadgets.lab.example.com/extra -n sql-hard-crd --timeout=120s
echo 'SELECT name, "/spec/size" AS size FROM "lab.example.com/v1/gadgets";' | ./ksql --namespace sql-hard-crd

cat tools/fixtures/08-clusternotes-crd.yaml
kubectl create -f tools/fixtures/08-clusternotes-crd.yaml
kubectl wait --for=condition=Established crd/clusternotes.lab.example.com --timeout=60s
kubectl api-resources --api-group=lab.example.com --cached=false
cat tools/fixtures/08-clusternotes-sample.yaml
kubectl create -f tools/fixtures/08-clusternotes-sample.yaml
echo "SELECT name, namespace, \"/spec/owner\" AS owner FROM \"lab.example.com/v1/clusternotes\" WHERE name = 'cluster-note';" | ./ksql --namespace sql-hard-crd
echo "SELECT name FROM \"apiextensions.k8s.io/v1/customresourcedefinitions\" WHERE name = 'clusternotes.lab.example.com';" | ./ksql --namespace sql-hard-crd

go test -count=1 -v ./internal/sql -run 'TestParseSelectAST|TestParseErrorJSON'
kubectl delete ns sql-easy-where --wait=true --timeout=180s
go test -tags=e2e -count=1 -v ./test/e2e -run '^TestEasyWhere$'

minikube stop -p kubesql-test
