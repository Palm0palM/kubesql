package main

import (
	"bytes"
	"testing"
)

func TestRunReportsUnimplemented(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(&stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	want := "ksql: SQL execution is not implemented yet\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
