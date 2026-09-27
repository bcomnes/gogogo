package gogogo

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// contentSniffSize matches the maximum prefix examined by http.DetectContentType.
// Text candidates are still checked beyond this prefix before substitution.
const contentSniffSize = 512

// isBinaryFile conservatively decides whether substitution could corrupt a file.
// A binary MIME classification is sufficient to skip formatting; text candidates
// are then scanned in full for NUL bytes or invalid UTF-8. This uses bounded read
// buffers but may read the whole file and does not support context cancellation.
// It is a content heuristic, not validation of any particular text format.
func isBinaryFile(filename string) (bool, error) {
	file, err := os.Open(filename)
	if err != nil {
		return false, err
	}
	defer file.Close()

	sample := make([]byte, contentSniffSize)
	count, err := io.ReadFull(file, sample)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false, err
	}
	if binaryMediaType(sample[:count]) {
		return true, nil
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	reader := bufio.NewReader(file)
	for {
		runeValue, size, err := reader.ReadRune()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if runeValue == 0 || runeValue == utf8.RuneError && size == 1 {
			return true, nil
		}
	}
}

// binaryMediaType treats sniffed text and a small allowlist of text-based media
// types as substitution candidates. Unknown types remain binary; a false result
// must still be followed by the UTF-8 and NUL checks in isBinaryFile.
func binaryMediaType(sample []byte) bool {
	contentType := http.DetectContentType(sample)
	mediaType, _, _ := strings.Cut(strings.ToLower(contentType), ";")
	if strings.HasPrefix(mediaType, "text/") {
		return false
	}
	switch mediaType {
	case "application/json", "application/ld+json", "application/javascript", "application/xml", "application/xhtml+xml", "image/svg+xml":
		return false
	default:
		return true
	}
}

// formatFile streams substitutions through a sibling temporary file, leaving the
// original intact until copying and both closes succeed. Publication removes the
// original before renaming, so it is not an atomic replacement: a rename failure
// can leave filename absent. This helper is intended for a disposable staging tree.
//
// Temporary-file cleanup is best-effort. Original permissions and timestamps are
// not preserved here; the extraction caller restores archive metadata afterward.
// Cancellation is checked before source reads, not during publication.
func formatFile(ctx context.Context, filename string, values map[string]string) error {
	source, err := os.Open(filename)
	if err != nil {
		return err
	}

	temporary, err := os.CreateTemp(filepath.Dir(filename), ".gogogo-format-*")
	if err != nil {
		source.Close()
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	formatted := formatReader(&contextReader{ctx: ctx, reader: source}, values)
	_, copyErr := io.Copy(temporary, formatted)
	sourceCloseErr := source.Close()
	temporaryCloseErr := temporary.Close()
	if copyErr != nil {
		return copyErr
	}
	if sourceCloseErr != nil {
		return sourceCloseErr
	}
	if temporaryCloseErr != nil {
		return temporaryCloseErr
	}
	if err := os.Remove(filename); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filename); err != nil {
		return fmt.Errorf("publish formatted file: %w", err)
	}
	return nil
}

// formatReader composes two streaming, literal substitution passes: {{key}} first,
// then __key__. Text inserted by the first pass can therefore be substituted by
// the second, but replacements are not recursively expanded within a single pass.
// Unknown placeholders remain unchanged. Neither keys nor values are interpreted
// as expressions or escaped for a target language. The caller owns source.
func formatReader(source io.Reader, values map[string]string) io.Reader {
	braces := makeReplacements("{{", "}}", values)
	underscores := makeReplacements("__", "__", values)
	return newReplacingReader(newReplacingReader(source, braces), underscores)
}

// makeReplacements snapshots nonempty keys and their values into literal byte
// pairs. Sorting keys makes matching precedence deterministic rather than dependent
// on map iteration; empty values are valid and remove the matching placeholder.
func makeReplacements(open, close string, values map[string]string) []replacement {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	replacements := make([]replacement, 0, len(keys))
	for _, key := range keys {
		replacements = append(replacements, replacement{
			old: []byte(open + key + close),
			new: []byte(values[key]),
		})
	}
	return replacements
}

// replacement describes one literal byte match and its emitted bytes. old must
// be nonempty so a match always advances the input; new may be empty.
type replacement struct {
	old []byte
	new []byte
}

// replacingReader substitutes bytes incrementally while retaining enough trailing
// input to recognize matches split across source reads. Replacement output is not
// rescanned within this reader. Buffering depends on chunk size, longest pattern,
// and replacement expansion, rather than the total input size.
//
// Source errors are deferred until all output from preceding bytes is delivered.
// The mutable reader is not safe for concurrent Read calls and does not close its
// source or provide cancellation independently of that source.
type replacingReader struct {
	source       io.Reader
	replacements []replacement
	maximumSize  int
	pending      []byte
	output       []byte
	buffer       []byte
	finished     bool
	finalError   error
}

// newReplacingReader returns source unchanged when there are no replacements.
// Otherwise it uses the supplied order as first-match precedence. Patterns must be
// nonempty, and callers must not mutate the retained replacement slice or its bytes
// while the returned reader is in use.
func newReplacingReader(source io.Reader, replacements []replacement) io.Reader {
	if len(replacements) == 0 {
		return source
	}

	maximumSize := 0
	for _, replacement := range replacements {
		maximumSize = max(maximumSize, len(replacement.old))
	}
	return &replacingReader{
		source:       source,
		replacements: replacements,
		maximumSize:  maximumSize,
		buffer:       make([]byte, 32*1024),
	}
}

// Read delivers buffered transformed bytes before reporting a saved source error.
// A non-EOF source error is reported once, followed by EOF on subsequent reads.
// A zero-length destination succeeds without consuming input or a pending error.
func (r *replacingReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}

	for len(r.output) == 0 && !r.finished {
		r.fill()
	}
	if len(r.output) > 0 {
		count := copy(destination, r.output)
		r.output = r.output[count:]
		return count, nil
	}
	if r.finalError != nil {
		err := r.finalError
		r.finalError = nil
		return 0, err
	}
	return 0, io.EOF
}

// fill reads another chunk and emits matches only where enough lookahead exists
// to rule out a longer, boundary-spanning pattern. Any source error marks the final
// chunk, allowing the retained suffix to be flushed before that error is surfaced.
// It may produce no output when more input is needed or replacements erase it.
func (r *replacingReader) fill() {
	count, err := r.source.Read(r.buffer)
	if count > 0 {
		r.pending = append(r.pending, r.buffer[:count]...)
	}

	final := err != nil
	limit := len(r.pending)
	if !final {
		limit = len(r.pending) - r.maximumSize + 1
		if limit <= 0 {
			return
		}
	}

	position := 0
	for position < limit {
		matched := false
		for _, replacement := range r.replacements {
			if bytes.HasPrefix(r.pending[position:], replacement.old) {
				r.output = append(r.output, replacement.new...)
				position += len(replacement.old)
				matched = true
				break
			}
		}
		if !matched {
			r.output = append(r.output, r.pending[position])
			position++
		}
	}
	r.pending = r.pending[position:]

	if final {
		r.finished = true
		if err != io.EOF {
			r.finalError = err
		}
	}
}
