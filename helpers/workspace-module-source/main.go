// module-source is an external nested-client helper for the operation this
// Dang module cannot safely perform inside a module function:
// loading a ModuleSource from a Directory.
//
// Keep that boundary intact. The caller module function may prepare a
// WorkspaceID, DirectoryID, paths, and flags, but the Directory -> ModuleSource
// conversion must happen here, in this plain helper process connected to a
// nested Dagger client. Moving this code into a Dagger/Dang module function
// would reintroduce the original failure mode.
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
	workspaceIDEnv     = "WORKSPACE_ID"
	seedDirectoryIDEnv = "SEED_DIRECTORY_ID"
)

type moduleSourceOptions struct {
	ref   string
	cwd   string
	local bool
	name  string
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: module-source REF [--cwd CWD] [--local] [--name NAME]")
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
		return runGenerate(ctx, client, os.Args[2:])
	case "deps-add":
		return runDepsAdd(ctx, client, os.Args[2:])
	case "deps-remove":
		return runDepsRemove(ctx, client, os.Args[2:])
	case "deps-update":
		return runDepsUpdate(ctx, client, os.Args[2:])
	default:
		return runModuleSource(ctx, client, os.Args[1:])
	}
}

func runModuleSource(ctx context.Context, client *dagger.Client, args []string) error {
	opts, err := parseModuleSourceOptions(args, 1)
	if err != nil {
		return err
	}

	workspace, err := workspace(ctx, client)
	if err != nil {
		return err
	}

	src, err := moduleSource(ctx, client, workspace, opts)
	if err != nil {
		return err
	}
	if opts.name != "" {
		src = src.WithName(opts.name)
	}

	id, err := src.ID(ctx)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func runGenerate(ctx context.Context, client *dagger.Client, args []string) error {
	opts, rest, err := parseOptions(args)
	if err != nil {
		return err
	}
	if len(rest) != 4 {
		return fmt.Errorf("usage: module-source generate REF CHANGESET_ROOT_PATH BEFORE_DIR AFTER_DIR [--cwd CWD] [--local] [--name NAME]")
	}
	opts.ref = rest[0]
	changesetRootPath, err := clean(rest[1])
	if err != nil {
		return err
	}

	seedDirectoryID, seeded, err := optionalEnvString(seedDirectoryIDEnv)
	if err != nil {
		return err
	}
	if seeded {
		sourceRootPath, err := clean(opts.ref)
		if err != nil {
			return err
		}
		changes := client.
			LoadDirectoryFromID(dagger.DirectoryID(seedDirectoryID)).
			AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: sourceRootPath}).
			GeneratedContextChangeset()
		if _, err := client.Directory().Export(ctx, rest[2]); err != nil {
			return err
		}
		if _, err := directoryAt(changes.After(), changesetRootPath).Export(ctx, rest[3]); err != nil {
			return err
		}
		return nil
	}

	workspace, err := workspace(ctx, client)
	if err != nil {
		return err
	}
	src, err := moduleSource(ctx, client, workspace, opts)
	if err != nil {
		return err
	}
	if opts.name != "" {
		src = src.WithName(opts.name)
	}

	return exportChangeset(ctx, src.GeneratedContextChangeset(), changesetRootPath, rest[2], rest[3])
}

func runDepsAdd(ctx context.Context, client *dagger.Client, args []string) error {
	opts, rest, err := parseOptions(args)
	if err != nil {
		return err
	}
	if len(rest) != 5 {
		return fmt.Errorf("usage: module-source deps-add TARGET_REF DEP_REF CHANGESET_ROOT_PATH BEFORE_DIR AFTER_DIR [--name NAME]")
	}

	targetPath, err := clean(rest[0])
	if err != nil {
		return err
	}
	changesetRootPath, err := clean(rest[2])
	if err != nil {
		return err
	}

	workspace, err := workspace(ctx, client)
	if err != nil {
		return err
	}
	target := localFSModuleSource(client, targetPath)
	dep, err := dependencyModuleSource(ctx, client, workspace, targetPath, rest[1], opts.name)
	if err != nil {
		return err
	}

	changes := target.
		WithDependencies([]*dagger.ModuleSource{dep}).
		GeneratedContextChangeset()
	return exportChangeset(ctx, changes, changesetRootPath, rest[3], rest[4])
}

