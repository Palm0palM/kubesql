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
	"github.com/Palm0palM/kubesql/internal/resource"
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
	var query *engine.Query
	var write *engine.Write
	var insert *engine.Insert
	metrics := engine.IsMetrics(stmt)
	if metrics {
		query, err = engine.BindMetrics(stmt)
	} else {
		err = engine.Preflight(stmt, *all)
	}
	if err != nil {
		return fail(stderr, 2, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, namespace, err := kube.Connect(ctx, options)
	if err != nil {
		return fail(stderr, 1, diagnostic{Code: "E_CONFIG", Message: err.Error()})
	}
	if stmt.Type == "insert" {
		if err := engine.Preflight(stmt, *all, namespace); err != nil {
			return fail(stderr, 2, err)
		}
	}
	if !metrics {
		descriptor, err := client.Resolve(ctx, stmt.Table, stmt.TableQuoted)
		if err != nil {
			if detail, ok := errors.AsType[*resource.Error](err); ok {
				code := 2
				if detail.Code == "E_DISCOVERY" {
					code = 1
				}
				return fail(stderr, code, detail)
			}
			return fail(stderr, 1, diagnostic{Code: "E_DISCOVERY", Message: "API discovery failed"})
		}
		if stmt.Type == "select" {
			query, err = engine.Bind(stmt, descriptor)
		} else if stmt.Type == "insert" {
			insert, err = engine.BindInsert(stmt, *all, descriptor)
		} else {
			write, err = engine.BindWrite(stmt, *all, descriptor)
		}
		if err != nil {
			return fail(stderr, 2, err)
		}
	}
	var outputValue any
	exitCode := 0
	if write != nil || insert != nil {
		var result *engine.WriteResult
		if insert != nil {
			result, err = insert.Execute(ctx, client, namespace)
		} else {
			result, err = write.Execute(ctx, client, namespace)
		}
		outputValue = result
		if result != nil && result.FailedRows != 0 {
			exitCode = 1
		}
	} else if metrics {
		outputValue, err = query.ExecuteMetrics(ctx, client, namespace, *all)
	} else {
		outputValue, err = query.Execute(ctx, client, namespace, *all)
	}
	if err != nil {
		if detail, ok := errors.AsType[*resource.Error](err); ok {
			return fail(stderr, 1, detail)
		}
		if semantic, ok := errors.AsType[*engine.Error](err); ok {
			return fail(stderr, 2, semantic)
		}
		return fail(stderr, 1, diagnostic{Code: "E_API", Message: "API request failed", Reason: string(apierrors.ReasonForError(err))})
	}
	if err := json.NewEncoder(stdout).Encode(outputValue); err != nil {
		return fail(stderr, 1, diagnostic{Code: "E_OUTPUT", Message: "cannot write JSON results"})
	}
	return exitCode
}
