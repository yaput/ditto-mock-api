package scanner

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// ExtractFromDir parses all .go files in a directory (recursively) and extracts
// struct definitions, route registrations, and handler functions.
func ExtractFromDir(dir string) (
	structs []models.ExtractedStruct,
	routes []models.ExtractedRoute,
	handlers []models.ExtractedHandler,
	err error,
) {
	err = filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || (strings.HasPrefix(base, ".") && path != dir) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		node, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil
		}

		relPath, _ := filepath.Rel(dir, path)

		s := extractStructs(node, relPath)
		structs = append(structs, s...)

		r := extractRoutes(node, fset, relPath)
		routes = append(routes, r...)

		h := extractHandlers(node, fset, relPath)
		handlers = append(handlers, h...)

		return nil
	})

	return structs, routes, handlers, err
}

func extractStructs(file *ast.File, relPath string) []models.ExtractedStruct {
	var result []models.ExtractedStruct
	pkgName := file.Name.Name

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}

			fields := extractStructFields(structType)
			result = append(result, models.ExtractedStruct{
				Name:    typeSpec.Name.Name,
				Package: pkgName,
				File:    relPath,
				Fields:  fields,
			})
		}
	}
	return result
}

func extractStructFields(st *ast.StructType) []models.StructField {
	var fields []models.StructField
	if st.Fields == nil {
		return fields
	}

	for _, field := range st.Fields.List {
		typeName := typeToString(field.Type)
		jsonTag, omitempty := parseJSONTag(field.Tag)

		if len(field.Names) == 0 {
			fields = append(fields, models.StructField{
				Name:      typeName,
				Type:      typeName,
				JSONTag:   jsonTag,
				Omitempty: omitempty,
			})
			continue
		}

		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			tag := jsonTag
			if tag == "" {
				tag = strings.ToLower(name.Name)
			}
			fields = append(fields, models.StructField{
				Name:      name.Name,
				Type:      typeName,
				JSONTag:   tag,
				Omitempty: omitempty,
			})
		}
	}
	return fields
}

func parseJSONTag(tag *ast.BasicLit) (string, bool) {
	if tag == nil {
		return "", false
	}
	raw := strings.Trim(tag.Value, "`")

	idx := strings.Index(raw, `json:"`)
	if idx < 0 {
		return "", false
	}
	val := raw[idx+6:]
	end := strings.Index(val, `"`)
	if end < 0 {
		return "", false
	}
	val = val[:end]

	parts := strings.Split(val, ",")
	key := parts[0]
	if key == "-" {
		return "-", false
	}

	omitempty := false
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) == "omitempty" {
			omitempty = true
		}
	}

	return key, omitempty
}

func typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", typeToString(t.X), t.Sel.Name)
	case *ast.StarExpr:
		return "*" + typeToString(t.X)
	case *ast.ArrayType:
		return "[]" + typeToString(t.Elt)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", typeToString(t.Key), typeToString(t.Value))
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + typeToString(t.Elt)
	default:
		return "unknown"
	}
}

func extractRoutes(file *ast.File, fset *token.FileSet, relPath string) []models.ExtractedRoute {
	var routes []models.ExtractedRoute

	ast.Inspect(file, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		method, path, handler := parseRouteCall(sel, callExpr)
		if method == "" || path == "" {
			return true
		}

		pos := fset.Position(callExpr.Pos())
		routes = append(routes, models.ExtractedRoute{
			Method:  method,
			Path:    path,
			Handler: handler,
			File:    relPath,
			Line:    pos.Line,
		})

		return true
	})

	return routes
}

func parseRouteCall(sel *ast.SelectorExpr, call *ast.CallExpr) (method, path, handler string) {
	methodName := sel.Sel.Name

	upper := strings.ToUpper(methodName)
	switch upper {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		if len(call.Args) >= 2 {
			path = extractStringLit(call.Args[0])
			handler = exprToString(call.Args[1])
			return upper, path, handler
		}
	}

	if methodName == "HandleFunc" || methodName == "Handle" {
		if len(call.Args) >= 2 {
			path = extractStringLit(call.Args[0])
			handler = exprToString(call.Args[1])
			return "ANY", path, handler
		}
	}

	return "", "", ""
}

