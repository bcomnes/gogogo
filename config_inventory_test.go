package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTemplateName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"web", "Web_2-test", "0", "_", "-"} {
		if err := validateTemplateName(name); err != nil {
			t.Errorf("validateTemplateName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", " ", " web", "web ", "web.app", "web/app", "web#main", "é", "web\n", "a\x00b"} {
		if err := validateTemplateName(name); err == nil {
			t.Errorf("validateTemplateName(%q) succeeded", name)
		}
		for _, operation := range []string{"get", "set", "unset"} {
			cfg := config{}
			var err error
			switch operation {
			case "get":
				_, err = getConfigValue(cfg, "template."+name)
			case "set":
				err = setConfigValue(&cfg, "template."+name, "owner/repo")
			case "unset":
				err = unsetConfigValue(&cfg, "template."+name)
			}
			if err == nil {
				t.Errorf("%s accepted invalid name %q", operation, name)
			}
		}
	}
}

func TestLoadConfigInventory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, content, want, wantError string
	}{
		{name: "legacy", content: `{}`},
		{name: "null", content: `{"templates":null}`},
		{name: "empty", content: `{"templates":{}}`},
		{name: "canonical", content: `{"templates":{"web":"owner/repo#main"}}`, want: "owner/repo#main"},
		{name: "URL", content: `{"templates":{"web":"https://github.com/owner/repo.git#main"}}`, want: "owner/repo#main"},
		{name: "default branch", content: `{"templates":{"web":"owner/repo"}}`, want: "owner/repo#master"},
		{name: "invalid name", content: `{"templates":{"bad.name":"owner/repo"}}`, wantError: "invalid template name"},
		{name: "empty name", content: `{"templates":{"":"owner/repo"}}`, wantError: "invalid template name"},
		{name: "empty ref", content: `{"templates":{"web":""}}`, wantError: `invalid template "web"`},
		{name: "invalid ref", content: `{"templates":{"web":"not-a-repository"}}`, wantError: `invalid template "web"`},
		{name: "invalid type", content: `{"templates":{"web":42}}`, wantError: "decode config"},
		{name: "repeated suffix", content: `{"templates":{"web":"owner/repo.git.git#main"}}`, wantError: `invalid template "web"`},
		{name: "URL repeated suffix", content: `{"templates":{"web":"https://github.com/owner/repo.git.git#main"}}`, wantError: `invalid template "web"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfig(path)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("loadConfig error = %v, want %q", err, test.wantError)
				}
				app := &application{configPath: path}
				runConfigCommand(t, app, "", 1, "validate")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Templates == nil || cfg.Templates["web"] != test.want {
				t.Fatalf("inventory = %#v, want web = %q and writable map", cfg.Templates, test.want)
			}
			if cfg.GitHub != defaultConfig().GitHub {
				t.Fatal("inventory changed default template")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != test.content {
				t.Fatalf("load modified config: %q, %v", data, err)
			}
		})
	}
}

func TestInventoryNilMapAndInvalidUpdate(t *testing.T) {
	t.Parallel()
	cfg := config{}
	if _, err := getConfigValue(cfg, "template.web"); err == nil {
		t.Fatal("missing inventory entry should fail")
	}
	if err := unsetConfigValue(&cfg, "template.web"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "template.web", "git@github.com:owner/repo.git#main"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "template.web", "invalid"); err == nil {
		t.Fatal("invalid replacement succeeded")
	}
	if cfg.Templates["web"] != "owner/repo#main" {
		t.Fatalf("inventory = %#v", cfg.Templates)
	}
	first, second := defaultConfig(), defaultConfig()
	first.Templates["web"] = "owner/repo#main"
	if len(second.Templates) != 0 {
		t.Fatal("defaults share inventory maps")
	}
	missing, err := loadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || missing.Templates == nil {
		t.Fatalf("missing config: %+v, %v", missing, err)
	}
}

func TestLoadConfigStructuredRepository(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, content, wantURL string
	}{
		{"legacy empty branch", `{"github":{"user":"owner","repo":"repo","branch":""},"defaults":{"license":"MIT"}}`, "https://github.com/owner/repo/archive/master.tar.gz"},
		{"legacy omitted branch", `{"github":{"user":"owner","repo":"repo"}}`, "https://github.com/owner/repo/archive/master.tar.gz"},
		{"null github", `{"github":null}`, defaultConfig().GitHub.ArchiveURL()},
		{"null visibility and owner", `{"github_visibility":null,"github_owner":null}`, defaultConfig().GitHub.ArchiveURL()},
		{"literal repository suffix", `{"github":{"user":"owner","repo":"repo.git","branch":"main"}}`, "https://github.com/owner/repo.git/archive/main.tar.gz"},
		{"repeated repository suffix", `{"github":{"user":"owner","repo":"repo.git.git","branch":"main"}}`, ""},
		{"owner separator", `{"github":{"user":"owner/extra","repo":"repo","branch":"main"}}`, ""},
		{"repository separator", `{"github":{"user":"owner","repo":"extra/repo","branch":"main"}}`, ""},
		{"owner colon", `{"github":{"user":"owner:extra","repo":"repo","branch":"main"}}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfig(path)
			app := &application{configPath: path}
			if test.wantURL == "" {
				if err == nil || !strings.Contains(err.Error(), "invalid configured repository") {
					t.Fatalf("loadConfig error = %v", err)
				}
				runConfigCommand(t, app, "", 1, "validate")
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got := cfg.GitHub.ArchiveURL(); got != test.wantURL {
					t.Fatalf("ArchiveURL = %q, want %q", got, test.wantURL)
				}
				runConfigCommand(t, app, "", 0, "validate")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != test.content {
				t.Fatalf("load/validate changed source: %q, %v", data, err)
			}
		})
	}
}

