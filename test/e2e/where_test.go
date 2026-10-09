//go:build e2e

package e2e

import (
	"bytes"
	"os/exec"
	"reflect"
	"testing"
)

func TestEasyWhere(t *testing.T) {
	ctx, binary, args := prepareFixture(t, "easy-where", "sql-easy-where")
	for _, name := range []string{"precedence", "parentheses", "is-null", "equals-null", "not-unknown"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Stdin = bytes.NewReader(readFixture(t, "easy-where", name+".sql"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("CLI failed: %v, stderr: %s", err, &stderr)
			}
			got := canonicalRows(t, stdout.Bytes())
			want := canonicalRows(t, readFixture(t, "easy-where", name+".json"))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rows = %v, want %v", got, want)
			}
		})
	}
}
