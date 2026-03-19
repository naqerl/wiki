package templates

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

//go:embed templates/*.html
var templatesFS embed.FS

type Templates struct {
	views   *template.Template
	funcMap template.FuncMap
}

func New(fsys fs.FS) Templates {
	funcMap := template.FuncMap{
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid dict call")
			}
			dict := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
	}

	views := template.Must(template.New("template").Funcs(funcMap).ParseFS(templatesFS, "templates/*.html"))
	views = template.Must(views.ParseFS(fsys, "views/*.html"))

	return Templates{
		views:   views,
		funcMap: funcMap,
	}
}

func (t Templates) Render(w io.Writer, name string, data any) error {
	return t.views.ExecuteTemplate(w, name, data)
}

func (t Templates) RenderHTTP(w http.ResponseWriter, r *http.Request, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := t.Render(w, name, data); err != nil {
		slog.Error("Template execution error", "template", name, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (t Templates) RenderError(w http.ResponseWriter, r *http.Request, title, description string, status int, extra map[string]any) {
	w.WriteHeader(status)
	data := map[string]any{
		"Title":       title,
		"Description": description,
	}
	for key, value := range extra {
		data[key] = value
	}
	t.RenderHTTP(w, r, "error", data)
}

// Reference represents an external link reference found in markdown.
type Reference struct {
	URL  string
	Text string
}

// RenderMarkdown converts markdown to HTML and extracts external references.
func RenderMarkdown(input []byte) (template.HTML, []Reference) {
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(input)

	refs := extractReferences(doc)

	opts := html.RendererOptions{
		Flags: html.CommonFlags | html.HrefTargetBlank,
	}
	renderer := html.NewRenderer(opts)
	output := markdown.Render(doc, renderer)

	return template.HTML(output), refs
}

func extractReferences(doc ast.Node) []Reference {
	seen := make(map[string]bool)
	var refs []Reference

	ast.WalkFunc(doc, func(node ast.Node, entering bool) ast.WalkStatus {
		if !entering {
			return ast.GoToNext
		}

		if link, ok := node.(*ast.Link); ok {
			dest := string(link.Destination)
			// Only external links (http/https)
			if !strings.HasPrefix(dest, "http://") && !strings.HasPrefix(dest, "https://") {
				return ast.GoToNext
			}
			// Skip if already seen
			if seen[dest] {
				return ast.GoToNext
			}

			seen[dest] = true
			text := extractLinkText(link)
			refs = append(refs, Reference{
				URL:  dest,
				Text: text,
			})
		}
		return ast.GoToNext
	})

	return refs
}

func extractLinkText(link *ast.Link) string {
	var b strings.Builder
	ast.WalkFunc(link, func(node ast.Node, entering bool) ast.WalkStatus {
		if !entering {
			return ast.GoToNext
		}
		if leaf := node.AsLeaf(); leaf != nil && len(leaf.Literal) > 0 {
			// Skip the link itself to avoid including the URL as text
			if _, ok := node.(*ast.Link); ok {
				return ast.GoToNext
			}
			b.Write(leaf.Literal)
		}
		return ast.GoToNext
	})
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "Link"
	}
	return text
}
