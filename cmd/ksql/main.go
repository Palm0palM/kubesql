package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Stderr))
}

func run(stderr io.Writer) int {
	fmt.Fprintln(stderr, "ksql: SQL execution is not implemented yet")
	return 1
}
