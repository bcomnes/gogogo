package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenTemplateRemoteSources(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		opts options
		url  string
	}{
		{"default", options{positionals: []string{"example"}}, "https://github.com/bcomnes/go-template/archive/master.tar.gz"},
		{"branch", options{positionals: []string{"example", "next"}}, "https://github.com/bcomnes/go-template/archive/next.tar.gz"},
		{"repository", options{positionals: []string{"example", "acme/template#main"}}, "https://github.com/acme/template/archive/main.tar.gz"},
		{"URL", options{url: "https://example.test/template.tar"}, "https://example.test/template.tar"},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := &application{client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != test.url {
					t.Fatalf("requested URL = %q, want %q", request.URL, test.url)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("template"))}, nil
			})}}
			source, label, err := app.openTemplate(context.Background(), test.opts, defaultConfig(), io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			body, err := io.ReadAll(source)
			if err != nil || string(body) != "template" || label == "" {
				t.Fatalf("body = %q, label = %q, error = %v", body, label, err)
			}
		})
	}
}

func TestOpenTemplateDownloadFailure(t *testing.T) {
	t.Parallel()
	app := &application{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("missing archive"))}, nil
	})}}
	source, _, err := app.openTemplate(context.Background(), options{url: "https://example.test/template.tar"}, defaultConfig(), io.Discard)
	if source != nil || err == nil || !strings.Contains(err.Error(), "open template https://example.test/template.tar") || !strings.Contains(err.Error(), "missing archive") {
		t.Fatalf("source = %v, error = %v", source, err)
	}
}

func TestOpenTemplateRejectsInvalidBranchShorthand(t *testing.T) {
	t.Parallel()
	for _, branch := range []string{"", "bad\nbranch"} {
		t.Run(branch, func(t *testing.T) {
			app := &application{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("unexpected download")
				return nil, nil
			})}}
			source, label, err := app.openTemplate(context.Background(), options{positionals: []string{"example", branch}}, defaultConfig(), io.Discard)
			if source != nil || label != "" || err == nil || !strings.Contains(err.Error(), "repository branch") {
				t.Fatalf("source = %v, label = %q, error = %v", source, label, err)
			}
		})
	}
}
