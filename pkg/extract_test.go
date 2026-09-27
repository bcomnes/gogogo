package gogogo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testArchiveEntry struct {
	name     string
	body     string
	data     []byte
	mode     int64
	typeflag byte
	linkname string
}

func (entry testArchiveEntry) content() []byte {
	if entry.data != nil {
		return entry.data
	}
	return []byte(entry.body)
}

func TestCreate(t *testing.T) {
	t.Parallel()

	for _, compressed := range []bool{false, true} {
		name := "tar"
		if compressed {
			name = "tar.gz"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			binary := []byte{0x00, 0x01, '{', '{', 'n', 'a', 'm', 'e', '}', '}', 0xff}
			pdf := []byte("%PDF-1.7\n{{name}}\n%%EOF\n")
			archive := makeTestArchive(t, compressed, []testArchiveEntry{
				{name: "template/README.md", body: "# {{name}} by __owner__ and {{unknown}}\n", mode: 0o644, typeflag: tar.TypeReg},
				{name: "template/bin/run.sh", body: "#!/bin/sh\necho {{name}}\n", mode: 0o755, typeflag: tar.TypeReg},
				{name: "template/image.bin", data: binary, mode: 0o644, typeflag: tar.TypeReg},
				{name: "template/document.pdf", data: pdf, mode: 0o644, typeflag: tar.TypeReg},
				{name: "template/empty", mode: 0o755, typeflag: tar.TypeDir},
			})
			destination := filepath.Join(t.TempDir(), "example")
			project, err := Create(context.Background(), destination, bytes.NewReader(archive), Options{
				Parameters: map[string]string{"owner": "bret"},
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if project.Name != "example" || project.Destination != destination {
				t.Fatalf("Create() project = %#v", project)
			}

			readme, err := os.ReadFile(filepath.Join(destination, "README.md"))
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			const wanted = "# example by bret and {{unknown}}\n"
			if string(readme) != wanted {
				t.Fatalf("README.md = %q, want %q", readme, wanted)
			}

			for filename, wanted := range map[string][]byte{
				"image.bin":    binary,
				"document.pdf": pdf,
			} {
				actual, err := os.ReadFile(filepath.Join(destination, filename))
				if err != nil {
					t.Fatalf("ReadFile(%s) error = %v", filename, err)
				}
				if !bytes.Equal(actual, wanted) {
					t.Fatalf("%s was modified: got %q, want %q", filename, actual, wanted)
				}
			}

			info, err := os.Stat(filepath.Join(destination, "bin", "run.sh"))
			if err != nil {
				t.Fatalf("Stat() error = %v", err)
			}
			if info.Mode().Perm() != 0o755 {
				t.Fatalf("run.sh mode = %o, want 755", info.Mode().Perm())
			}
			if info, err := os.Stat(filepath.Join(destination, "empty")); err != nil || !info.IsDir() {
				t.Fatalf("empty directory was not extracted: info=%v error=%v", info, err)
			}
		})
	}
}

func TestExtractArchiveRejectsTraversalAtomically(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	destination := filepath.Join(parent, "example")
	archive := makeTestArchive(t, true, []testArchiveEntry{
		{name: "template/../../escaped", body: "unsafe", mode: 0o644, typeflag: tar.TypeReg},
	})

	if err := extractArchive(context.Background(), bytes.NewReader(archive), destination, nil); err == nil {
		t.Fatal("extractArchive() unexpectedly succeeded")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failed extraction: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escaped")); !os.IsNotExist(err) {
		t.Fatalf("traversal path exists after failed extraction: %v", err)
	}
}

func TestExtractArchiveRejectsEscapingSymlink(t *testing.T) {
	t.Parallel()

	destination := filepath.Join(t.TempDir(), "example")
	archive := makeTestArchive(t, false, []testArchiveEntry{
		{name: "template/link", mode: 0o777, typeflag: tar.TypeSymlink, linkname: "../../outside"},
	})

	if err := extractArchive(context.Background(), bytes.NewReader(archive), destination, nil); err == nil {
		t.Fatal("extractArchive() unexpectedly succeeded")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failed extraction: %v", err)
	}
}

func TestExtractArchiveRejectsEscapingChainHardlink(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	outside := filepath.Join(parent, "secret")
	const content = "outside project"
	if err := os.WriteFile(outside, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	destination := filepath.Join(parent, "example")
	archive := makeTestArchive(t, false, []testArchiveEntry{
		{name: "template/a", mode: 0o777, typeflag: tar.TypeSymlink, linkname: "."},
		{name: "template/b", mode: 0o777, typeflag: tar.TypeSymlink, linkname: "a/.."},
		{name: "template/leaked", mode: 0o600, typeflag: tar.TypeLink, linkname: "template/b/secret"},
	})

	if err := extractArchive(context.Background(), bytes.NewReader(archive), destination, nil); err == nil || !strings.Contains(err.Error(), "non-directory") {
		t.Fatalf("extractArchive() error = %v, want rejection of symlink parent", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failed extraction: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "secret" {
		t.Fatalf("unexpected entries after failed extraction: %v", entries)
	}
	actual, err := os.ReadFile(outside)
	if err != nil || string(actual) != content {
		t.Fatalf("outside file = %q, error = %v", actual, err)
	}
}

func TestExtractArchivePreservesSafeLinks(t *testing.T) {
	t.Parallel()

	destination := filepath.Join(t.TempDir(), "example")
	archive := makeTestArchive(t, false, []testArchiveEntry{
		{name: "template/links/chain", typeflag: tar.TypeLink, linkname: "template/links/direct"},
		{name: "template/links/direct", typeflag: tar.TypeLink, linkname: "template/data/file"},
		{name: "template/links/symlink", typeflag: tar.TypeSymlink, linkname: "../data/file"},
		{name: "template/data/file", body: "hello {{name}}", mode: 0o644, typeflag: tar.TypeReg},
	})
	if err := extractArchive(context.Background(), bytes.NewReader(archive), destination, map[string]string{"name": "example"}); err != nil {
		t.Fatalf("extractArchive() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(destination, "data", "file"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	for _, name := range []string{"direct", "chain", "symlink"} {
		filename := filepath.Join(destination, "links", name)
		actual, err := os.ReadFile(filename)
		if err != nil || string(actual) != "hello example" {
			t.Fatalf("ReadFile(%s) = %q, error = %v", name, actual, err)
		}
		linkInfo, err := os.Stat(filename)
		if err != nil || !os.SameFile(info, linkInfo) {
			t.Fatalf("%s does not refer to the original file: %v", name, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(destination, "links", "symlink")); err != nil || target != "../data/file" {
		t.Fatalf("Readlink() = %q, error = %v", target, err)
	}
}

func TestExtractArchiveRestrictiveFileModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []int64{0o000, 0o200, 0o100, 0o400} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			t.Parallel()

			binary := []byte{0, '{', '{', 'n', 'a', 'm', 'e', '}', '}', 0xff}
			archive := makeTestArchive(t, false, []testArchiveEntry{
				{name: "template/text", body: "hello {{name}}", mode: mode, typeflag: tar.TypeReg},
				{name: "template/binary", data: binary, mode: mode, typeflag: tar.TypeReg},
			})
			destination := filepath.Join(t.TempDir(), "example")
			if err := extractArchive(context.Background(), bytes.NewReader(archive), destination, map[string]string{"name": "example"}); err != nil {
				t.Fatalf("extractArchive() error = %v", err)
			}
			for name, wanted := range map[string][]byte{"text": []byte("hello example"), "binary": binary} {
				filename := filepath.Join(destination, name)
				info, err := os.Stat(filename)
				if err != nil {
					t.Fatalf("Stat(%s) error = %v", name, err)
				}
				if info.Mode().Perm() != os.FileMode(mode) {
					t.Fatalf("%s mode = %o, want %o", name, info.Mode().Perm(), mode)
				}
				// Restore read access only after checking the archived mode.
				if err := os.Chmod(filename, 0o600); err != nil {
					t.Fatalf("Chmod(%s) error = %v", name, err)
				}
				actual, err := os.ReadFile(filename)
				if err != nil || !bytes.Equal(actual, wanted) {
					t.Fatalf("ReadFile(%s) = %q, error = %v; want %q", name, actual, err, wanted)
				}
			}
		})
	}
}

func TestExtractArchiveRejectsExistingDestination(t *testing.T) {
	t.Parallel()

	destination := t.TempDir()
	if err := extractArchive(context.Background(), bytes.NewReader(nil), destination, nil); err == nil {
		t.Fatal("extractArchive() unexpectedly succeeded")
	}
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
		content := entry.content()
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     int64(len(content)),
			Typeflag: entry.typeflag,
			Linkname: entry.linkname,
		}
		if entry.typeflag == tar.TypeDir || entry.typeflag == tar.TypeSymlink || entry.typeflag == tar.TypeLink {
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader() error = %v", err)
		}
		if header.Size > 0 {
			if _, err := tarWriter.Write(content); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
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
