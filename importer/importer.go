package importer

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

// This code is adopted from https://godoc.org/golang.org/x/tools/go/loader
type Walker struct {
	base     string
	context  *build.Context
	dirs     []string
	scope    []string
	current  *types.Package
	features map[string]bool           // set
	imported map[string]*types.Package // packages already imported
	info     *types.Info
}

var fset *token.FileSet

func NewWalker(fs *token.FileSet, context *build.Context, base string, info *types.Info) *Walker {
	fset = fs
	dirs := []string{}
	dirs = append(dirs, filepath.Join(os.Getenv("GOROOT"), "src"))

	for _, dir := range strings.Split(os.Getenv("GOPATH"), ":") {
		dirs = append(dirs, filepath.Join(dir, "src"))
	}

	return &Walker{
		info:     info,
		base:     base,
		context:  context,
		dirs:     dirs,
		features: map[string]bool{},
		imported: map[string]*types.Package{"unsafe": types.Unsafe},
	}
}

const usePkgCache = true

// Importing is a sentinel taking the place in Walker.imported
// for a package that is in the process of being imported.
var importing types.Package

var (
	pkgCache = map[string]*types.Package{} // map tagKey to package
	pkgTags  = map[string][]string{}       // map import dir to list of relevant tags
)

// tagKey returns the tag-based key to use in the pkgCache.
// It is a comma-separated string; the first part is dir, the rest tags.
// The satisfied tags are derived from context but only those that
// matter (the ones listed in the tags argument) are used.
// The tags list, which came from go/build's Package.AllTags,
// is known to be sorted.
func tagKey(dir string, context *build.Context, tags []string) string {
	ctags := map[string]bool{
		context.GOOS:   true,
		context.GOARCH: true,
	}
	if context.CgoEnabled {
		ctags["cgo"] = true
	}
	for _, tag := range context.BuildTags {
		ctags[tag] = true
	}
	// TODO: ReleaseTags (need to load default)
	key := dir
	for _, tag := range tags {
		if ctags[tag] {
			key += "," + tag
		}
	}
	return key
}

func (w *Walker) Import(name string) (*types.Package, error) {
	pkg := w.imported[name]
	if pkg != nil {
		if pkg == &importing {
			return nil, fmt.Errorf("cycle importing package %q", name)
		}
		return pkg, nil
	}
	w.imported[name] = &importing

	// Determine package files.
	var dir string
	for _, d := range w.dirs {
		dir = filepath.Join(d, filepath.FromSlash(name))
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			break
		}
		dir = ""
	}
	if dir == "" {
		return nil, fmt.Errorf("no source in tree for %v", name)
	}
	
	return w.importImpl(name, dir)
}

func (w *Walker) ImportPackage(name string, dir string) (*types.Package, error) {
	pkg := w.imported[name]
	if pkg != nil {
		if pkg == &importing {
			return nil, fmt.Errorf("cycle importing package %q", name)
		}
		return pkg, nil
	}
	w.imported[name] = &importing
	
	return w.importImpl(name, dir)
}

var parsedFileCache = make(map[string]*ast.File)

func (w *Walker) ParseFile(filename string) (*ast.File, error) {
	if f := parsedFileCache[filename]; f != nil {
		return f, nil
	}

	// fmt.Println(filename)
	fi, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	b, _ := ioutil.ReadAll(fi)

	f, err := parser.ParseFile(fset, filename, b, 0)
	if err != nil {
		return nil, err
	}
	parsedFileCache[filename] = f

	return f, nil
}

func (w *Walker) importImpl(name string, dir string) (*types.Package, error) {
	context := w.context
	if context == nil {
		context = &build.Default
	}

	// Look in cache.
	// If we've already done an import with the same set
	// of relevant tags, reuse the result.
	var key string
	if usePkgCache {
		if tags, ok := pkgTags[dir]; ok {
			key = tagKey(dir, context, tags)
			if pkg := pkgCache[key]; pkg != nil {
				w.imported[name] = pkg
				return pkg, nil
			}
		}
	}

	info, err := context.ImportDir(dir, 0)
	if err != nil {
		if _, nogo := err.(*build.NoGoError); nogo {
			return nil, nil
		}
		return nil, fmt.Errorf("pkg %q, dir %q: ScanDir: %v", name, dir, err)
	}

	// Save tags list first time we see a directory.
	if usePkgCache {
		if _, ok := pkgTags[dir]; !ok {
			pkgTags[dir] = info.AllTags
			key = tagKey(dir, context, info.AllTags)
		}
	}

	filenames := append(append([]string{}, info.GoFiles...), info.CgoFiles...)

	// Parse package files.
	var files []*ast.File
	for _, file := range filenames {
		filename := filepath.Join(dir, file)
		f, err := w.ParseFile(filename)
		if err != nil {
			return nil, fmt.Errorf("error parsing package %s: %s", name, err)
		}
		files = append(files, f)
	}

	// Type-check package files.
	conf := types.Config{
		IgnoreFuncBodies: true,
		FakeImportC:      true,
		Importer:         w,
		Error: func(err error) {
		},
	}

	pkg, err := conf.Check(name, fset, files, w.info)
	if err != nil {
		ctxt := "<no context>"
		if w.context != nil {
			ctxt = fmt.Sprintf("%s-%s", w.context.GOOS, w.context.GOARCH)
		}
		return nil, fmt.Errorf("error typechecking package %s: %s (%s)", name, err, ctxt)
	}
	if usePkgCache {
		pkgCache[key] = pkg
	}

	w.imported[name] = pkg
	return pkg, nil
}
