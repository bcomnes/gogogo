package gogogo_test

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bcomnes/gogogo/pkg"
)

func ExampleCreate() {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	content := []byte("# __name__\n")
	_ = writer.WriteHeader(&tar.Header{
		Name:     "template/README.md",
		Mode:     0o644,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	})
	_, _ = writer.Write(content)
	_ = writer.Close()

	parent, _ := os.MkdirTemp("", "gogogo-example-")
	defer os.RemoveAll(parent)
	destination := filepath.Join(parent, "example")
	project, _ := gogogo.Create(context.Background(), destination, bytes.NewReader(archive.Bytes()), gogogo.Options{})
	readme, _ := os.Open(filepath.Join(project.Destination, "README.md"))
	defer readme.Close()

	fmt.Println(project.Name)
	_, _ = io.Copy(os.Stdout, readme)
	// Output:
	// example
	// # example
}
