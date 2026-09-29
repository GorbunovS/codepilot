package chunk

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// chunkGo чанкит Go-файлы через go/parser + go/ast:
// func/method/type/var/const верхнего уровня вместе с doc-комментариями.
func chunkGo(relPath string, src []byte) []Chunk {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, relPath, src, parser.ParseComments)
	if err != nil {
		return Fallback(relPath, "go", src)
	}
	lines := Lines(src)
	hash := HashBytes(src)
	var out []Chunk

	slice := func(start, end token.Pos) string {
		s := fset.Position(start).Offset
		e := fset.Position(end).Offset
		if s < 0 || e > len(src) || s >= e {
			return ""
		}
		return strings.TrimSpace(string(src[s:e]))
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			start := fset.Position(d.Pos()).Line
			if d.Doc != nil {
				start = fset.Position(d.Doc.Pos()).Line
			}
			end := fset.Position(d.End()).Line
			sig := slice(d.Pos(), d.End())
			if d.Body != nil {
				sig = slice(d.Pos(), d.Body.Pos())
			}
			doc := ""
			if d.Doc != nil {
				doc = strings.TrimSpace(d.Doc.Text())
			}
			kind := "func"
			if d.Recv != nil {
				kind = "method"
			}
			out = append(out, newChunk(relPath, "go", start, end, d.Name.Name, kind, sig, doc, lines, hash))
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			kind := strings.ToLower(d.Tok.String())
			declDoc := ""
			if d.Doc != nil {
				declDoc = strings.TrimSpace(d.Doc.Text())
			}
			for _, spec := range d.Specs {
				var name string
				doc := declDoc
				var start, end int
				switch s := spec.(type) {
				case *ast.ValueSpec:
					if len(s.Names) == 0 {
						continue
					}
					name = s.Names[0].Name
					if s.Doc != nil {
						doc = strings.TrimSpace(s.Doc.Text())
					}
					start = fset.Position(s.Pos()).Line
					end = fset.Position(s.End()).Line
				case *ast.TypeSpec:
					name = s.Name.Name
					if s.Doc != nil {
						doc = strings.TrimSpace(s.Doc.Text())
					}
					start = fset.Position(s.Pos()).Line
					end = fset.Position(s.End()).Line
				default:
					continue
				}
				if d.Doc != nil {
					start = fset.Position(d.Doc.Pos()).Line
				}
				sig := slice(spec.Pos(), spec.End())
				out = append(out, newChunk(relPath, "go", start, end, name, kind, sig, doc, lines, hash))
			}
		}
	}
	if len(out) == 0 {
		return Fallback(relPath, "go", src)
	}
	return out
}
