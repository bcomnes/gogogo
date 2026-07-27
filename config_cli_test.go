package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSubcommandLifecycle(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	app := &application{client: http.DefaultClient, configPath: path}
	run := func(arguments ...string) string {
		t.Helper()
		var output bytes.Buffer
		var errorOutput bytes.Buffer
		exitCode := app.run(context.Background(), append([]string{"config"}, arguments...), strings.NewReader(""), &output, &errorOutput)
		if exitCode != 0 {
			t.Fatalf("gogogo config %v exit code = %d, stderr = %q", arguments, exitCode, errorOutput.String())
		}
		return output.String()
	}

	if got := strings.TrimSpace(run("path")); got != path {
		t.Fatalf("config path = %q, want %q", got, path)
	}
	run("set", "template", "owner/template#main")
	run("set", "github.visibility", "private")
	run("set", "github.owner", "acme")
	run("set", "parameter.license", "MIT")

	shown := run("show")
	for _, wanted := range []string{"owner", "template", "main", "github_visibility", "private", "github_owner", "acme", "license", "MIT"} {
		if !strings.Contains(shown, wanted) {
			t.Fatalf("config show output does not contain %q: %s", wanted, shown)
		}
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.GitHub.String() != "owner/template#main" || cfg.GitHubVisibility != "private" || cfg.GitHubOwner != "acme" || cfg.Defaults["license"] != "MIT" {
		t.Fatalf("configured values = %+v", cfg)
	}

	run("unset", "template")
	run("unset", "github.visibility")
	run("unset", "github.owner")
	run("unset", "parameter.license")
	cfg, err = loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	defaults := defaultConfig()
	if cfg.GitHub.String() != defaults.GitHub.String() || cfg.GitHubVisibility != "" || cfg.GitHubOwner != "" || len(cfg.Defaults) != 0 {
		t.Fatalf("config was not reset by unset operations: %+v", cfg)
	}
}

func TestConfigSubcommandRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"config"},
		{"config", "unknown"},
		{"config", "show", "extra"},
		{"config", "path", "extra"},
		{"config", "set", "github.visibility"},
		{"config", "set", "github.visibility", "secret"},
		{"config", "set", "github.owner", "bad/owner"},
		{"config", "set", "parameter.", "value"},
		{"config", "set", "parameter.owner", ""},
		{"config", "set", "unknown", "value"},
		{"config", "unset"},
		{"config", "unset", "unknown"},
	}
	for _, arguments := range tests {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			app := &application{client: http.DefaultClient, configPath: filepath.Join(t.TempDir(), "config.json")}
			var errorOutput bytes.Buffer
			exitCode := app.run(context.Background(), arguments, strings.NewReader(""), io.Discard, &errorOutput)
			if exitCode != 2 {
				t.Fatalf("run(%q) exit code = %d, stderr = %q", arguments, exitCode, errorOutput.String())
			}
		})
	}
}

func TestConfigSubcommandHelp(t *testing.T) {
	t.Parallel()

	app := &application{client: http.DefaultClient, configPath: filepath.Join(t.TempDir(), "config.json")}
	for _, argument := range []string{"help", "-help", "--help"} {
		var output bytes.Buffer
		exitCode := app.run(context.Background(), []string{"config", argument}, strings.NewReader(""), &output, io.Discard)
		if exitCode != 0 || !strings.Contains(output.String(), "gogogo config set <key> <value>") {
			t.Fatalf("config %s exit code = %d, stdout = %q", argument, exitCode, output.String())
		}
	}
}
