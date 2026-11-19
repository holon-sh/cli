package recipe

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/holon-sh/protocol/runtimecontract"
)

func TestRead(t *testing.T) {
	page := `{"scripts":{"build":"vite build"}}`
	modules := runtimecontract.Freeze{Dependencies: []string{"node_modules"}}
	npm := Deploy{Install: [][]string{{"npm", "install"}}, Freeze: modules}
	pnpmState := []string{"rm", "-f", "node_modules/.modules.yaml", "node_modules/.pnpm-workspace-state-v1.json"}
	for _, test := range []struct {
		name  string
		files map[string]string
		want  Recipe
		err   string
	}{
		{
			name: "pnpm declared, with no build",
			files: map[string]string{
				"go.mod": "", "main.go": "",
				".nvmrc": "22", ".node-version": "24", "web/.nvmrc": "20",
				"package.json": `{"packageManager":"pnpm@11.25.0","dependencies":{"holon-sh":"^0.0.1"}}`,
			},
			want: Recipe{
				Inputs: []string{".node-version", ".nvmrc", "package.json"}, Install: [][]string{{"pnpm", "install"}},
				Deploy: Deploy{Install: [][]string{{"pnpm", "install"}}, Build: [][]string{pnpmState}, Freeze: modules},
			},
		},
		{
			name:  "pnpm by its lockfile",
			files: map[string]string{"package.json": `{"engines":{"node":">=99"},"scripts":{"build":"vite build"}}`, "pnpm-lock.yaml": ""},
			want: Recipe{
				Inputs: []string{"package.json", "pnpm-lock.yaml"}, Install: [][]string{{"pnpm", "install"}}, Build: [][]string{{"pnpm", "run", "build"}},
				Deploy: Deploy{
					Install: [][]string{{"pnpm", "install", "--frozen-lockfile"}}, Build: [][]string{{"pnpm", "run", "build"}, pnpmState}, Freeze: modules,
				},
			},
		},
		{
			name:  "a declaration over the lockfile",
			files: map[string]string{"package.json": `{"devEngines":{"packageManager":[{"name":"npm"}]}}`, "pnpm-lock.yaml": ""},
			want:  Recipe{Inputs: []string{"package.json", "pnpm-lock.yaml"}, Install: [][]string{{"npm", "install"}}, Deploy: npm},
		},
		{
			name:  "npm otherwise, beside a Python version file",
			files: map[string]string{"package.json": "{}", ".python-version": "3.13\n"},
			want:  Recipe{Inputs: []string{"package.json"}, Install: [][]string{{"npm", "install"}}, Deploy: npm},
		},
		{
			name: "uv with a page, reinstalled when a manifest changes",
			files: map[string]string{
				"pyproject.toml": "", "server.py": "", "uv.lock": "", "package.json": page, ".python-version": "3.13\n", ".npmrc": "",
				"patches/left-pad.patch": "", "web/package.json": "{}", "web/main.ts": "",
			},
			want: Recipe{
				Inputs:  []string{".python-version", "pyproject.toml", "uv.lock", ".npmrc", "package.json", "patches/left-pad.patch", "web/package.json"},
				Install: [][]string{{"uv", "sync"}, {"npm", "install"}}, Build: [][]string{{"npm", "run", "build"}},
				Deploy: Deploy{
					Install: [][]string{{"uv", "sync", "--locked", "--no-dev"}, {"npm", "install"}},
					Build:   [][]string{{"npm", "run", "build"}, {"rm", "-rf", "node_modules"}},
					Freeze:  runtimecontract.Freeze{Dependencies: []string{".venv"}},
				},
			},
		},
		{name: "yarn", files: map[string]string{"package.json": `{"packageManager":"yarn@4.5.0"}`}, err: "declares the package manager yarn"},
		{name: "two managers", files: map[string]string{"package.json": `{"packageManager":"pnpm@11.25.0","engines":{"npm":">=10"}}`}, err: "npm and pnpm"},
		{name: "pip", files: map[string]string{"requirements.txt": "holon-sh\n", "server.py": ""}, err: "found requirements.txt but no uv.lock: Python projects install with uv from a pyproject.toml and its uv.lock"},
		{name: "no project", files: map[string]string{"server.ts": ""}, err: "found neither package.json nor pyproject.toml"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := fstest.MapFS{}
			for name, data := range test.files {
				source[name] = &fstest.MapFile{Data: []byte(data)}
			}
			got, err := Read(source)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("error = %v, want %q", err, test.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("recipe = %+v, want %+v: %v", got, test.want, err)
			}
		})
	}
}
