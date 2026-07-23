package goproject

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

const contentSniffSize = 512

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

func formatFile(ctx context.Context, filename string, values map[string]string) error {
	source, err := os.Open(filename)
	if err != nil {
		return err
	}

	temporary, err := os.CreateTemp(filepath.Dir(filename), ".goproject-format-*")
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

func formatReader(source io.Reader, values map[string]string) io.Reader {
	braces := makeReplacements("{{", "}}", values)
	underscores := makeReplacements("__", "__", values)
	return newReplacingReader(newReplacingReader(source, braces), underscores)
}

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

type replacement struct {
	old []byte
	new []byte
}

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

func newReplacingReader(source io.Reader, replacements []replacement) io.Reader {
	if len(replacements) == 0 {
		return source
	}

	maximumSize := 0
	for _, replacement := range replacements {
		if len(replacement.old) > maximumSize {
			maximumSize = len(replacement.old)
		}
	}
	return &replacingReader{
		source:       source,
		replacements: replacements,
		maximumSize:  maximumSize,
		buffer:       make([]byte, 32*1024),
	}
}

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
			if len(r.pending)-position >= len(replacement.old) && bytes.Equal(r.pending[position:position+len(replacement.old)], replacement.old) {
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
