package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOptions(t *testing.T) {
	t.Parallel()

	opts, err := parseOptions([]string{"-set", "owner=bret", "-set=license=MIT", "example", "owner/template#main"})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if len(opts.positionals) != 2 || opts.positionals[0] != "example" || opts.positionals[1] != "owner/template#main" {
		t.Fatalf("positionals = %#v", opts.positionals)
	}
	if opts.values["owner"] != "bret" || opts.values["license"] != "MIT" {
		t.Fatalf("values = %#v", opts.values)
	}
}

func TestParseOptionsRejectsConflictingSources(t *testing.T) {
	t.Parallel()

	if _, err := parseOptions([]string{"-file", "template.tar", "-url", "https://example.test/template.tar", "example"}); err == nil {
		t.Fatal("parseOptions() unexpectedly succeeded")
	}
}

func TestParseOptionsRejectsFlagsAfterProjectName(t *testing.T) {
	t.Parallel()

	if _, err := parseOptions([]string{"example", "-set", "owner=bret"}); err == nil {
		t.Fatal("parseOptions() unexpectedly succeeded")
	}
}

func TestParseOptionsRejectsInvalidRepositoryOptions(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"-github=secret", "example"},
		{"-no-git", "-github=private", "example"},
		{"-no-github", "-github=private", "example"},
	} {
		if _, err := parseOptions(arguments); err == nil {
			t.Errorf("parseOptions(%q) unexpectedly succeeded", arguments)
		}
	}
}

func TestResolveGitHubOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		opts       options
		visibility string
		want       string
		wantError  bool
	}{
		{name: "configured default", visibility: "private", want: "private"},
		{name: "explicit override", opts: options{github: "public"}, visibility: "private", want: "public"},
		{name: "per-run opt out", opts: options{noGitHub: true}, visibility: "private"},
		{name: "no git implies no GitHub", opts: options{noGit: true}, visibility: "private"},
		{name: "owner uses configured default", opts: options{githubOwner: "acme"}, visibility: "private", want: "private"},
		{name: "owner without repository creation", opts: options{githubOwner: "acme"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := resolveGitHubOptions(test.opts, config{GitHubVisibility: test.visibility})
			if test.wantError {
				if err == nil {
					t.Fatal("resolveGitHubOptions() unexpectedly succeeded")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveGitHubOptions() error = %v", err)
			}
			if resolved.github != test.want {
				t.Fatalf("github visibility = %q, want %q", resolved.github, test.want)
			}
		})
	}
}

func TestApplicationSetsDefaultGitHubVisibility(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	app := &application{client: http.DefaultClient, configPath: path}
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "private", want: "private"},
		{value: "none", want: ""},
	} {
		var output bytes.Buffer
		var errorOutput bytes.Buffer
		exitCode := app.run(context.Background(), []string{"-default-github=" + test.value}, strings.NewReader(""), &output, &errorOutput)
		if exitCode != 0 {
			t.Fatalf("run(-default-github=%s) exit code = %d, stderr = %q", test.value, exitCode, errorOutput.String())
		}
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}
		if cfg.GitHubVisibility != test.want {
			t.Fatalf("configured visibility = %q, want %q", cfg.GitHubVisibility, test.want)
		}
	}

	var errorOutput bytes.Buffer
	if exitCode := app.run(context.Background(), []string{"-default-github=secret"}, strings.NewReader(""), io.Discard, &errorOutput); exitCode != 2 {
		t.Fatalf("invalid visibility exit code = %d, stderr = %q", exitCode, errorOutput.String())
	}
}

func TestApplicationHelp(t *testing.T) {
	t.Parallel()

	app := &application{client: http.DefaultClient, configPath: filepath.Join(t.TempDir(), "config.json")}
	var output bytes.Buffer
	exitCode := app.run(context.Background(), []string{"-help"}, strings.NewReader(""), &output, io.Discard)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d", exitCode)
	}
	if !strings.Contains(output.String(), "Usage:") ||
		!strings.Contains(output.String(), "-set value") ||
		!strings.Contains(output.String(), "Configured GitHub visibility: none") {
		t.Fatalf("help output = %q", output.String())
	}
}

