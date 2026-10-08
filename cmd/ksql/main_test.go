package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Palm0palM/kubesql/internal/cli"
)

func TestEmptyInputReportsParseError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), nil, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	var detail struct{ Code string }
	if err := json.Unmarshal(stderr.Bytes(), &detail); err != nil || detail.Code != "E_PARSE" || stdout.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q, decode error = %v", stdout.String(), stderr.String(), err)
	}
}
