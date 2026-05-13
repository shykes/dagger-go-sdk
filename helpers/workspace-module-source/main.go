package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
	if len(os.Args) != 5 {
		return fmt.Errorf("usage: workspace-module-source SOURCE_ROOT_PATH CHANGESET_ROOT_PATH BEFORE_DIR AFTER_DIR")
	}

	workspaceID, err := envString(workspaceIDEnv)
	if err != nil {
		return err
	}

	var include []string
	if err := json.Unmarshal([]byte(os.Getenv(includeEnv)), &include); err != nil {
		return fmt.Errorf("parse %s: %w", includeEnv, err)
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()

	changes := client.
		LoadWorkspaceFromID(dagger.WorkspaceID(workspaceID)).
		Directory("/", dagger.WorkspaceDirectoryOpts{Include: include}).
		AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: os.Args[1]}).
		GeneratedContextChangeset()

	changesetRootPath := os.Args[2]
	if _, err := changes.Before().Directory(changesetRootPath).Export(ctx, os.Args[3]); err != nil {
		return err
	}
	if _, err := changes.After().Directory(changesetRootPath).Export(ctx, os.Args[4]); err != nil {
		return err
	}
	return nil
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
