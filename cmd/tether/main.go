// Command tether reads a Go program that serves its API with tether.Route
// and writes an OpenAPI document, a typed TypeScript client, and a small Go
// file per package that registers the enums the API uses.
//
// Usage, usually from a go:generate directive:
//
//	go tool tether -out web/src/api [-openapi openapi.json] [-title T] [-version V] [-qualify] [-check] [packages]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/maulai/tether/internal/gen"
)

func main() {
	var o gen.Options
	flag.StringVar(&o.Out, "out", "", "directory for the TypeScript files (required)")
	flag.StringVar(&o.OpenAPI, "openapi", "openapi.json", "path of the OpenAPI document")
	flag.StringVar(&o.Title, "title", "", "title of the API in the OpenAPI document")
	flag.StringVar(&o.Version, "version", "", "version of the API in the OpenAPI document")
	flag.BoolVar(&o.Qualify, "qualify", false, "start type names with their package name, as in DocumentStatus for document.Status")
	check := flag.Bool("check", false, "write nothing; fail if a generated file is missing or differs")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tether -out <dir> [-openapi file] [-title text] [-version text] [-qualify] [-check] [packages]")
		flag.PrintDefaults()
	}
	flag.Parse()
	o.Patterns = flag.Args()
	o.Dir, _ = os.Getwd()
	if info, ok := debug.ReadBuildInfo(); ok {
		o.Tool = info.Main.Version
	}

	files, err := gen.Generate(o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tether:", err)
		os.Exit(1)
	}
	if *check {
		if stale := gen.Stale(files); len(stale) > 0 {
			fmt.Fprintln(os.Stderr, "tether: generated files are out of date; run go generate:")
			for _, s := range stale {
				if rel, err := filepath.Rel(o.Dir, s); err == nil {
					s = rel
				}
				fmt.Fprintln(os.Stderr, "  "+s)
			}
			os.Exit(1)
		}
		return
	}
	if err := gen.Write(files); err != nil {
		fmt.Fprintln(os.Stderr, "tether:", err)
		os.Exit(1)
	}
}
