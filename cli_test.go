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
	app := &application{
		client:     http.DefaultClient,
		configPath: filepath.Join(temporaryDirectory, "config.json"),
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
	exitCode := app.run(context.Background(), []string{destination}, strings.NewReader(""), io.Discard, &errorOutput)
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
