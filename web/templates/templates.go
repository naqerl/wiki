package templates

import (
        "embed"
        "fmt"
        "html/template"
        "io"
        "io/fs"
        "log/slog"
        "net/http"

        "github.com/gomarkdown/markdown"
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
        w.Header().Set("Content-Type", "text/html")

        if err := t.Render(w, name, data); err != nil {
                slog.Error("Template execution error", "template", name, "error", err)
                http.Error(w, err.Error(), http.StatusInternalServerError)
        }
}

func (t Templates) RenderError(w http.ResponseWriter, r *http.Request, title, description string, status int) {
        w.WriteHeader(status)
        t.RenderHTTP(w, r, "error", map[string]any{
                "Title":       title,
                "Description": description,
        })
}

// RenderMarkdown converts markdown to HTML.
func RenderMarkdown(input []byte) template.HTML {
        extensions := parser.CommonExtensions | parser.AutoHeadingIDs
        p := parser.NewWithExtensions(extensions)

        opts := html.RendererOptions{
                Flags: html.CommonFlags | html.HrefTargetBlank,
        }
        renderer := html.NewRenderer(opts)
        output := markdown.ToHTML(input, p, renderer)

        return template.HTML(output)
}
