// The gosymbols command prints type information for package-level symbols.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/tools/go/buildutil"
)

const usage = `Usage: gosymbols <package> ...`

func init() {
	flag.Var((*buildutil.TagsFlag)(&build.Default.BuildTags), "tags", buildutil.TagsFlagDoc)
}

func main() {
	if err := doMain(); err != nil {
		fmt.Fprintf(os.Stderr, "go-symbols: %s\n", err)
		os.Exit(1)
	}
}

type symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Package   string `json:"package"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
}

var mutex sync.Mutex
var syms = make([]symbol, 0)

type visitor struct {
	pkg   *ast.Package
	fset  *token.FileSet
	query string
	syms  []symbol
}

func (v *visitor) Visit(node ast.Node) bool {
	descend := true
	var ident *ast.Ident
	var kind string
	switch t := node.(type) {
	case *ast.FuncDecl:
		kind = "func"
		ident = t.Name
		descend = false

	case *ast.TypeSpec:
		kind = "type"
		ident = t.Name
		descend = false
	}

	if ident != nil && strings.Contains(strings.ToLower(ident.Name), v.query) {
		f := v.fset.File(ident.Pos())
		v.syms = append(v.syms, symbol{
			Package: v.pkg.Name,
			Path:    f.Name(),
			Name:    ident.Name,
			Kind:    kind,
			Line:    f.Line(ident.Pos()) - 1,
		})
	}

	return descend
}

func doMain() error {
	var dir, query string

	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	dir = args[0]

	if len(args) > 1 {
		query = args[1]
	}

	// Bail early with an unbounded search.
	// TODO: refactor me.
	if len(query) < 1 {
		b, _ := json.MarshalIndent(syms, "", " ")
		fmt.Println(string(b))
		os.Exit(0)
	}

	runtime.GOMAXPROCS(runtime.NumCPU())

	query = strings.ToLower(query)

	ctxt := build.Default // copy
	if len(dir) > 0 {
		// TODO: Build a buildutil.FakeContext out of a directory which exists outside a GOPATH
	}

	fset := token.NewFileSet()
	sema := make(chan int, 8) // concurrency-limiting semaphore
	var wg sync.WaitGroup

	buildutil.ForEachPackage(&ctxt, func(path string, err error) {
		if err != nil {
			log.Printf("Error in package %s: %s", path, err)
			return
		}
		if len(path) == 0 {
			return
		}
		wg.Add(1)
		go func() {
			sema <- 1 // acquire token
			defer func() {
				<-sema // release token
			}()

			v := &visitor{
				fset:  fset,
				query: query,
			}
			defer func() {
				mutex.Lock()
				syms = append(syms, v.syms...)
				mutex.Unlock()
			}()

			defer wg.Done()

			// Ignore errors; some packages contain no source.
			pkg, _ := ctxt.Import(path, "", 0)
			parsed, _ := parser.ParseDir(fset, pkg.Dir, nil, 0)

			for _, astpkg := range parsed {
				v.pkg = astpkg
				for _, f := range astpkg.Files {
					ast.Inspect(f, v.Visit)
				}
			}
		}()
	})
	wg.Wait()

	b, _ := json.MarshalIndent(syms, "", " ")
	fmt.Println(string(b))

	return nil
}
