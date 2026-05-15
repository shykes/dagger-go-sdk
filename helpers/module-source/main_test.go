package main

import (
	"reflect"
	"testing"
)

func TestModuleSourceIncludeFromConfigsIncludesDeclaredPaths(t *testing.T) {
	configs := map[string]sourceConfig{
		"app/dagger.json": {
			dependencies: []string{"../dep"},
			include:      []string{"../root.txt", "assets/**/*"},
		},
		"dep/dagger.json": {
			include: []string{"../shared.txt", "subdir/**/*"},
		},
	}

	got, err := moduleSourceIncludeFromConfigs(configs, "app")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"app",
		"app/**",
		"app/assets/**/*",
		"app/dagger.json",
		"dep",
		"dep/**",
		"dep/dagger.json",
		"dep/subdir/**/*",
		"root.txt",
		"shared.txt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("include mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
