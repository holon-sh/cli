package installation

import (
	"os"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/pkg/mcpregistry"
)

func readServerDescriptor(path string) (*mcpregistry.Descriptor, error) {
	if err := regularNoSymlink(path, "server descriptor"); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := clientutil.ReadLimited(file, mcpregistry.MaxDescriptorBytes)
	if err != nil {
		return nil, err
	}
	return mcpregistry.ParseDescriptor(data)
}
