//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestHardNative(t *testing.T) {
	ctx, binary, args := prepareFixture(t, "hard-native", "sql-hard-native")
	for _, name := range []string{"replicasets", "statefulsets", "secrets", "persistentvolumeclaims", "configmaps"} {
		t.Run(name, func(t *testing.T) {
			if name == "configmaps" {
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Stdin = bytes.NewReader(readFixture(t, "hard-native", name+".sql"))
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil || stderr.Len() != 0 {
					t.Fatalf("query failed: %v, stderr=%s", err, &stderr)
				}
				got := canonicalRows(t, stdout.Bytes())
				// Kubernetes independently publishes this system ConfigMap into
				// every Namespace. Keep it in CLI output; exclude only the known
				// extra row from the task's fixture-object comparison.
				got = slices.DeleteFunc(got, func(row string) bool { return row == `{"mode":null,"name":"kube-root-ca.crt"}` })
				if want := canonicalRows(t, readFixture(t, "hard-native", name+".json")); !reflect.DeepEqual(got, want) {
					t.Fatalf("rows=%v, want=%v", got, want)
				}
				return
			}
			executeSQL(t, ctx, binary, args, string(readFixture(t, "hard-native", name+".sql")), string(readFixture(t, "hard-native", name+".json")))
		})
	}
	executeSQL(t, ctx, binary, args, `UPDATE configmaps SET "/data/mode" = 'prod' WHERE name = 'settings'`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT name, "/data/mode" AS mode FROM configmaps WHERE name = 'settings'`, `[{"name":"settings","mode":"prod"}]`)
	// Existing parents allow adding leaves; pointer escapes address literal keys.
	executeSQL(t, ctx, binary, args, `UPDATE "v1/configmaps" SET "/metadata/annotations/test.example.com~1value" = 'slash', "/data/new-key" = 'new' WHERE name = 'settings'`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT "/metadata/annotations/test.example.com~1value" AS slash, "/data/new-key" AS added FROM configmaps WHERE name = 'settings'`, `[{"slash":"slash","added":"new"}]`)
	executeSQL(t, ctx, binary, args, `UPDATE configmaps SET "/data" = CAST('{"mode":"prod","region":"test"}' AS JSON) WHERE name = 'settings'`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT "/data" AS data FROM configmaps WHERE name = 'settings'`, `[{"data":{"mode":"prod","region":"test"}}]`)
	executeSQL(t, ctx, binary, args, `UPDATE configmaps SET "/data/region" = NULL WHERE name = 'settings'`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT name, "/data/region" AS region FROM configmaps WHERE name = 'settings' AND "/data/region" IS NULL`, `[{"name":"settings","region":null}]`)
	// Arrays can be read in their entirety or indexed, and existing entries replaced.
	executeSQL(t, ctx, binary, args, `SELECT "/spec/accessModes" AS modes, "/spec/accessModes/0" AS first FROM persistentvolumeclaims WHERE name = 'data'`, `[{"modes":["ReadWriteOnce"],"first":"ReadWriteOnce"}]`)
	executeSQL(t, ctx, binary, args, `SELECT "/spec/template/spec/containers/0/image" AS image FROM statefulsets WHERE "/spec/replicas" = 0`, `[{"image":"nginx:1.27"}]`)
	// Generalized create/delete uses the same discovered GVR, not a new typed client.
	executeSQL(t, ctx, binary, args, `INSERT INTO "v1/configmaps" (manifest) VALUES ('{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"created"},"data":{"mode":"fresh"}}')`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT name, namespace, "/data/mode" AS mode FROM configmaps WHERE name = 'created'`, `[{"name":"created","namespace":"sql-hard-native","mode":"fresh"}]`)
	executeSQL(t, ctx, binary, args, `DELETE FROM configmaps WHERE name = 'created'`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT name FROM configmaps WHERE name = 'created'`, `[]`)
	// Cluster-level namespace is NULL regardless of the unrelated CLI namespace.
	executeSQL(t, ctx, binary, args, `SELECT name, namespace FROM "v1/namespaces" WHERE name = 'sql-hard-native'`, `[{"name":"sql-hard-native","namespace":null}]`)
	executeSQL(t, ctx, binary, args, `SELECT name, namespace FROM "v1/nodes" WHERE name = 'kubesql-test'`, `[{"name":"kubesql-test","namespace":null}]`)
	// Freeze acceptance: exercise every ordinary resource family listed by the
	// task, not just the five fixture queries. This is read-only routing coverage;
	// an empty resource list is valid and does not claim workload readiness.
	client := testClient(t)
	for _, tt := range []struct {
		table   string
		cluster bool
	}{
		{"v1/pods", false}, {"v1/nodes", true}, {"v1/services", false},
		{"apps/v1/replicasets", false}, {"apps/v1/statefulsets", false}, {"apps/v1/daemonsets", false},
		{"batch/v1/jobs", false}, {"batch/v1/cronjobs", false}, {"v1/configmaps", false}, {"v1/secrets", false},
		{"v1/persistentvolumes", true}, {"v1/persistentvolumeclaims", false}, {"storage.k8s.io/v1/storageclasses", true},
		{"v1/serviceaccounts", false}, {"rbac.authorization.k8s.io/v1/roles", false}, {"rbac.authorization.k8s.io/v1/clusterroles", true},
		{"rbac.authorization.k8s.io/v1/rolebindings", false}, {"rbac.authorization.k8s.io/v1/clusterrolebindings", true},
	} {
		t.Run("catalog/"+tt.table, func(t *testing.T) {
			index := strings.LastIndex(tt.table, "/")
			gv, err := schema.ParseGroupVersion(tt.table[:index])
			if err != nil {
				t.Fatal(err)
			}
			namespace := "sql-hard-native"
			if tt.cluster {
				namespace = ""
			}
			objects, err := client.Resource(gv.WithResource(tt.table[index+1:])).Namespace(namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			rows := make([]map[string]any, 0, len(objects.Items))
			for _, object := range objects.Items {
				var ns any = object.GetNamespace()
				if tt.cluster {
					ns = nil
				}
				rows = append(rows, map[string]any{"name": object.GetName(), "namespace": ns})
			}
			want, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			executeSQL(t, ctx, binary, args, `SELECT name, namespace FROM "`+tt.table+`"`, string(want))
		})
	}
}