func TestApplicationCreatesProjectFromFile(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	archivePath := filepath.Join(temporaryDirectory, "template.tar.gz")
	archive := makeTestArchive(t, true, []testArchiveEntry{
		{name: "template/go.mod", body: "module github.com/{{owner}}/__name__\n", mode: 0o644, typeflag: tar.TypeReg},
		{name: "template/main.go", body: "package __name__\n", mode: 0o644, typeflag: tar.TypeReg},
	})
	if err := os.WriteFile(archivePath, archive, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	destination := filepath.Join(temporaryDirectory, "example")
	runner := newTestProjectCommandRunner(t, localGitCommandSteps()...)
	app := &application{
		client:     http.DefaultClient,
		configPath: filepath.Join(temporaryDirectory, "config.json"),
		commands:   runner,
	}
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	exitCode := app.run(context.Background(), []string{"-file", archivePath, "-set", "owner=bret", destination}, strings.NewReader(""), &output, &errorOutput)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %s", exitCode, errorOutput.String())
	}

	module, err := os.ReadFile(filepath.Join(destination, "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(module) != "module github.com/bret/example\n" {
		t.Fatalf("go.mod = %q", module)
	}
	if !strings.Contains(output.String(), "Creating new project example") ||
		!strings.Contains(output.String(), "Project created in ") ||
		!strings.Contains(output.String(), "Initializing Git repository...") ||
		!strings.Contains(output.String(), "Created initial commit") {
		t.Fatalf("stdout = %q", output.String())
	}
	runner.done()
}

func TestApplicationUsesConfiguredGitHubVisibility(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	archivePath := filepath.Join(temporaryDirectory, "template.tar")
	archive := makeTestArchive(t, false, []testArchiveEntry{
		{name: "template/README.md", body: "# __name__\n", mode: 0o644, typeflag: tar.TypeReg},
	})
	if err := os.WriteFile(archivePath, archive, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	configPath := filepath.Join(temporaryDirectory, "config.json")
	cfg := defaultConfig()
	cfg.GitHubVisibility = "private"
	if err := saveConfig(configPath, cfg); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}

	commands := localGitCommandSteps()
	commands = append(commands,
		testProjectCommand{name: "gh", args: []string{"auth", "status", "--hostname", "github.com"}},
		testProjectCommand{name: "gh", args: []string{"repo", "create", "example", "--private", "--source=.", "--remote=origin", "--push"}},
	)
	runner := newTestProjectCommandRunner(t, commands...)
	app := &application{client: http.DefaultClient, configPath: configPath, commands: runner}
	var errorOutput bytes.Buffer
	exitCode := app.run(
		context.Background(),
		[]string{"-file", archivePath, filepath.Join(temporaryDirectory, "example")},
		strings.NewReader(""),
		io.Discard,
		&errorOutput,
	)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, errorOutput.String())
	}
	runner.done()
}

func TestApplicationUsesDefaultTemplate(t *testing.T) {
	t.Parallel()

	archive := makeTestArchive(t, true, []testArchiveEntry{
		{name: "go-template-master/README.md", body: "# {{name}}\n", mode: 0o644, typeflag: tar.TypeReg},
	})
	var requestedURL string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestedURL = request.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(archive)),
			Request:    request,
		}, nil
	})}

	temporaryDirectory := t.TempDir()
	destination := filepath.Join(temporaryDirectory, "example")
	app := &application{
		client:     client,
		configPath: filepath.Join(temporaryDirectory, "config.json"),
	}
	var errorOutput bytes.Buffer
	exitCode := app.run(context.Background(), []string{"-no-git", destination}, strings.NewReader(""), io.Discard, &errorOutput)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %s", exitCode, errorOutput.String())
	}

	const wantedURL = "https://github.com/bcomnes/go-template/archive/master.tar.gz"
	if requestedURL != wantedURL {
		t.Fatalf("requested URL = %q, want %q", requestedURL, wantedURL)
	}
	readme, err := os.ReadFile(filepath.Join(destination, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(readme) != "# example\n" {
		t.Fatalf("README.md = %q", readme)
	}
}

func TestConfigureAndLoad(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "config.json")
	input := strings.NewReader("owner/template#main\nprivate\nowner=bret\n\n")
	if err := configure(input, io.Discard, path, defaultConfig()); err != nil {
		t.Fatalf("configure() error = %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.GitHub.String() != "owner/template#main" {
		t.Fatalf("repository = %q", cfg.GitHub.String())
	}
	if cfg.GitHubVisibility != "private" {
		t.Fatalf("GitHub visibility = %q", cfg.GitHubVisibility)
	}
	if cfg.Defaults["owner"] != "bret" {
		t.Fatalf("defaults = %#v", cfg.Defaults)
	}
}

type testArchiveEntry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
}

func makeTestArchive(t *testing.T, compressed bool, entries []testArchiveEntry) []byte {
	t.Helper()

	var result bytes.Buffer
	var destination io.Writer = &result
	var gzipWriter *gzip.Writer
	if compressed {
		gzipWriter = gzip.NewWriter(&result)
		destination = gzipWriter
	}
	tarWriter := tar.NewWriter(destination)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: entry.typeflag}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader() error = %v", err)
		}
		if _, err := io.WriteString(tarWriter, entry.body); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("tar Close() error = %v", err)
	}
	if gzipWriter != nil {
		if err := gzipWriter.Close(); err != nil {
			t.Fatalf("gzip Close() error = %v", err)
		}
	}
	return result.Bytes()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
