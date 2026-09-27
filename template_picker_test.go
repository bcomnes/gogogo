package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectTemplate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		opts      options
		input     string
		want      string
		wantError bool
	}{
		{"unchanged", options{}, "", "bcomnes/go-template#master", false},
		{"named", options{template: "cli"}, "", "acme/cli#main", false},
		{"missing", options{template: "missing"}, "", "", true},
		{"default", options{pickTemplate: true}, "\n", "bcomnes/go-template#master", false},
		{"sorted choice", options{pickTemplate: true}, "2\n", "acme/cli#main", false},
		{"retry", options{pickTemplate: true}, "bad\n0\n99\n3\n", "acme/web#main", false},
		{"EOF", options{pickTemplate: true}, "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Templates = map[string]string{"web": "acme/web#main", "cli": "acme/cli#main"}
			var output bytes.Buffer
			selected, err := selectTemplate(context.Background(), test.opts, cfg, strings.NewReader(test.input), &output)
			if test.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || selected.GitHub.String() != test.want {
				t.Fatalf("selected = %s, error = %v", selected.GitHub.String(), err)
			}
			if cfg.GitHub.String() != "bcomnes/go-template#master" {
				t.Fatal("mutated default")
			}
		})
	}
}

func TestTemplatePickerCancellation(t *testing.T) {
	t.Parallel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel when the prompt is written, before input becomes available.
	output := cancelPromptWriter{cancel: cancel}
	_, err := selectTemplate(ctx, options{pickTemplate: true}, defaultConfig(), reader, output)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

type cancelPromptWriter struct{ cancel context.CancelFunc }

func (w cancelPromptWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "Selection") {
		w.cancel()
	}
	return len(p), nil
}

// cancelReadReader cancels only after selection has started reading input.
type cancelReadReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelReadReader) Read(p []byte) (int, error) {
	r.cancel()
	return r.Reader.Read(p)
}

func TestTemplatePickerCancelsPendingRead(t *testing.T) {
	t.Parallel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := selectTemplate(ctx, options{pickTemplate: true}, defaultConfig(), cancelReadReader{Reader: reader, cancel: cancel}, io.Discard)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("picker did not cancel its pending read")
	}
}

func TestTemplateSelectionConflicts(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"-template=", "example"},
		{"-template=bad/name", "example"},
		{"-template=cli", "-pick-template", "example"},
		{"-template=cli", "-file=archive.tar", "example"},
		{"-pick-template", "-url=https://example.test/a.tar", "example"},
		{"-pick-template", "example", "owner/repo"},
		{"-template=cli", "example", "main"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestApplicationUsesInventoryTemplate(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-template=cli", "-pick-template"} {
		t.Run(flag, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			cfg := defaultConfig()
			cfg.Templates = map[string]string{"cli": "acme/cli#main"}
			if err := saveConfig(path, cfg); err != nil {
				t.Fatal(err)
			}
			archive := makeTestArchive(t, true, []testArchiveEntry{
				{name: "cli-main/README.md", body: "# {{name}}\n", mode: 0o644, typeflag: tar.TypeReg},
			})
			app := &application{configPath: path, client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if got := request.URL.String(); got != "https://github.com/acme/cli/archive/main.tar.gz" {
					t.Errorf("archive URL = %q", got)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
			})}}
			destination := filepath.Join(dir, "example")
			var stderr bytes.Buffer
			if code := app.run(context.Background(), []string{flag, "-no-git", destination}, strings.NewReader("2\n"), io.Discard, &stderr); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, &stderr)
			}
			content, err := os.ReadFile(filepath.Join(destination, "README.md"))
			if err != nil || string(content) != "# example\n" {
				t.Fatalf("content = %q, error = %v", content, err)
			}
			stored, err := loadConfig(path)
			if err != nil || stored.GitHub != cfg.GitHub {
				t.Fatalf("default changed: %v, error = %v", stored.GitHub, err)
			}
		})
	}
}

func TestPickerWithEmptyInventory(t *testing.T) {
	t.Parallel()
	cfg := defaultConfig()
	selected, err := selectTemplate(context.Background(), options{pickTemplate: true}, cfg, strings.NewReader("\n"), io.Discard)
	if err != nil || selected.GitHub != cfg.GitHub {
		t.Fatalf("selection = %v, error = %v", selected.GitHub, err)
	}
}

func TestPickerEOFDoesNotCreateProject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app := &application{configPath: filepath.Join(dir, "config.json"), client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("download started before selection")
		return nil, nil
	})}}
	destination := filepath.Join(dir, "example")
	code := app.run(context.Background(), []string{"-pick-template", destination}, strings.NewReader(""), io.Discard, io.Discard)
	if code == 0 {
		t.Fatal("EOF succeeded")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination: %v", err)
	}
}
