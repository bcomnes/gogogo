# goproject

[![Actions Status][action-img]][action-url]
[![PkgGoDev][pkg-go-dev-img]][pkg-go-dev-url]

[action-img]: https://github.com/bcomnes/goproject/actions/workflows/test.yml/badge.svg
[action-url]: https://github.com/bcomnes/goproject/actions/workflows/test.yml
[pkg-go-dev-img]: https://pkg.go.dev/badge/github.com/bcomnes/goproject/pkg
[pkg-go-dev-url]: https://pkg.go.dev/github.com/bcomnes/goproject/pkg

`goproject` is a command and library for creating projects from tar-based templates.
It is a Go port of [`mafintosh/create-project`](https://github.com/mafintosh/create-project).
The default template is [`bcomnes/go-template`](https://github.com/bcomnes/go-template) on its `master` branch.

## Install

Install the standalone command with [Homebrew](https://brew.sh):

```console
brew install bcomnes/tap/goproject
```

This adds the `bcomnes/tap` tap automatically.
Update it later with `brew upgrade goproject`.

Alternatively, install the latest source with Go:

```console
go install github.com/bcomnes/goproject@latest
```

## Command-line usage

Create a project from the default Go template:

```console
goproject my-project
```

Create a project from another GitHub repository and branch:

```console
goproject my-project owner/template#main
```

A configured repository also accepts a branch name as shorthand:

```console
goproject my-project next
```

Flags must appear before the project name, matching the standard Go `flag` package convention.
Pass template parameters with repeatable `-set key=value` flags:

```console
goproject -set owner=bcomnes -set license=MIT my-project
```

Use a local uncompressed or gzip-compressed tar archive:

```console
goproject -file template.tar.gz my-project
```

Download an archive from an HTTP or HTTPS URL:

```console
goproject -url https://example.com/template.tar.gz my-project
```

Run `goproject -help` for complete help text and flag descriptions.

### Template placeholders

Text files may use both `{{key}}` and `__key__` placeholders.
The `name` parameter defaults to the destination directory's base name.
Unknown placeholders are left unchanged.
Use `__name__` in files that must remain syntactically valid before generation.
For example, a template `go.mod` can contain:

```go
module github.com/bcomnes/__name__
```

Likewise, a Go package declaration can contain `package __name__`.
Binary files are detected from their media type and complete contents, then copied byte-for-byte without placeholder substitution.

### Configuration

Set a default repository and default template parameters interactively:

```console
goproject -configure
```

Configuration is stored in `~/.config/goproject.json` by default.
Set `GOPROJECT_CONFIG` to use a different configuration path.

### Extraction behavior

The destination must not already exist.
Files are extracted into a private staging directory and published atomically.
The common top-level directory in the archive is removed.
Unsafe paths, escaping links, duplicate paths, and unsupported archive entries are rejected.

## Library usage

The implementation is available from `github.com/bcomnes/goproject/pkg`.

```go
package main

import (
	"context"
	"log"
	"os"

	goproject "github.com/bcomnes/goproject/pkg"
)

func main() {
	archive, err := os.Open("template.tar.gz")
	if err != nil {
		log.Fatal(err)
	}
	defer archive.Close()

	project, err := goproject.Create(
		context.Background(),
		"my-project",
		archive,
		goproject.Options{
			Parameters: map[string]string{"owner": "bcomnes"},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("created %s in %s", project.Name, project.Destination)
}
```

Repository strings can be parsed with `goproject.ParseRepository`, and `Repository.ArchiveURL` returns the corresponding GitHub archive URL.

## License

MIT
