package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"
)

const varsJSONEnv = "DAGGER_QUERY_VARS_JSON"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dagger-query", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	queryPath := fs.String("query", "", "GraphQL query file")
	varsPath := fs.String("vars", "", "GraphQL variables JSON file")
	outPath := fs.String("out", "/response.json", "response JSON output file")
	opName := fs.String("operation", "", "GraphQL operation name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *queryPath == "" {
		return fmt.Errorf("--query is required")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	query, err := os.ReadFile(*queryPath)
	if err != nil {
		return fmt.Errorf("read query: %w", err)
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()

	vars, err := variables(*varsPath)
	if err != nil {
		return err
	}

	var data any
	resp := dagger.Response{Data: &data}
	if err := client.Do(ctx, &dagger.Request{
		Query:     string(query),
		Variables: vars,
		OpName:    *opName,
	}, &resp); err != nil {
		return err
	}

	full := map[string]any{"data": data}
	if len(resp.Extensions) > 0 {
		full["extensions"] = resp.Extensions
	}
	if len(resp.Errors) > 0 {
		full["errors"] = resp.Errors
	}

	encoded, err := json.MarshalIndent(full, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(*outPath, append(encoded, '\n'), 0o644)
}

func variables(varsPath string) (map[string]any, error) {
	var raw []byte
	if varsPath != "" {
		var err error
		raw, err = os.ReadFile(varsPath)
		if err != nil {
			return nil, fmt.Errorf("read vars: %w", err)
		}
	} else {
		raw = []byte(os.Getenv(varsJSONEnv))
	}

	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		return nil, fmt.Errorf("decode vars: %w", err)
	}
	return vars, nil
}
