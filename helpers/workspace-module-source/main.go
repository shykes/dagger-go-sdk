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

const (
	workspaceIDEnv = "DAGGER_GO_SDK_WORKSPACE_ID"
	includeEnv     = "DAGGER_GO_SDK_INCLUDE"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: workspace-module-source generate|init ...")
	}

	workspaceID, err := envString(workspaceIDEnv)
	if err != nil {
		return err
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()

	switch os.Args[1] {
	case "generate":
		return runGenerate(ctx, client, workspaceID, os.Args[2:])
	case "init":
		return runInit(ctx, client, workspaceID, os.Args[2:])
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func runGenerate(ctx context.Context, client *dagger.Client, workspaceID string, args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("usage: workspace-module-source generate SOURCE_ROOT_PATH CHANGESET_ROOT_PATH BEFORE_DIR AFTER_DIR")
	}

	var include []string
	if err := json.Unmarshal([]byte(os.Getenv(includeEnv)), &include); err != nil {
		return fmt.Errorf("parse %s: %w", includeEnv, err)
	}

	changes := client.
		LoadWorkspaceFromID(dagger.WorkspaceID(workspaceID)).
		Directory("/", dagger.WorkspaceDirectoryOpts{Include: include}).
		AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: args[0]}).
		GeneratedContextChangeset()

	changesetRootPath := args[1]
	if _, err := changes.Before().Directory(changesetRootPath).Export(ctx, args[2]); err != nil {
		return err
	}
	if _, err := changes.After().Directory(changesetRootPath).Export(ctx, args[3]); err != nil {
		return err
	}
	return nil
}

func runInit(ctx context.Context, client *dagger.Client, workspaceID string, args []string) error {
	if len(args) != 5 {
		return fmt.Errorf("usage: workspace-module-source init MODULE_PATH CHANGESET_ROOT_PATH NAME BEFORE_DIR AFTER_DIR")
	}

	modulePath, err := clean(args[0])
	if err != nil {
		return err
	}
	changesetRootPath, err := clean(args[1])
	if err != nil {
		return err
	}
	name := args[2]
	diffPath, err := childPath(changesetRootPath, modulePath)
	if err != nil {
		return err
	}

	existing := client.
		LoadWorkspaceFromID(dagger.WorkspaceID(workspaceID)).
		Directory("/", dagger.WorkspaceDirectoryOpts{Include: []string{path.Join(modulePath, "*")}})
	exists, err := existing.Exists(ctx, path.Join(modulePath, "dagger.json"))
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("module already exists: %s", modulePath)
	}

	source := client.
		Directory().
		WithNewFile(path.Join(modulePath, "dagger.json"), "{}").
		AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: modulePath})

	generated := source.
		WithName(name).
		WithSDK("go").
		WithEngineVersion("latest").
		GeneratedContextDirectory().
		Directory(modulePath)

	before := client.Directory()
	after := before.WithDirectory(diffPath, generated)
	if _, err := before.Export(ctx, args[3]); err != nil {
		return err
	}
	if _, err := after.Export(ctx, args[4]); err != nil {
		return err
	}
	return nil
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

func childPath(parent, child string) (string, error) {
	if parent == "." {
		return child, nil
	}
	if child == parent {
		return ".", nil
	}
	prefix := parent + "/"
	if strings.HasPrefix(child, prefix) {
		return strings.TrimPrefix(child, prefix), nil
	}
	return "", fmt.Errorf("module path %q is outside changeset root %q", child, parent)
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