func runDepsRemove(ctx context.Context, client *dagger.Client, args []string) error {
	_, rest, err := parseOptions(args)
	if err != nil {
		return err
	}
	if len(rest) != 5 {
		return fmt.Errorf("usage: module-source deps-remove TARGET_REF DEP_NAME CHANGESET_ROOT_PATH BEFORE_DIR AFTER_DIR")
	}
	if rest[1] == "" {
		return fmt.Errorf("dependency name or source is required")
	}

	targetPath, err := clean(rest[0])
	if err != nil {
		return err
	}
	changesetRootPath, err := clean(rest[2])
	if err != nil {
		return err
	}

	target := localFSModuleSource(client, targetPath)

	changes := target.
		WithoutDependencies([]string{rest[1]}).
		GeneratedContextChangeset()
	return exportChangeset(ctx, changes, changesetRootPath, rest[3], rest[4])
}

func runDepsUpdate(ctx context.Context, client *dagger.Client, args []string) error {
	_, rest, err := parseOptions(args)
	if err != nil {
		return err
	}
	if len(rest) != 5 {
		return fmt.Errorf("usage: module-source deps-update TARGET_REF DEP_NAME CHANGESET_ROOT_PATH BEFORE_DIR AFTER_DIR")
	}

	targetPath, err := clean(rest[0])
	if err != nil {
		return err
	}
	changesetRootPath, err := clean(rest[2])
	if err != nil {
		return err
	}

	var deps []string
	if rest[1] != "" {
		deps = []string{rest[1]}
	}

	target := localFSModuleSource(client, targetPath)

	changes := target.
		WithUpdateDependencies(deps).
		GeneratedContextChangeset()
	return exportChangeset(ctx, changes, changesetRootPath, rest[3], rest[4])
}

func parseModuleSourceOptions(args []string, wantPositionals int) (moduleSourceOptions, error) {
	opts, rest, err := parseOptions(args)
	if err != nil {
		return opts, err
	}
	if len(rest) != wantPositionals {
		return opts, fmt.Errorf("expected %d module source ref argument(s), got %d", wantPositionals, len(rest))
	}
	opts.ref = rest[0]
	return opts, nil
}

func parseOptions(args []string) (moduleSourceOptions, []string, error) {
	var opts moduleSourceOptions
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--local":
			opts.local = true
		case arg == "--cwd":
			i++
			if i >= len(args) {
				return opts, nil, fmt.Errorf("--cwd requires a value")
			}
			opts.cwd = args[i]
		case strings.HasPrefix(arg, "--cwd="):
			opts.cwd = strings.TrimPrefix(arg, "--cwd=")
		case arg == "--name":
			i++
			if i >= len(args) {
				return opts, nil, fmt.Errorf("--name requires a value")
			}
			opts.name = args[i]
		case strings.HasPrefix(arg, "--name="):
			opts.name = strings.TrimPrefix(arg, "--name=")
		case strings.HasPrefix(arg, "-"):
			return opts, nil, fmt.Errorf("unknown option: %s", arg)
		default:
			rest = append(rest, arg)
		}
	}
	return opts, rest, nil
}

func workspace(ctx context.Context, client *dagger.Client) (*dagger.Workspace, error) {
	workspaceID, err := envString(workspaceIDEnv)
	if err != nil {
		return nil, err
	}
	return client.LoadWorkspaceFromID(dagger.WorkspaceID(workspaceID)), nil
}

func moduleSource(
	ctx context.Context,
	client *dagger.Client,
	workspace *dagger.Workspace,
	opts moduleSourceOptions,
) (*dagger.ModuleSource, error) {
	cwd := opts.cwd
	if cwd == "" {
		var err error
		cwd, err = workspace.Path(ctx)
		if err != nil {
			return nil, err
		}
	}

	candidate, err := workspacePath(cwd, opts.ref)
	if err != nil {
		return nil, err
	}

	local, err := workspaceDirectoryExists(ctx, workspace, candidate)
	if err != nil {
		return nil, err
	}
	if local {
		include, err := workspaceModuleSourceInclude(ctx, client, workspace, candidate)
		if err != nil {
			return nil, err
		}
		return workspace.
			Directory("/", dagger.WorkspaceDirectoryOpts{Include: include}).
			AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: candidate}), nil
	}
	if opts.local || mustBeLocalRef(opts.ref) {
		return nil, fmt.Errorf("local module source %q does not exist in workspace at %q", opts.ref, candidate)
	}

	return client.ModuleSource(opts.ref, dagger.ModuleSourceOpts{
		DisableFindUp: true,
	}), nil
}

