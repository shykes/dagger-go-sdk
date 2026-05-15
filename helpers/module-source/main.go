package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"dagger.io/dagger"
)

const workspaceIDEnv = "WORKSPACE_ID"

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
	opts, err := parseModuleSourceOptions(os.Args[1:], 1)
	if err != nil {
		return err
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()

	workspaceID, err := envString(workspaceIDEnv)
	if err != nil {
		return err
	}
	workspace := client.LoadWorkspaceFromID(dagger.WorkspaceID(workspaceID))

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

func parseModuleSourceOptions(args []string, wantPositionals int) (moduleSourceOptions, error) {
	opts, rest, err := parseOptions(args)
	if err != nil {
		return opts, err
	}
	if len(rest) != wantPositionals {
		return opts, fmt.Errorf("usage: module-source REF [--cwd CWD] [--local] [--name NAME]")
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

func moduleSource(
	ctx context.Context,
	client *dagger.Client,
	workspace *dagger.Workspace,
	opts moduleSourceOptions,
) (*dagger.ModuleSource, error) {
	cwd := opts.cwd
	if cwd == "" {
		var err error
		cwd, err = currentWorkspacePath(ctx, workspace)
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
		include, err := workspaceModuleSourceInclude(ctx, workspace, candidate)
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
	workspace *dagger.Workspace,
	modulePath string,
) ([]string, error) {
	configDir := workspace.Directory("/", dagger.WorkspaceDirectoryOpts{
		Include: []string{"**/dagger.json"},
	})
	configPaths, err := configDir.Glob(ctx, "**/dagger.json")
	if err != nil {
		return nil, err
	}
	rootConfig, err := configDir.Exists(ctx, "dagger.json", dagger.DirectoryExistsOpts{
		ExpectedType: dagger.ExistsTypeRegularType,
	})
	if err != nil {
		return nil, err
	}
	if rootConfig && !contains(configPaths, "dagger.json") {
		configPaths = append(configPaths, "dagger.json")
	}

	configs := map[string]sourceConfig{}
	for _, configPath := range configPaths {
		contents, err := configDir.File(configPath).Contents(ctx)
		if err != nil {
			return nil, err
		}
		config, err := parseSourceConfig(contents)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", configPath, err)
		}
		configs[configPath] = config
	}

	return moduleSourceIncludeFromConfigs(configs, modulePath)
}

type sourceConfig struct {
	dependencies []string
	include      []string
}

func moduleSourceIncludeFromConfigs(configs map[string]sourceConfig, modulePath string) ([]string, error) {
	include := map[string]struct{}{}
	seen := map[string]struct{}{}
	var visit func(string) error
	visit = func(p string) error {
		p, err := clean(p)
		if err != nil {
			return err
		}
		if _, ok := seen[p]; ok {
			return nil
		}
		seen[p] = struct{}{}

		configPath := daggerJSONPath(p)
		config, ok := configs[configPath]
		if !ok {
			return nil
		}

		if p == "." {
			include["."] = struct{}{}
			include["dagger.json"] = struct{}{}
			include["**"] = struct{}{}
		} else {
			include[p] = struct{}{}
			include[configPath] = struct{}{}
			include[path.Join(p, "**")] = struct{}{}
		}

		for _, includePath := range config.include {
			resolved, err := workspacePath(p, includePath)
			if err != nil {
				return err
			}
			include[resolved] = struct{}{}
		}

		for _, dep := range config.dependencies {
			if mustBeLocalRef(dep) {
				depPath, err := workspacePath(p, dep)
				if err != nil {
					return err
				}
				if err := visit(depPath); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(modulePath); err != nil {
		return nil, err
	}

	ordered := make([]string, 0, len(include))
	for p := range include {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	return ordered, nil
}

func parseSourceConfig(contents string) (sourceConfig, error) {
	var config struct {
		Dependencies []json.RawMessage `json:"dependencies"`
		Include      []json.RawMessage `json:"include"`
	}
	if err := json.Unmarshal([]byte(contents), &config); err != nil {
		return sourceConfig{}, err
	}

	var parsed sourceConfig
	for _, raw := range config.Dependencies {
		var source string
		if err := json.Unmarshal(raw, &source); err == nil {
			parsed.dependencies = append(parsed.dependencies, source)
			continue
		}

		var object struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(raw, &object); err != nil {
			return sourceConfig{}, err
		}
		if object.Source != "" {
			parsed.dependencies = append(parsed.dependencies, object.Source)
		}
	}
	for _, raw := range config.Include {
		var includePath string
		if err := json.Unmarshal(raw, &includePath); err == nil && includePath != "" {
			parsed.include = append(parsed.include, includePath)
		}
	}
	return parsed, nil
}

func daggerJSONPath(modulePath string) string {
	if modulePath == "." {
		return "dagger.json"
	}
	return path.Join(modulePath, "dagger.json")
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
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

func currentWorkspacePath(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	// Newer engines do not expose Workspace.path. Searching for "." from "."
	// returns the current workspace directory as a workspace-root-relative path.
	cwd, err := workspace.FindUp(ctx, ".", dagger.WorkspaceFindUpOpts{From: "."})
	if err != nil {
		return "", err
	}
	return clean(cwd)
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
