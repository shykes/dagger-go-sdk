package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"dagger.io/dagger"
	"github.com/iancoleman/strcase"
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
		return fmt.Errorf("usage: workspace-module-source render-template|generate|init-generate|deps-add|deps-remove|deps-update ...")
	}

	if os.Args[1] == "render-template" {
		return runRenderTemplate(os.Args[2:])
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()

	switch os.Args[1] {
	case "generate":
		workspaceID, err := envString(workspaceIDEnv)
		if err != nil {
			return err
		}
		return runGenerate(ctx, client, workspaceID, os.Args[2:])
	case "init-generate":
		return runInitGenerate(ctx, client, os.Args[2:])
	case "deps-add":
		return runDepsAdd(ctx, client, os.Args[2:])
	case "deps-remove":
		return runDepsRemove(ctx, client, os.Args[2:])
	case "deps-update":
		return runDepsUpdate(ctx, client, os.Args[2:])
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

func runInitGenerate(ctx context.Context, client *dagger.Client, args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: workspace-module-source init-generate SOURCE_ROOT_PATH BEFORE_DIR AFTER_DIR")
	}

	sourceRootPath, err := clean(args[0])
	if err != nil {
		return err
	}

	changes := client.
		Host().
		Directory("/seed").
		AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: sourceRootPath}).
		GeneratedContextChangeset()

	if _, err := client.Directory().Export(ctx, args[1]); err != nil {
		return err
	}
	if _, err := changes.After().Export(ctx, args[2]); err != nil {
		return err
	}
	return nil
}

func runRenderTemplate(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: workspace-module-source render-template MODULE_NAME TEMPLATE_DIR OUT_DIR")
	}

	moduleName := args[0]
	templateDir := args[1]
	outDir := args[2]
	data := map[string]string{
		"ModuleName":   moduleName,
		"ModuleType":   strcase.ToCamel(moduleName),
		"ModuleImport": "dagger/" + strcase.ToKebab(moduleName),
	}

	return filepath.WalkDir(templateDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(templateDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		dstRel := strings.TrimSuffix(rel, ".tmpl")
		dst := filepath.Join(outDir, dstRel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("template symlinks are not supported: %s", rel)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}

		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(rel, ".tmpl") {
			return os.WriteFile(dst, contents, 0o644)
		}

		var buf bytes.Buffer
		tmpl, err := template.New(rel).Parse(string(contents))
		if err != nil {
			return err
		}
		if err := tmpl.Execute(&buf, data); err != nil {
			return err
		}
		return os.WriteFile(dst, buf.Bytes(), 0o644)
	})
}

func runDepsAdd(ctx context.Context, client *dagger.Client, args []string) error {
	if len(args) != 6 {
		return fmt.Errorf("usage: workspace-module-source deps-add MODULE_PATH CHANGESET_ROOT_PATH SOURCE NAME BEFORE_DIR AFTER_DIR")
	}

	modulePath, err := clean(args[0])
	if err != nil {
		return err
	}
	changesetRootPath, err := clean(args[1])
	if err != nil {
		return err
	}
	dep, err := dependencyModuleSource(client, modulePath, args[2], args[3])
	if err != nil {
		return err
	}

	changes := moduleSource(client, modulePath).
		WithDependencies([]*dagger.ModuleSource{dep}).
		GeneratedContextChangeset()
	return exportChangeset(ctx, changes, changesetRootPath, args[4], args[5])
}

func runDepsRemove(ctx context.Context, client *dagger.Client, args []string) error {
	if len(args) != 5 {
		return fmt.Errorf("usage: workspace-module-source deps-remove MODULE_PATH CHANGESET_ROOT_PATH NAME BEFORE_DIR AFTER_DIR")
	}
	if args[2] == "" {
		return fmt.Errorf("dependency name or source is required")
	}

	modulePath, err := clean(args[0])
	if err != nil {
		return err
	}
	changesetRootPath, err := clean(args[1])
	if err != nil {
		return err
	}

	changes := moduleSource(client, modulePath).
		WithoutDependencies([]string{args[2]}).
		GeneratedContextChangeset()
	return exportChangeset(ctx, changes, changesetRootPath, args[3], args[4])
}

func runDepsUpdate(ctx context.Context, client *dagger.Client, args []string) error {
	if len(args) != 5 {
		return fmt.Errorf("usage: workspace-module-source deps-update MODULE_PATH CHANGESET_ROOT_PATH NAME BEFORE_DIR AFTER_DIR")
	}

	modulePath, err := clean(args[0])
	if err != nil {
		return err
	}
	changesetRootPath, err := clean(args[1])
	if err != nil {
		return err
	}

	var deps []string
	if args[2] != "" {
		deps = []string{args[2]}
	}
	changes := moduleSource(client, modulePath).
		WithUpdateDependencies(deps).
		GeneratedContextChangeset()
	return exportChangeset(ctx, changes, changesetRootPath, args[3], args[4])
}

func moduleSource(client *dagger.Client, modulePath string) *dagger.ModuleSource {
	ref := "/ws"
	if modulePath != "." {
		ref = path.Join(ref, modulePath)
	}
	return client.ModuleSource(ref, dagger.ModuleSourceOpts{
		RequireKind: dagger.ModuleSourceKindLocalSource,
	})
}

func dependencyModuleSource(client *dagger.Client, modulePath, source, name string) (*dagger.ModuleSource, error) {
	if source == "" {
		return nil, fmt.Errorf("dependency source is required")
	}

	ref := source
	if isLocalModuleRef(source) {
		localSource, _, hasPin := strings.Cut(source, "@")
		if hasPin {
			return nil, fmt.Errorf("pinning local dependency sources is not supported: %s", source)
		}

		var depPath string
		var err error
		if path.IsAbs(localSource) {
			depPath, err = clean(localSource)
		} else {
			depPath, err = clean(path.Join(modulePath, localSource))
		}
		if err != nil {
			return nil, err
		}

		ref = "/ws"
		if depPath != "." {
			ref = path.Join(ref, depPath)
		}
	}

	dep := client.ModuleSource(ref, dagger.ModuleSourceOpts{
		DisableFindUp: true,
	})
	if name != "" {
		dep = dep.WithName(name)
	}
	return dep, nil
}

func isLocalModuleRef(ref string) bool {
	source, _, _ := strings.Cut(ref, "@")
	if strings.HasPrefix(source, "/") || strings.HasPrefix(source, ".") {
		return true
	}
	return !strings.Contains(source, ".")
}

func exportChangeset(ctx context.Context, changes *dagger.Changeset, changesetRootPath, beforeDir, afterDir string) error {
	if _, err := changes.Before().Directory(changesetRootPath).Export(ctx, beforeDir); err != nil {
		return err
	}
	if _, err := changes.After().Directory(changesetRootPath).Export(ctx, afterDir); err != nil {
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