func localFSModuleSource(client *dagger.Client, modulePath string) *dagger.ModuleSource {
	ref := "/ws"
	if modulePath != "." {
		ref = path.Join(ref, modulePath)
	}
	return client.ModuleSource(ref, dagger.ModuleSourceOpts{
		DisableFindUp: true,
		RequireKind:   dagger.ModuleSourceKindLocalSource,
	})
}

func dependencyModuleSource(
	ctx context.Context,
	client *dagger.Client,
	workspace *dagger.Workspace,
	modulePath,
	source,
	name string,
) (*dagger.ModuleSource, error) {
	if source == "" {
		return nil, fmt.Errorf("dependency source is required")
	}

	candidate, err := workspacePath(modulePath, source)
	if err != nil {
		return nil, err
	}
	local, err := workspaceDirectoryExists(ctx, workspace, candidate)
	if err != nil {
		return nil, err
	}

	var dep *dagger.ModuleSource
	if local {
		dep = localFSModuleSource(client, candidate)
	} else if mustBeLocalRef(source) {
		return nil, fmt.Errorf("local module source %q does not exist in workspace at %q", source, candidate)
	} else {
		dep = client.ModuleSource(source, dagger.ModuleSourceOpts{
			DisableFindUp: true,
		})
	}

	if name != "" {
		dep = dep.WithName(name)
	}
	return dep, nil
}

func workspaceDirectoryExists(ctx context.Context, workspace *dagger.Workspace, p string) (bool, error) {
	p, err := clean(p)
	if err != nil {
		return false, err
	}
	if p == "." {
		return true, nil
	}
	return workspace.
		Directory("/", dagger.WorkspaceDirectoryOpts{Include: []string{p, path.Join(p, "**")}}).
		Exists(ctx, p, dagger.DirectoryExistsOpts{ExpectedType: dagger.ExistsTypeDirectoryType})
}

func workspaceModuleSourceInclude(
	ctx context.Context,
	client *dagger.Client,
	workspace *dagger.Workspace,
	p string,
) ([]string, error) {
	stdout, err := client.Container().
		From("python:3.13-alpine").
		WithDirectory("/ws", workspace.Directory("/", dagger.WorkspaceDirectoryOpts{Include: []string{"**/dagger.json"}})).
		WithFile("/includes.py", client.Host().File("/helper/includes.py")).
		WithExec([]string{"python3", "/includes.py", p}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}

	var include []string
	for _, line := range strings.Split(stdout, "\n") {
		if line != "" {
			include = append(include, line)
		}
	}
	return include, nil
}

func workspacePath(cwd, ref string) (string, error) {
	cwd, err := clean(cwd)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(ref, "/") {
		return clean(ref)
	}
	if cwd == "." {
		return clean(ref)
	}
	return clean(path.Join(cwd, ref))
}

func mustBeLocalRef(ref string) bool {
	if ref == "" {
		return false
	}
	return strings.HasPrefix(ref, "/") ||
		strings.HasPrefix(ref, ".") ||
		strings.HasPrefix(ref, "..") ||
		!strings.Contains(ref, ".")
}

func exportChangeset(ctx context.Context, changes *dagger.Changeset, changesetRootPath, beforeDir, afterDir string) error {
	if _, err := directoryAt(changes.Before(), changesetRootPath).Export(ctx, beforeDir); err != nil {
		return err
	}
	if _, err := directoryAt(changes.After(), changesetRootPath).Export(ctx, afterDir); err != nil {
		return err
	}
	return nil
}

func directoryAt(dir *dagger.Directory, p string) *dagger.Directory {
	if p == "." {
		return dir
	}
	return dir.Directory(p)
}

func runRenderTemplate(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: module-source render-template MODULE_NAME TEMPLATE_DIR OUT_DIR")
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

func optionalEnvString(name string) (string, bool, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return "", false, nil
	}

	var decoded string
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return decoded, true, nil
	}
	return raw, true, nil
}
