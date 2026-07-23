package goproject

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
)

// Options controls project creation.
type Options struct {
	// Parameters contains placeholder values.
	// The name parameter defaults to the destination directory name.
	Parameters map[string]string
}

// Project describes a created project.
type Project struct {
	Name        string
	Destination string
}

// Create extracts a tar or gzip-compressed tar template into destination.
//
// The archive must contain a common top-level directory, which Create strips.
// Placeholder substitution is applied only to text files.
func Create(ctx context.Context, destination string, archive io.Reader, options Options) (Project, error) {
	if archive == nil {
		return Project{}, fmt.Errorf("template archive is required")
	}

	name := filepath.Base(filepath.Clean(destination))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return Project{}, fmt.Errorf("project name %q is invalid", destination)
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return Project{}, fmt.Errorf("resolve destination: %w", err)
	}

	values := make(map[string]string, len(options.Parameters)+1)
	values["name"] = name
	for key, value := range options.Parameters {
		values[key] = value
	}

	if err := extractArchive(ctx, archive, absoluteDestination, values); err != nil {
		return Project{}, err
	}
	return Project{Name: name, Destination: absoluteDestination}, nil
}
