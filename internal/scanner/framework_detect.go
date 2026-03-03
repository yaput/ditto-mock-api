package scanner

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

const (
	FrameworkChi     = "chi"
	FrameworkGin     = "gin"
	FrameworkEcho    = "echo"
	FrameworkGorilla = "gorilla"
	FrameworkStdlib  = "stdlib"
	FrameworkUnknown = "unknown"
)

var frameworkImports = map[string]string{
	"github.com/go-chi/chi":       FrameworkChi,
	"github.com/go-chi/chi/v5":    FrameworkChi,
	"github.com/gin-gonic/gin":    FrameworkGin,
	"github.com/labstack/echo":    FrameworkEcho,
	"github.com/labstack/echo/v4": FrameworkEcho,
	"github.com/gorilla/mux":      FrameworkGorilla,
}

// DetectFramework scans Go files to detect which HTTP framework is used.
func DetectFramework(repoPath string, scanDirs []string) string {
	counts := make(map[string]int)

	for _, dir := range scanDirs {
		scanDir := filepath.Join(repoPath, dir)
		_ = filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}

			fset := token.NewFileSet()
			node, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if parseErr != nil {
				return nil
			}

			for _, imp := range node.Imports {
				impPath := strings.Trim(imp.Path.Value, `"`)
				if fw, ok := frameworkImports[impPath]; ok {
					counts[fw]++
				}
				if impPath == "net/http" {
					counts[FrameworkStdlib]++
				}
			}
			return nil
		})
	}

	if len(counts) == 0 {
		return FrameworkUnknown
	}

	best := FrameworkUnknown
	bestCount := 0
	for fw, count := range counts {
		if fw == FrameworkStdlib {
			continue
		}
		if count > bestCount {
			best = fw
			bestCount = count
		}
	}

	if best == FrameworkUnknown && counts[FrameworkStdlib] > 0 {
		return FrameworkStdlib
	}

	return best
}

// DetectFrameworkFromImports checks a list of import paths and returns the detected framework.
func DetectFrameworkFromImports(imports []string) string {
	for _, imp := range imports {
		if fw, ok := frameworkImports[imp]; ok {
			return fw
		}
	}
	for _, imp := range imports {
		if imp == "net/http" {
			return FrameworkStdlib
		}
	}
	return FrameworkUnknown
}
