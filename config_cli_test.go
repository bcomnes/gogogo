package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runConfigCommand(t *testing.T, app *application, input string, wantExitCode int, arguments ...string) string {
	t.Helper()
	var output, errorOutput bytes.Buffer
	exitCode := app.run(context.Background(), append([]string{"config"}, arguments...), strings.NewReader(input), &output, &errorOutput)
	if exitCode != wantExitCode {
		t.Fatalf("gogogo config %q exit code = %d, want %d; stdout = %q, stderr = %q", arguments, exitCode, wantExitCode, output.String(), errorOutput.String())
	}
	return output.String()
}

func TestConfigSubcommandLifecycle(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	app := &application{client: http.DefaultClient, configPath: path}
	run := func(arguments ...string) string {
		t.Helper()
		return runConfigCommand(t, app, "", 0, arguments...)
	}

	if got := strings.TrimSpace(run("path")); got != path {
		t.Fatalf("config path = %q, want %q", got, path)
	}
	run("set", "template", "owner/template#main")
	run("set", "github.visibility", "private")
	run("set", "github.owner", "acme")
	run("set", "parameter.license", "MIT")

	for key, want := range map[string]string{
		"template":          "owner/template#main",
		"github.visibility": "private",
		"github.owner":      "acme",
		"parameter.license": "MIT",
	} {
		if got := strings.TrimSpace(run("get", key)); got != want {
			t.Fatalf("config get %s = %q, want %q", key, got, want)
		}
	}
	if output := run("validate"); !strings.Contains(output, "Configuration is valid") {
		t.Fatalf("config validate output = %q", output)
	}

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
		{},
		{"unknown"},
		{"show", "extra"},
		{"get"},
		{"get", "unknown"},
		{"get", "parameter.missing"},
		{"get", "parameter. "},
		{"path", "extra"},
		{"set", "github.visibility"},
		{"set", "github.visibility", "secret"},
		{"set", "github.owner", "bad/owner"},
		{"set", "parameter.", "value"},
		{"set", "parameter. ", "value"},
		{"set", "parameter.owner", ""},
		{"set", "unknown", "value"},
		{"unset"},
		{"unset", "unknown"},
		{"unset", "parameter. "},
		{"validate", "extra"},
		{"reset", "unexpected"},
	}
	for _, arguments := range tests {
		t.Run("config "+strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			app := &application{client: http.DefaultClient, configPath: filepath.Join(t.TempDir(), "config.json")}
			runConfigCommand(t, app, "", 2, arguments...)
		})
	}
}

func TestConfigSubcommandReset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arguments  []string
		input      string
		wantReset  bool
		wantOutput string
	}{
		{name: "declined", input: "n\n", wantOutput: "Configuration unchanged"},
		{name: "confirmed", input: "yes\n", wantReset: true, wantOutput: "Configuration reset"},
		{name: "confirmed at EOF", input: " YeS ", wantReset: true, wantOutput: "Configuration reset"},
		{name: "empty EOF", wantOutput: "Configuration unchanged"},
		{name: "forced", arguments: []string{"--force"}, wantReset: true, wantOutput: "Configuration reset"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.json")
			cfg := defaultConfig()
			cfg.GitHubVisibility = "private"
			if err := saveConfig(path, cfg); err != nil {
				t.Fatalf("saveConfig() error = %v", err)
			}
			app := &application{client: http.DefaultClient, configPath: path}
			output := runConfigCommand(t, app, test.input, 0, append([]string{"reset"}, test.arguments...)...)
			if !strings.Contains(output, test.wantOutput) {
				t.Fatalf("stdout = %q", output)
			}
			loaded, err := loadConfig(path)
			if err != nil {
				t.Fatalf("loadConfig() error = %v", err)
			}
			wantVisibility := "private"
			if test.wantReset {
				wantVisibility = ""
			}
			if loaded.GitHubVisibility != wantVisibility {
				t.Fatalf("visibility after reset = %q, want %q", loaded.GitHubVisibility, wantVisibility)
			}
		})
	}
}

func TestConfigSubcommandValidateRejectsMalformedConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	app := &application{client: http.DefaultClient, configPath: path}
	runConfigCommand(t, app, "", 1, "validate")
}

func TestConfigSubcommandHelp(t *testing.T) {
	t.Parallel()

	app := &application{client: http.DefaultClient, configPath: filepath.Join(t.TempDir(), "config.json")}
	for _, argument := range []string{"help", "-help", "--help"} {
		output := runConfigCommand(t, app, "", 0, argument)
		if !strings.Contains(output, "gogogo config set <key> <value>") ||
			!strings.Contains(output, "gogogo config reset [--force]") {
			t.Fatalf("config %s stdout = %q", argument, output)
		}
	}
}
