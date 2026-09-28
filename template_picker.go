package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	gogogo "github.com/bcomnes/gogogo/pkg"
)

// selectTemplate applies a named or interactive template choice to this run only.
// It never saves configuration or opens an archive. With neither flag, it does
// not read input and leaves the configured default unchanged.
func selectTemplate(ctx context.Context, opts options, cfg config, input io.Reader, output io.Writer) (config, error) {
	if opts.template != "" {
		reference, found := cfg.Templates[opts.template]
		if !found {
			return cfg, fmt.Errorf("unknown saved template %q; use config show to list templates", opts.template)
		}
		repo, err := gogogo.ParseRepository(reference)
		if err != nil {
			return cfg, fmt.Errorf("saved template %q: %w", opts.template, err)
		}
		cfg.GitHub = repo
		return cfg, nil
	}
	if !opts.pickTemplate {
		return cfg, nil
	}

	names := slices.Sorted(maps.Keys(cfg.Templates))
	fmt.Fprintf(output, "Choose a template:\n  1. Default — %s\n", cfg.GitHub.String())
	for index, name := range names {
		fmt.Fprintf(output, "  %d. %s — %s\n", index+2, name, cfg.Templates[name])
	}
	reader := bufio.NewReader(input)
	for {
		fmt.Fprint(output, "Selection [1]: ")
		line, err := readTemplateSelection(ctx, reader)
		if err != nil {
			return cfg, fmt.Errorf("template selection canceled: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" || line == "1" {
			return cfg, nil
		}
		selection, err := strconv.Atoi(line)
		if err != nil || selection < 2 || selection > len(names)+1 {
			fmt.Fprintf(output, "Enter a number from 1 to %d.\n", len(names)+1)
			continue
		}
		return selectTemplate(ctx, options{template: names[selection-2]}, cfg, input, output)
	}
}

// readTemplateSelection lets cancellation interrupt a pending prompt without
// closing caller-owned stdin. If input blocks, the read goroutine finishes when
// that reader next produces data or is closed; the CLI itself can exit promptly.
// EOF is cancellation, not an implicit choice of the default template.
func readTemplateSelection(ctx context.Context, reader *bufio.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	type result struct {
		line string
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		line, err := reader.ReadString('\n')
		ready <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case value := <-ready:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return value.line, value.err
	}
}