func extractStringLit(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	return strings.Trim(lit.Value, `"`)
}

func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", exprToString(e.X), e.Sel.Name)
	case *ast.FuncLit:
		return "<anonymous>"
	default:
		return "<unknown>"
	}
}

func extractHandlers(file *ast.File, fset *token.FileSet, relPath string) []models.ExtractedHandler {
	var handlers []models.ExtractedHandler

	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Name == nil || !funcDecl.Name.IsExported() {
			continue
		}

		if !isHTTPHandler(funcDecl) {
			continue
		}

		receiver := ""
		if funcDecl.Recv != nil && len(funcDecl.Recv.List) > 0 {
			receiver = typeToString(funcDecl.Recv.List[0].Type)
		}

		decodes, encodes, statusCodes := analyzeHandlerBody(funcDecl.Body)

		handlers = append(handlers, models.ExtractedHandler{
			Name:        funcDecl.Name.Name,
			Receiver:    receiver,
			File:        relPath,
			Decodes:     decodes,
			Encodes:     encodes,
			StatusCodes: statusCodes,
		})
	}

	return handlers
}

func isHTTPHandler(funcDecl *ast.FuncDecl) bool {
	params := funcDecl.Type.Params
	if params == nil {
		return false
	}

	for _, param := range params.List {
		typStr := typeToString(param.Type)
		if strings.Contains(typStr, "ResponseWriter") ||
			strings.Contains(typStr, "Request") ||
			strings.Contains(typStr, "Context") {
			return true
		}
	}
	return false
}

func analyzeHandlerBody(body *ast.BlockStmt) (decodes, encodes string, statusCodes []int) {
	if body == nil {
		return "", "", nil
	}

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		name := sel.Sel.Name

		switch name {
		case "Decode", "Unmarshal", "Bind", "ShouldBindJSON", "BindJSON", "ShouldBind":
			if len(call.Args) > 0 {
				decodes = lastTypeName(call.Args[len(call.Args)-1])
			}
		case "Encode", "Marshal", "JSON", "Render", "JSONP":
			if len(call.Args) > 0 {
				encodes = lastTypeName(call.Args[len(call.Args)-1])
			}
		case "WriteHeader":
			if len(call.Args) > 0 {
				if code := extractIntLit(call.Args[0]); code > 0 {
					statusCodes = append(statusCodes, code)
				}
			}
		}

		if (name == "JSON" || name == "JSONP") && len(call.Args) >= 2 {
			if code := extractIntLit(call.Args[0]); code > 0 {
				statusCodes = append(statusCodes, code)
			}
		}

		return true
	})

	return decodes, encodes, statusCodes
}

func lastTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		return lastTypeName(e.X)
	case *ast.CompositeLit:
		return typeToString(e.Type)
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprToString(e)
	default:
		return ""
	}
}

func extractIntLit(expr ast.Expr) int {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.INT {
			var v int
			fmt.Sscanf(e.Value, "%d", &v)
			return v
		}
	case *ast.SelectorExpr:
		return httpStatusConstToInt(e.Sel.Name)
	}
	return 0
}

func httpStatusConstToInt(name string) int {
	m := map[string]int{
		"StatusOK":                  200,
		"StatusCreated":             201,
		"StatusAccepted":            202,
		"StatusNoContent":           204,
		"StatusBadRequest":          400,
		"StatusUnauthorized":        401,
		"StatusForbidden":           403,
		"StatusNotFound":            404,
		"StatusConflict":            409,
		"StatusInternalServerError": 500,
		"StatusServiceUnavailable":  503,
	}
	if v, ok := m[name]; ok {
		return v
	}
	return 0
}