func TestInventoryRepositoryIdentity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	app := &application{configPath: path}
	runConfigCommand(t, app, "", 0, "set", "template.web", "https://github.com/owner/repo.git#main")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"owner/repo.git.git#main", "https://github.com/owner/repo.git.git#main", "git@github.com:owner/repo.git.git#main"} {
		runConfigCommand(t, app, "", 2, "set", "template.web", value)
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("rejected update changed file: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if got := runConfigCommand(t, app, "", 0, "get", "template.web"); got != "owner/repo#main\n" {
			t.Fatalf("get = %q", got)
		}
		runConfigCommand(t, app, "", 0, "set", "parameter.license", "MIT")
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := selectTemplate(context.Background(), options{template: "web"}, cfg, strings.NewReader(""), &bytes.Buffer{})
		if err != nil || selected.GitHub.String() != "owner/repo#main" {
			t.Fatalf("selection = %+v, %v", selected.GitHub, err)
		}
	}
}

func TestConfigInventoryLifecycle(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	app := &application{configPath: path}
	run := func(args ...string) string {
		t.Helper()
		return runConfigCommand(t, app, "", 0, args...)
	}
	run("set", "template", "owner/default#main")
	run("set", "parameter.name", "project")
	run("set", "template.Web_2-test", "https://github.com/owner/web.git#dev")
	run("set", "template.other", "owner/other")
	if got := run("get", "template.Web_2-test"); got != "owner/web#dev\n" {
		t.Fatalf("get = %q", got)
	}
	runConfigCommand(t, app, "", 2, "get", "template.web_2-test")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runConfigCommand(t, app, "", 2, "set", "template.Web_2-test", "bad")
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("invalid update changed file: %v", err)
	}
	var shown config
	if err := json.Unmarshal([]byte(run("show")), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Templates["Web_2-test"] != "owner/web#dev" || shown.Templates["other"] != "owner/other#master" {
		t.Fatalf("shown inventory = %#v", shown.Templates)
	}
	run("unset", "template.Web_2-test")
	run("unset", "template.Web_2-test")
	if got := run("get", "template"); got != "owner/default#main\n" {
		t.Fatalf("default changed: %q", got)
	}
	run("unset", "template")
	if got := run("get", "template.other"); got != "owner/other#master\n" {
		t.Fatalf("inventory changed: %q", got)
	}
	run("unset", "template.other")
	if strings.Contains(run("show"), `"templates"`) {
		t.Fatal("empty inventory was not omitted")
	}
	if got := run("get", "parameter.name"); got != "project\n" {
		t.Fatalf("parameter changed: %q", got)
	}
	run("set", "template.web", "owner/web")
	run("reset", "--force")
	runConfigCommand(t, app, "", 2, "get", "template.web")
}
