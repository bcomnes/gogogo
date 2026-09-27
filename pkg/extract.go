package gogogo

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// directoryMetadata defers directory permissions and timestamps until descendants
// have been created, so restrictive modes and child writes do not interfere.
type directoryMetadata struct {
	path    string
	mode    os.FileMode
	modTime time.Time
}

// hardlink records root-relative, stripped archive paths for deferred resolution.
// Deferral allows a link to precede its target in the tar stream.
type hardlink struct {
	path   string
	target string
}

// extractArchive stages extraction on the destination's filesystem and publishes
// the completed tree with a rename. Failed staging is removed on a best-effort
// basis, but newly created parent directories are retained. The caller must keep
// the destination namespace stable: the initial availability check does not make
// the final rename an atomic create-if-absent operation.
func extractArchive(ctx context.Context, source io.Reader, destination string, values map[string]string) error {
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	if err := destinationAvailable(absoluteDestination); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absoluteDestination), 0o755); err != nil {
		return fmt.Errorf("create destination parent: %w", err)
	}

	staging, err := os.MkdirTemp(filepath.Dir(absoluteDestination), ".gogogo-*")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	removeStaging := true
	defer func() {
		if removeStaging {
			_ = os.RemoveAll(staging)
		}
	}()

	if err := extractInto(ctx, source, staging, values); err != nil {
		return err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return fmt.Errorf("set destination permissions: %w", err)
	}
	if err := os.Rename(staging, absoluteDestination); err != nil {
		return fmt.Errorf("publish project: %w", err)
	}
	removeStaging = false
	return nil
}

// destinationAvailable requires a missing directory entry, not merely a path
// whose target is missing. Lstat therefore rejects dangling symlinks as well as
// ordinary files and directories. This check does not reserve the path.
func destinationAvailable(destination string) error {
	_, err := os.Lstat(destination)
	if err == nil {
		return fmt.Errorf("destination %s already exists", destination)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	return nil
}

// extractInto consumes a tar or magic-byte-detected gzip stream into a private,
// caller-owned staging root. It strips one leading component per entry, rejects
// duplicate resulting paths, and supports only directories, regular files,
// symlinks, and hardlinks. It does not verify that all stripped components match.
//
// Regular files are streamed to disk before classification and substitution.
// Hardlinks are resolved after all entries, and directory metadata is applied last.
// Gzip input is drained after tar EOF to surface trailer/checksum errors. Neither
// that drain nor metadata finalization checks ctx; entry iteration and file-copy
// reads provide cooperative, rather than immediate, cancellation.
//
// The caller is responsible for cleanup on error and for preventing concurrent
// modification of root. No limits are placed on expanded size or entry count.
func extractInto(ctx context.Context, source io.Reader, root string, values map[string]string) error {
	buffered := bufio.NewReader(source)
	archiveReader := io.Reader(buffered)
	var gzipReader *gzip.Reader

	header, err := buffered.Peek(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("inspect archive: %w", err)
	}
	if len(header) == 2 && header[0] == 0x1f && header[1] == 0x8b {
		gzipReader, err = gzip.NewReader(buffered)
		if err != nil {
			return fmt.Errorf("open gzip archive: %w", err)
		}
		defer gzipReader.Close()
		archiveReader = gzipReader
	}

	tarReader := tar.NewReader(archiveReader)
	seen := make(map[string]struct{})
	directories := make([]directoryMetadata, 0)
	hardlinks := make([]hardlink, 0)
	entryCount := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}

		relativePath, include, err := strippedArchivePath(header.Name)
		if err != nil {
			return err
		}
		if !include {
			continue
		}
		if _, exists := seen[relativePath]; exists {
			return fmt.Errorf("archive contains duplicate path %q", relativePath)
		}
		seen[relativePath] = struct{}{}
		entryCount++

		target, err := secureJoin(root, relativePath)
		if err != nil {
			return err
		}
		if err := ensureParentDirectories(root, target); err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(target, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("create directory %q: %w", relativePath, err)
			}
			info, err := os.Lstat(target)
			if err != nil {
				return fmt.Errorf("inspect directory %q: %w", relativePath, err)
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("archive directory %q conflicts with another entry", relativePath)
			}
			directories = append(directories, directoryMetadata{target, header.FileInfo().Mode().Perm(), header.ModTime})

		case tar.TypeReg, tar.TypeRegA:
			if err := writeArchiveFile(ctx, tarReader, target, relativePath, header, values); err != nil {
				return err
			}

		case tar.TypeSymlink:
			if err := createArchiveSymlink(root, target, relativePath, header.Linkname); err != nil {
				return err
			}

		case tar.TypeLink:
			linkTarget, include, err := strippedArchivePath(header.Linkname)
			if err != nil {
				return fmt.Errorf("invalid hardlink target for %q: %w", relativePath, err)
			}
			if !include {
				return fmt.Errorf("hardlink %q targets the stripped archive root", relativePath)
			}
			hardlinks = append(hardlinks, hardlink{path: relativePath, target: linkTarget})

		default:
			return fmt.Errorf("archive entry %q has unsupported type %d", relativePath, header.Typeflag)
		}
	}

	if gzipReader != nil {
		if _, err := io.Copy(io.Discard, gzipReader); err != nil {
			return fmt.Errorf("finish reading gzip archive: %w", err)
		}
	}
	if entryCount == 0 {
		return fmt.Errorf("archive contains no project files after stripping its root directory")
	}
	if err := createHardlinks(root, hardlinks); err != nil {
		return err
	}
	return applyDirectoryMetadata(directories)
}

