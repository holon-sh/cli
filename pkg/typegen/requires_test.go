package typegen

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDeclaredSources(t *testing.T) {
	const descriptor = `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout"`
	const metadata = `,"_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":`
	for _, test := range []struct {
		name string
		data string
		want []Source
		err  string
	}{
		{name: "no descriptor"},
		{name: "no requires", data: descriptor + `}`},
		{name: "empty requires", data: descriptor + metadata + `{"requires":{}}}}}`},
		{
			name: "sorted aliases and registry references",
			data: descriptor + metadata + `{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0"},"docs":{"name":"example.com/documentation","version":"4.4.4","registry":"https://registry.example"}}}}}}`,
			want: []Source{
				{Alias: "docs", Registry: &Reference{Name: "example.com/documentation", Version: "4.4.4", Registry: "https://registry.example"}},
				{Alias: "orders", Registry: &Reference{Name: "example.com/orders", Version: "1.0.0"}},
			},
		},
		{name: "invalid descriptor", data: `{"name":"checkout","version":"1.0.0","description":"Checkout"}`, err: "namespace/name"},
		{name: "invalid alias", data: descriptor + metadata + `{"requires":{"Orders":{"name":"example.com/orders","version":"1.0.0"}}}}}}`, err: "invalid dependency name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.data != "" {
				put(t, root, "server.json", test.data)
			}
			got, err := DeclaredSources(root)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "server.json")) || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("DeclaredSources error = %v, want server.json and %q", err, test.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("DeclaredSources = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}
