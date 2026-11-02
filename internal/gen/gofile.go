package gen

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"os"
	"path/filepath"
	"sort"
)

// renderGoFiles writes tether_gen.go into every handler package whose
// handlers take enums, and marks for removal the files tether wrote
// anywhere else. The server cannot list constants at run time; this file
// lists them for it.
func (m *model) renderGoFiles(header string) []File {
	var pkgs []*types.Package
	for pkg := range m.registers {
		pkgs = append(pkgs, pkg)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Path() < pkgs[j].Path() })

	var files []File
	written := map[string]bool{}
	for _, pkg := range pkgs {
		lp := m.roots[pkg]
		path := filepath.Join(filepath.Dir(lp.GoFiles[0]), genFile)
		written[path] = true
		files = append(files, File{Path: path, Data: registration(header, pkg, m.registers[pkg])})
	}
	for _, lp := range m.pkgs {
		for _, f := range lp.GoFiles {
			if filepath.Base(f) == genFile && !written[f] && ours(f) {
				files = append(files, File{Path: f})
			}
		}
	}
	return files
}

// registration writes the file for one handler package. Import names are
// chosen so they collide neither with each other nor with a name declared
// in the package, which Go would reject.
func registration(header string, pkg *types.Package, enums []*enumDef) []byte {
	taken := func(name string, used map[string]bool) bool {
		return used[name] || pkg.Scope().Lookup(name) != nil
	}
	used := map[string]bool{}
	pick := func(name string) string {
		alias := name
		for i := 2; taken(alias, used); i++ {
			alias = fmt.Sprintf("%s%d", name, i)
		}
		used[alias] = true
		return alias
	}
	tether := pick("tether")

	var foreign []*types.Package
	seen := map[*types.Package]bool{}
	for _, e := range enums {
		if p := e.obj.Pkg(); p != pkg && !seen[p] {
			seen[p] = true
			foreign = append(foreign, p)
		}
	}
	sort.Slice(foreign, func(i, j int) bool { return foreign[i].Path() < foreign[j].Path() })
	names := map[*types.Package]string{}
	for _, p := range foreign {
		names[p] = pick(p.Name())
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\n\npackage %s\n\nimport (\n", header, pkg.Name())
	writeImport(&b, tether, "tether", tetherPath)
	for _, p := range foreign {
		writeImport(&b, names[p], p.Name(), p.Path())
	}
	b.WriteString(")\n\nfunc init() {\n")
	for _, e := range enums {
		fmt.Fprintf(&b, "\t%s.RegisterEnum(", tether)
		for i, c := range e.consts {
			if i > 0 {
				b.WriteString(", ")
			}
			if q := names[e.obj.Pkg()]; q != "" {
				b.WriteString(q + ".")
			}
			b.WriteString(c.Name())
		}
		b.WriteString(")\n")
	}
	b.WriteString("}\n")
	src, err := format.Source(b.Bytes())
	if err != nil {
		return b.Bytes()
	}
	return src
}

func writeImport(b *bytes.Buffer, alias, name, path string) {
	if alias == name {
		fmt.Fprintf(b, "\t%q\n", path)
	} else {
		fmt.Fprintf(b, "\t%s %q\n", alias, path)
	}
}

// ours reports whether tether wrote the file at path.
func ours(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && bytes.HasPrefix(data, []byte(genPrefix))
}
