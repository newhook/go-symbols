package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/newhook/go-symbols/importer"
)

var fset = token.NewFileSet()
var info = types.Info{
	Defs: map[*ast.Ident]types.Object{},
}

type symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Package   string `json:"package"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
}

type ignoreList struct {
	val []string
}

func (ss *ignoreList) contains(name string) bool {
	for _, d := range ss.val {
		if name == d {
			return true
		}
	}
	return false
}

func (ss *ignoreList) Set(s string) error {
	ss.val = strings.Split(s, ",")
	return nil
}

func (ss *ignoreList) String() string {
	return fmt.Sprintf("%v", ss.val)
}

var ignore ignoreList

func init() {
	flag.Var(&ignore, "ignore", "set of entries to ignore")
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: go-symbols [flags] directory [query]\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(2)
	}

	dir := flag.Arg(0)
	var query string
	if flag.NArg() > 1 {
		query = flag.Arg(1)
	}

	importer := importer.NewWalker(fset, &build.Default, dir, &info)

	srcDir := filepath.Join(dir, "src")
	fi, err := os.Stat(srcDir)
	if err == nil && fi.IsDir() {
		dir = srcDir
	}

	root := dir

	local := map[*types.Package]bool{}
	var walk func(dir string) error
	walk = func(dir string) error {
		files, err := ioutil.ReadDir(dir)
		if err != nil {
			return err
		}
		pkg, err := importer.ImportPackage(dir[len(root):], dir)

		if pkg != nil {
			local[pkg] = true
		}

		for _, f := range files {
			if f.IsDir() && !ignore.contains(f.Name()) {
				walk(filepath.Join(dir, f.Name()))
			}
		}
		return nil
	}
	err = walk(dir)
	if err != nil {
		log.Fatalf("error walking tree: %v\n", err)
	}

	query = strings.ToLower(query)
	var syms []symbol
	for s, typ := range info.Defs {
		if typ == nil {
			continue
		}

		pkg := typ.Pkg()
		if pkg == nil {
			continue
		}

		if _, ok := local[pkg]; !ok {
			continue
		}

		sym := symbol{
			Package: typ.Pkg().Name(),
			Path:    fset.File(s.NamePos).Name(),
			Name:    s.Name,
		}

		if s.Obj != nil {
			sym.Kind = s.Obj.Kind.String()
		} else {
			// Functions don't have an Obj as the type checker was run with
			// IgnoreFuncBodies=true.
			sym.Kind = "func"
		}

		if !strings.Contains(strings.ToLower(sym.Name), query) {
			continue
		}

		f := fset.File(s.NamePos)
		sym.Line = f.Line(s.NamePos) - 1
		syms = append(syms, sym)
	}

	b, _ := json.MarshalIndent(syms, "", " ")
	fmt.Println(string(b))
}
