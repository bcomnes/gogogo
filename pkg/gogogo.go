package gogogo

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
)

// Options controls project creation.
// Its zero value uses only the destination-derived name placeholder.
type Options struct {
	// Parameters contains placeholder values.
	// The name parameter defaults to the destination directory name.
	// An explicit name value overrides substitution but not Project.Name.
	// Empty keys are ignored during substitution, and unknown placeholders remain
	// unchanged. Values are inserted literally, without escaping for the target
	// file's language or syntax. Create copies the map; callers must not mutate it
	// concurrently with that copy.
	Parameters map[string]string
}

// Project describes a created project.
// It is returned only after extraction and publication succeed; Create returns
// the zero value on error.
type Project struct {
	// Name is the cleaned destination's base name, independent of Parameters.
	Name string
	// Destination is the absolute path at which the project was published.
	Destination string
}

// Create extracts a tar or gzip-compressed tar template into destination.
//
// The archive must contain a common top-level directory, which Create strips.
// Placeholder substitution is applied only to text files.
//
// destination must not already exist, including as a dangling symlink. Missing
// parent directories are created and may remain after an error. Extraction uses a
// sibling staging directory, with best-effort cleanup on failure, and publishes
// by rename only after extraction succeeds. Callers must prevent concurrent
// changes to the destination and its parents; the existence check and publication
// are not an atomic no-replace operation.
//
// archive must be non-nil and remains owned by the caller; Create never closes it.
// ctx must be non-nil. Cancellation is checked between archive entries and before
// content reads, but cannot interrupt an underlying Read already in progress.
// Some filesystem work, text classification, and gzip draining do not check ctx.
//
// File contents are processed incrementally using disk-backed staging rather than
// loading the complete archive into memory. No size, entry-count, or expansion
// limits are imposed. Paths and link targets are not subject to substitution.
// Regular-file and directory permission bits and nonzero modification times are
// restored; ownership and special mode bits are not.
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
