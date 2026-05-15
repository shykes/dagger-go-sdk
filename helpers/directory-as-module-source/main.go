package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"dagger.io/dagger"
)

const directoryIDEnv = "DIRECTORY_ID"

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	sourceRootPath, name, err := parseArgs(os.Args[1:])
	if err != nil {
		return err
	}

	directoryID, err := envString(directoryIDEnv)
	if err != nil {
		return err
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()

	src := client.
		LoadDirectoryFromID(dagger.DirectoryID(directoryID)).
		AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: sourceRootPath})
	if name != "" {
		src = src.WithName(name)
	}

	id, err := src.ID(ctx)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func parseArgs(args []string) (sourceRootPath string, name string, err error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--name":
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("--name requires a value")
			}
			name = args[i]
		case strings.HasPrefix(arg, "--name="):
			name = strings.TrimPrefix(arg, "--name=")
		case strings.HasPrefix(arg, "-"):
			return "", "", fmt.Errorf("unknown option: %s", arg)
		default:
			rest = append(rest, arg)
		}
	}
	if len(rest) != 1 {
		return "", "", fmt.Errorf("usage: directory-as-module-source SOURCE_ROOT_PATH [--name NAME]")
	}
	sourceRootPath, err = clean(rest[0])
	if err != nil {
		return "", "", err
	}
	return sourceRootPath, name, nil
}

func clean(p string) (string, error) {
	p = path.Clean(strings.TrimPrefix(p, "/"))
	if p == "." || p == "" {
		return ".", nil
	}
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("path escapes workspace: %s", p)
	}
	return p, nil
}

func envString(name string) (string, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return "", fmt.Errorf("%s is not set", name)
	}

	var decoded string
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return decoded, nil
	}
	return raw, nil
}
