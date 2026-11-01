package gen

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"path/filepath"
	"sort"
)

// renderGoFiles writes tether_gen.go for every package that declares an
// enum the API uses, and marks existing ones that are no longer needed for
// removal. The server cannot list constants at run time; this file lists
// them for it.
func (m *model) renderGoFiles(header string) []File {
	byPkg := map[*types.Package][]*enumDef{}
	for _, e := range m.sortedEnums() {
		byPkg[e.obj.Pkg()] = append(byPkg[e.obj.Pkg()], e)
	}
	var files []File
	for _, pkg := range m.pkgs {
		if len(pkg.GoFiles) == 0 {
			continue
		}
		path := filepath.Join(filepath.Dir(pkg.GoFiles[0]), genFile)
		enums := byPkg[pkg.Types]
		if len(enums) == 0 {
			for _, f := range pkg.GoFiles {
				if filepath.Base(f) == genFile {
					files = append(files, File{Path: path})
				}
			}
			continue
		}
		sort.Slice(enums, func(i, j int) bool { return enums[i].name < enums[j].name })
		var b bytes.Buffer
		fmt.Fprintf(&b, "%s\n\npackage %s\n\nimport \"%s\"\n\nfunc init() {\n", header, pkg.Name, tetherPath)
		for _, e := range enums {
			fmt.Fprintf(&b, "\ttether.RegisterEnum(")
			for i, c := range e.consts {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(c.Name())
			}
			b.WriteString(")\n")
		}
		b.WriteString("}\n")
		src, err := format.Source(b.Bytes())
		if err != nil {
			src = b.Bytes()
		}
		files = append(files, File{Path: path, Data: src})
	}
	return files
}
