package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogogo "github.com/bcomnes/gogogo/pkg"
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
		{"-github-owner=acme", "example"},
		{"-no-git", "-github=private", "example"},
	} {
		if _, err := parseOptions(arguments); err == nil {
			t.Errorf("parseOptions(%q) unexpectedly succeeded", arguments)
		}
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
	if !strings.Contains(output.String(), "Usage:") || !strings.Contains(output.String(), "-set value") {
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
	runner := &testProjectCommandRunner{outputs: [][]byte{nil, nil, nil}}
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
	if !strings.Contains(output.String(), "Creating new project example") || !strings.Contains(output.String(), "Project created in ") {
		t.Fatalf("stdout = %q", output.String())
	}
	assertProjectCommands(t, runner.calls, destination, []testProjectCommand{
		{name: "git", args: []string{"init"}},
		{name: "git", args: []string{"add", "--all"}},
		{name: "git", args: []string{"commit", "--allow-empty", "-m", "Initial commit"}},
	})
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

func TestInitializeRepository(t *testing.T) {
	t.Parallel()

	destination := t.TempDir()
	runner := &testProjectCommandRunner{outputs: [][]byte{nil, nil, nil}}
	app := &application{commands: runner}
	var output bytes.Buffer
	if err := app.initializeRepository(context.Background(), projectForTest(destination), options{}, &output); err != nil {
		t.Fatalf("initializeRepository() error = %v", err)
	}

	assertProjectCommands(t, runner.calls, destination, []testProjectCommand{
		{name: "git", args: []string{"init"}},
		{name: "git", args: []string{"add", "--all"}},
		{name: "git", args: []string{"commit", "--allow-empty", "-m", "Initial commit"}},
	})
	if !strings.Contains(output.String(), "Initialized Git repository") || !strings.Contains(output.String(), "Created initial commit") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestInitializeRepositoryCreatesGitHubRepository(t *testing.T) {
	t.Parallel()

	destination := t.TempDir()
	runner := &testProjectCommandRunner{outputs: [][]byte{nil, nil, nil, nil, []byte("https://github.com/acme/example\n")}}
	app := &application{commands: runner}
	var output bytes.Buffer
	opts := options{github: "public", githubOwner: "acme"}
	if err := app.initializeRepository(context.Background(), projectForTest(destination), opts, &output); err != nil {
		t.Fatalf("initializeRepository() error = %v", err)
	}

	assertProjectCommands(t, runner.calls, destination, []testProjectCommand{
		{name: "git", args: []string{"init"}},
		{name: "git", args: []string{"add", "--all"}},
		{name: "git", args: []string{"commit", "--allow-empty", "-m", "Initial commit"}},
		{name: "gh", args: []string{"auth", "status", "--hostname", "github.com"}},
		{name: "gh", args: []string{"repo", "create", "acme/example", "--public", "--source=.", "--remote=origin", "--push"}},
	})
	if runner.lookedUp != "gh" || !strings.Contains(output.String(), "https://github.com/acme/example") {
		t.Fatalf("lookup = %q, output = %q", runner.lookedUp, output.String())
	}
}

func TestInitializeRepositorySkipsUnavailableGitHubCLI(t *testing.T) {
	t.Parallel()

	destination := t.TempDir()
	runner := &testProjectCommandRunner{outputs: [][]byte{nil, nil, nil}, lookErr: errors.New("not found")}
	app := &application{commands: runner}
	var output bytes.Buffer
	if err := app.initializeRepository(context.Background(), projectForTest(destination), options{github: "private"}, &output); err != nil {
		t.Fatalf("initializeRepository() error = %v", err)
	}
	if len(runner.calls) != 3 || !strings.Contains(output.String(), "gh was not found") {
		t.Fatalf("calls = %#v, output = %q", runner.calls, output.String())
	}
}

func TestInitializeRepositorySkipsUnauthenticatedGitHubCLI(t *testing.T) {
	t.Parallel()

	destination := t.TempDir()
	runner := &testProjectCommandRunner{
		outputs: [][]byte{nil, nil, nil, []byte("not logged in")},
		errors:  []error{nil, nil, nil, errors.New("exit 1")},
	}
	app := &application{commands: runner}
	var output bytes.Buffer
	if err := app.initializeRepository(context.Background(), projectForTest(destination), options{github: "private"}, &output); err != nil {
		t.Fatalf("initializeRepository() error = %v", err)
	}
	if len(runner.calls) != 4 || !strings.Contains(output.String(), "gh is not authenticated") || !strings.Contains(output.String(), "not logged in") {
		t.Fatalf("calls = %#v, output = %q", runner.calls, output.String())
	}
}

func TestInitializeRepositoryPropagatesCancellation(t *testing.T) {
	t.Parallel()

	destination := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &testProjectCommandRunner{errors: []error{errors.New("signal: killed")}}
	app := &application{commands: runner}
	if err := app.initializeRepository(ctx, projectForTest(destination), options{}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("initializeRepository() error = %v", err)
	}
}

func TestInitializeRepositoryRejectsTemplateGitMetadata(t *testing.T) {
	t.Parallel()

	destination := t.TempDir()
	if err := os.Mkdir(filepath.Join(destination, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	runner := &testProjectCommandRunner{}
	app := &application{commands: runner}
	if err := app.initializeRepository(context.Background(), projectForTest(destination), options{}, io.Discard); err == nil || !strings.Contains(err.Error(), "template contains") {
		t.Fatalf("initializeRepository() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("commands were invoked: %#v", runner.calls)
	}
}

func TestConfigureAndLoad(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "config.json")
	input := strings.NewReader("owner/template#main\nowner=bret\n\n")
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
	if cfg.Defaults["owner"] != "bret" {
		t.Fatalf("defaults = %#v", cfg.Defaults)
	}
}

type testProjectCommand struct {
	dir  string
	name string
	args []string
}

type testProjectCommandRunner struct {
	calls    []testProjectCommand
	outputs  [][]byte
	errors   []error
	lookErr  error
	lookedUp string
}

func (runner *testProjectCommandRunner) LookPath(name string) (string, error) {
	runner.lookedUp = name
	if runner.lookErr != nil {
		return "", runner.lookErr
	}
	return "/usr/local/bin/" + name, nil
}

func (runner *testProjectCommandRunner) Run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	index := len(runner.calls)
	runner.calls = append(runner.calls, testProjectCommand{dir: dir, name: name, args: append([]string(nil), args...)})
	var output []byte
	if index < len(runner.outputs) {
		output = runner.outputs[index]
	}
	var err error
	if index < len(runner.errors) {
		err = runner.errors[index]
	}
	return output, err
}

func assertProjectCommands(t *testing.T, actual []testProjectCommand, destination string, expected []testProjectCommand) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("command count = %d, want %d: %#v", len(actual), len(expected), actual)
	}
	for index := range expected {
		if actual[index].dir != destination || actual[index].name != expected[index].name || strings.Join(actual[index].args, "\x00") != strings.Join(expected[index].args, "\x00") {
			t.Fatalf("command %d = %#v, want dir %q command %#v", index, actual[index], destination, expected[index])
		}
	}
}

func projectForTest(destination string) gogogo.Project {
	return gogogo.Project{Name: "example", Destination: destination}
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
