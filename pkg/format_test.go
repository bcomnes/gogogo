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

func TestFormatReaderEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		wanted string
	}{
		{name: "empty input"},
		{name: "partial placeholder", source: "{{name", wanted: "{{name"},
		{name: "partial suffix", source: "{{name}} {{na", wanted: "Ada {{na"},
		{name: "adjacent placeholders", source: "{{name}}{{name}}__name__", wanted: "AdaAdaAda"},
		{name: "different key lengths", source: "{{n}} {{name}}", wanted: "N Ada"},
		{name: "empty replacement", source: "before{{empty}}after", wanted: "beforeafter"},
		{name: "empty key ignored", source: "{{}} ____", wanted: "{{}} ____"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			values := map[string]string{"n": "N", "name": "Ada", "empty": "", "": "ignored"}
			for _, source := range []io.Reader{
				strings.NewReader(test.source),
				&chunkReader{value: test.source},
			} {
				formatted, err := io.ReadAll(formatReader(source, values))
				if err != nil {
					t.Fatalf("ReadAll(%T) error = %v", source, err)
				}
				if string(formatted) != test.wanted {
					t.Fatalf("formatted content (%T) = %q, want %q", source, formatted, test.wanted)
				}
			}
		})
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
