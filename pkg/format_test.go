package gogogo

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatReader(t *testing.T) {
	t.Parallel()

	source := &chunkReader{value: "{{first}} __second__ {{unknown}} {{nested}}"}
	formatted, err := io.ReadAll(formatReader(source, map[string]string{
		"first":  "Ada",
		"second": "Lovelace",
		"nested": "__second__",
	}))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}

	const wanted = "Ada Lovelace {{unknown}} Lovelace"
	if string(formatted) != wanted {
		t.Fatalf("formatted content = %q, want %q", formatted, wanted)
	}
}

func TestIsBinaryFile(t *testing.T) {
	t.Parallel()

	values := map[string][]byte{
		"text":             []byte("hello {{name}}\n"),
		"NUL bytes":        {0x00, '{', '{', 'n', 'a', 'm', 'e', '}', '}', 0xff},
		"PDF":              []byte("%PDF-1.7\n{{name}}\n%%EOF\n"),
		"late binary byte": append([]byte(strings.Repeat("a", contentSniffSize+1)), 0),
	}

	for name, value := range values {
		name := name
		value := value
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "content")
			if err := os.WriteFile(filename, value, 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			binary, err := isBinaryFile(filename)
			if err != nil {
				t.Fatalf("isBinaryFile() error = %v", err)
			}
			if wanted := name != "text"; binary != wanted {
				t.Fatalf("isBinaryFile() = %t, want %t", binary, wanted)
			}
		})
	}
}

func TestFormatReaderWithoutValues(t *testing.T) {
	t.Parallel()

	const value = "{{unknown}} __unknown__"
	formatted, err := io.ReadAll(formatReader(strings.NewReader(value), nil))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(formatted) != value {
		t.Fatalf("formatted content = %q, want %q", formatted, value)
	}
}

type chunkReader struct {
	value  string
	offset int
}

func (r *chunkReader) Read(buffer []byte) (int, error) {
	if r.offset >= len(r.value) {
		return 0, io.EOF
	}
	buffer[0] = r.value[r.offset]
	r.offset++
	return 1, nil
}