// strippedArchivePath normalizes a slash-separated tar name and removes its first
// component. The boolean is false for entries representing only the stripped root.
// Absolute paths, escaping traversal, empty names, and backslashes are rejected;
// internal dot components are cleaned before stripping. This is a per-entry check,
// not verification that the archive has a single shared root directory.
func strippedArchivePath(name string) (string, bool, error) {
	if name == "" || strings.ContainsRune(name, '\\') {
		return "", false, fmt.Errorf("archive path %q is invalid", name)
	}
	cleaned := path.Clean(strings.TrimPrefix(name, "./"))
	if cleaned == "." {
		return "", false, nil
	}
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false, fmt.Errorf("archive path %q escapes its root", name)
	}

	parts := strings.SplitN(cleaned, "/", 2)
	if len(parts) == 1 || parts[1] == "" || parts[1] == "." {
		return "", false, nil
	}
	relativePath := path.Clean(parts[1])
	if relativePath == ".." || strings.HasPrefix(relativePath, "../") || path.IsAbs(relativePath) {
		return "", false, fmt.Errorf("archive path %q escapes its root", name)
	}
	return relativePath, true, nil
}

// secureJoin converts an archive-relative path to a native path and checks lexical
// containment beneath root. It does not resolve symlinks or inspect the filesystem;
// callers must separately ensure that parent components are safe to traverse.
func secureJoin(root, relativePath string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(relativePath))
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("resolve archive path %q: %w", relativePath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("archive path %q escapes its destination", relativePath)
	}
	return target, nil
}

// ensureParentDirectories creates missing parents and rejects existing symlinks
// or non-directories along the route to target. target must already have passed
// secureJoin, and root must be a trusted directory. Lstat checks assume exclusive
// control of the staging tree; they do not prevent concurrent replacement races.
func ensureParentDirectories(root, target string) error {
	parent := filepath.Dir(target)
	relative, err := filepath.Rel(root, parent)
	if err != nil {
		return fmt.Errorf("resolve parent directory: %w", err)
	}
	if relative == "." {
		return nil
	}

	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return fmt.Errorf("create parent directory: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect parent directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive path traverses non-directory %s", current)
		}
	}
	return nil
}

