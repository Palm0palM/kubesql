package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/Palm0palM/kubesql/internal/engine"
	"github.com/Palm0palM/kubesql/internal/kube"
	"github.com/Palm0palM/kubesql/internal/sql"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Reason  string `json:"reason,omitempty"`
}

func fail(stderr io.Writer, code int, detail any) int {
	_ = json.NewEncoder(stderr).Encode(detail)
	return code
}

// Run executes one SQL statement. Logs/errors never share stdout with results.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ksql", flag.ContinueOnError)
	var help bytes.Buffer
	flags.SetOutput(&help)
	var options kube.Options
	flags.StringVar(&options.Kubeconfig, "kubeconfig", "", "path to kubeconfig")
	flags.StringVar(&options.Context, "context", "", "kubeconfig context")
	flags.StringVar(&options.Namespace, "namespace", "", "namespace (defaults to context namespace)")
	all := flags.Bool("all-namespaces", false, "query all namespaces")
	output := flags.String("output", "json", "output format (json)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if _, err := io.Copy(stdout, &help); err != nil {
				return fail(stderr, 1, diagnostic{Code: "E_OUTPUT", Message: "cannot write help"})
			}
			return 0
		}
		return fail(stderr, 2, diagnostic{Code: "E_FLAGS", Message: err.Error()})
	}
	if flags.NArg() != 0 || *output != "json" {
		return fail(stderr, 2, diagnostic{Code: "E_FLAGS", Message: "use stdin for SQL and --output json"})
	}
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fail(stderr, 1, diagnostic{Code: "E_INPUT", Message: "cannot read SQL from stdin"})
	}
	stmt, err := sql.Parse(string(input))
	if err != nil {
		return fail(stderr, 2, err)
	}
	query, err := engine.Bind(stmt)
	if err != nil {
		return fail(stderr, 2, err)
	}
	client, namespace, err := kube.Connect(options)
	if err != nil {
		return fail(stderr, 1, diagnostic{Code: "E_CONFIG", Message: err.Error()})
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := query.Execute(ctx, client, namespace, *all)
	if err != nil {
		return fail(stderr, 1, diagnostic{Code: "E_API", Message: err.Error(), Reason: string(apierrors.ReasonForError(err))})
	}
	if err := json.NewEncoder(stdout).Encode(rows); err != nil {
		return fail(stderr, 1, diagnostic{Code: "E_OUTPUT", Message: "cannot write JSON results"})
	}
	return 0
}