// writeArchiveFile exclusively creates a regular file, streams source into it,
// and substitutes placeholders only after the on-disk file is classified as text.
// source must be limited to this entry's payload, as a tar.Reader is. Parent paths
// must already be checked; exclusive creation prevents replacing an existing entry.
//
// Permission bits and nonzero modification times are restored after formatting.
// A failure may leave a partial file for the staging owner to remove. Cancellation
// is checked before payload and formatting reads, not during binary classification
// or an already-blocked underlying read.
func writeArchiveFile(ctx context.Context, source io.Reader, target, relativePath string, header *tar.Header, values map[string]string) error {
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, header.FileInfo().Mode().Perm())
	if err != nil {
		return fmt.Errorf("create file %q: %w", relativePath, err)
	}

	_, copyErr := io.Copy(file, &contextReader{ctx: ctx, reader: source})
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write file %q: %w", relativePath, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close file %q: %w", relativePath, closeErr)
	}

	binary, err := isBinaryFile(target)
	if err != nil {
		return fmt.Errorf("inspect file %q: %w", relativePath, err)
	}
	if !binary {
		if err := formatFile(ctx, target, values); err != nil {
			return fmt.Errorf("format file %q: %w", relativePath, err)
		}
	}
	if err := os.Chmod(target, header.FileInfo().Mode().Perm()); err != nil {
		return fmt.Errorf("set file mode %q: %w", relativePath, err)
	}
	if !header.ModTime.IsZero() {
		if err := os.Chtimes(target, header.ModTime, header.ModTime); err != nil {
			return fmt.Errorf("set file time %q: %w", relativePath, err)
		}
	}
	return nil
}

// createArchiveSymlink preserves a relative link target only when its lexical
// resolution from the link's directory stays within root. Absolute, empty, and
// backslash-containing targets are rejected. The target need not exist, and its
// symlink chain is not resolved here. The link's parent must already be validated.
func createArchiveSymlink(root, target, relativePath, linkTarget string) error {
	if linkTarget == "" || strings.ContainsRune(linkTarget, '\\') || filepath.IsAbs(filepath.FromSlash(linkTarget)) {
		return fmt.Errorf("symlink %q has unsafe target %q", relativePath, linkTarget)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(linkTarget)))
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("symlink %q escapes the destination", relativePath)
	}
	if err := os.Symlink(linkTarget, target); err != nil {
		return fmt.Errorf("create symlink %q: %w", relativePath, err)
	}
	return nil
}

// createHardlinks resolves deferred links in passes, allowing forward references
// and chains that eventually reach a regular file. A pass without progress reports
// an unresolved target, including missing targets and cycles. Direct symlink and
// directory targets are rejected by Lstat. The routine relies on the extraction
// tree's previously checked parents remaining unchanged and does not check context.
func createHardlinks(root string, links []hardlink) error {
	pending := append([]hardlink(nil), links...)
	for len(pending) > 0 {
		next := pending[:0]
		progress := false
		for _, link := range pending {
			target, err := secureJoin(root, link.target)
			if err != nil {
				return err
			}
			info, err := os.Lstat(target)
			if errors.Is(err, os.ErrNotExist) {
				next = append(next, link)
				continue
			}
			if err != nil {
				return fmt.Errorf("inspect hardlink target %q: %w", link.target, err)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("hardlink target %q is not a regular file", link.target)
			}

			linkPath, err := secureJoin(root, link.path)
			if err != nil {
				return err
			}
			if err := os.Link(target, linkPath); err != nil {
				return fmt.Errorf("create hardlink %q: %w", link.path, err)
			}
			progress = true
		}
		if !progress {
			return fmt.Errorf("archive contains unresolved hardlink target %q", pending[0].target)
		}
		pending = next
	}
	return nil
}

// applyDirectoryMetadata restores permissions and nonzero modification times from
// deepest directory to shallowest, avoiding restrictive parent modes until child
// metadata is complete. It reorders the supplied slice in place and returns on the
// first error without undoing metadata already applied.
func applyDirectoryMetadata(directories []directoryMetadata) error {
	sort.Slice(directories, func(i, j int) bool {
		return strings.Count(directories[i].path, string(filepath.Separator)) > strings.Count(directories[j].path, string(filepath.Separator))
	})
	for _, directory := range directories {
		if err := os.Chmod(directory.path, directory.mode); err != nil {
			return fmt.Errorf("set directory mode %s: %w", directory.path, err)
		}
		if !directory.modTime.IsZero() {
			if err := os.Chtimes(directory.path, directory.modTime, directory.modTime); err != nil {
				return fmt.Errorf("set directory time %s: %w", directory.path, err)
			}
		}
	}
	return nil
}

// contextReader adds a cancellation check before each read without taking
// ownership of the underlying reader. It cannot interrupt a Read in progress;
// readers that can block indefinitely need their own cancellation mechanism.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read returns the context error without touching the source when cancellation
// has already occurred; otherwise it preserves the underlying reader's result.
func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
