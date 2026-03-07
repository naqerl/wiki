package wiki

import (
        "embed"
        "encoding/json"
        stdhtml "html"
        "io/fs"
        "net/http"
        "os"
        "path/filepath"
        "regexp"
        "sort"
        "strconv"
        "strings"
        "sync"
        "time"

        "github.com/gomarkdown/markdown/ast"
        "github.com/gomarkdown/markdown/parser"

        "wiki/web/templates"
)

//go:embed views/*.html
var views embed.FS

type handler struct {
        t           templates.Templates
        content     fs.FS
        searchCache searchCache
}

type SearchDoc struct {
        ID      string `json:"id"`
        URL     string `json:"url"`
        Title   string `json:"title"`
        Section string `json:"section"`
        Body    string `json:"body"`
}

type searchSnapshot struct {
        maxModTime time.Time
        totalSize  int64
        fileCount  int
}

type searchCache struct {
        mu       sync.RWMutex
        snapshot searchSnapshot
        json     []byte
}

func InitMux(content fs.FS) *http.ServeMux {
        m := http.NewServeMux()
        h := handler{
                t:       templates.New(views),
                content: content,
        }

        m.HandleFunc("GET /{$}", h.index)
        m.HandleFunc("GET /search-index.json", h.searchIndex)
        m.HandleFunc("GET /{path...}", h.article)

        return m
}

type TreeNode struct {
        Name     string
        Path     string
        IsDir    bool
        Children []*TreeNode
}

type ArticleHeading struct {
        Level int
        ID    string
        Text  string
}

type ArticleHeadingNode struct {
        ID       string
        Text     string
        Children []*ArticleHeadingNode
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
        tree, err := h.buildTree(".")
        if err != nil {
                h.t.RenderError(w, r, "Error", "Failed to read directory", http.StatusInternalServerError)
                return
        }

        data := map[string]any{
                "Title": "wiki",
                "Tree":  tree,
        }
        h.t.RenderHTTP(w, r, "index", data)
}

func (h *handler) article(w http.ResponseWriter, r *http.Request) {
        path := r.PathValue("path")

        // Ensure path has .md extension
        if !strings.HasSuffix(path, ".md") {
                path += ".md"
        }

        // Read markdown file
        content, err := fs.ReadFile(h.content, path)
        if err != nil {
                h.t.RenderError(w, r, "Not Found", "Article not found", http.StatusNotFound)
                return
        }

        // Convert markdown to HTML
        html := templates.RenderMarkdown(content)
        headings := extractHeadingsFromHTML(string(html))
        headingTree := buildHeadingTree(headings)

        // Get display name (without .md extension)
        displayName := strings.TrimSuffix(path, ".md")

        data := map[string]any{
                "Title":       displayName,
                "Content":     html,
                "HeadingTree": headingTree,
        }
        h.t.RenderHTTP(w, r, "article", data)
}

func (h *handler) searchIndex(w http.ResponseWriter, r *http.Request) {
        snapshot, err := h.buildSearchSnapshot(".")
        if err != nil {
                h.t.RenderError(w, r, "Error", "Failed to build search snapshot", http.StatusInternalServerError)
                return
        }

        if payload, ok := h.getCachedSearchJSON(snapshot); ok {
                w.Header().Set("Content-Type", "application/json; charset=utf-8")
                _, _ = w.Write(payload)
                return
        }

        docs, err := h.buildSearchDocs(".")
        if err != nil {
                h.t.RenderError(w, r, "Error", "Failed to build search index", http.StatusInternalServerError)
                return
        }

        payload, err := json.Marshal(docs)
        if err != nil {
                h.t.RenderError(w, r, "Error", "Failed to serialize search index", http.StatusInternalServerError)
                return
        }

        h.setCachedSearchJSON(snapshot, payload)

        w.Header().Set("Content-Type", "application/json; charset=utf-8")
        _, _ = w.Write(payload)
}

func (h *handler) getCachedSearchJSON(snapshot searchSnapshot) ([]byte, bool) {
        h.searchCache.mu.RLock()
        defer h.searchCache.mu.RUnlock()

        if h.searchCache.snapshot == snapshot && len(h.searchCache.json) > 0 {
                return h.searchCache.json, true
        }
        return nil, false
}

func (h *handler) setCachedSearchJSON(snapshot searchSnapshot, payload []byte) {
        h.searchCache.mu.Lock()
        defer h.searchCache.mu.Unlock()
        h.searchCache.snapshot = snapshot
        h.searchCache.json = payload
}

func (h *handler) buildSearchSnapshot(dir string) (searchSnapshot, error) {
        var s searchSnapshot

        err := fs.WalkDir(h.content, dir, func(path string, d os.DirEntry, err error) error {
                if err != nil {
                        return err
                }
                if d.IsDir() || !strings.HasSuffix(path, ".md") {
                        return nil
                }

                info, err := d.Info()
                if err != nil {
                        return err
                }
                if info.ModTime().After(s.maxModTime) {
                        s.maxModTime = info.ModTime()
                }
                s.totalSize += info.Size()
                s.fileCount++
                return nil
        })

        return s, err
}

func (h *handler) buildSearchDocs(dir string) ([]SearchDoc, error) {
        docs := make([]SearchDoc, 0, 32)

        err := fs.WalkDir(h.content, dir, func(path string, d os.DirEntry, err error) error {
                if err != nil {
                        return err
                }
                if d.IsDir() || !strings.HasSuffix(path, ".md") {
                        return nil
                }

                raw, err := fs.ReadFile(h.content, path)
                if err != nil {
                        return err
                }

                title, _ := markdownToSearchFields(path, string(raw))
                chunks := buildSearchChunks(path, string(raw), title)
                docs = append(docs, chunks...)

                return nil
        })
        if err != nil {
                return nil, err
        }

        sort.Slice(docs, func(i, j int) bool {
                return docs[i].ID < docs[j].ID
        })

        return docs, nil
}

func markdownToSearchFields(path, markdown string) (string, string) {
        title := strings.TrimSuffix(filepath.Base(path), ".md")
        lines := strings.Split(markdown, "\n")

        for _, line := range lines {
                trimmed := strings.TrimSpace(line)
                if strings.HasPrefix(trimmed, "# ") {
                        candidate := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
                        if candidate != "" {
                                title = candidate
                                break
                        }
                }
        }

        body := strings.ReplaceAll(markdown, "\r\n", "\n")
        return title, body
}

func buildSearchChunks(path, markdown, title string) []SearchDoc {
        extensions := parser.CommonExtensions | parser.AutoHeadingIDs
        p := parser.NewWithExtensions(extensions)
        doc := p.Parse([]byte(markdown))

        chunks := make([]SearchDoc, 0, 16)
        currentSection := title
        currentAnchor := ""
        chunkCount := 0

        appendChunk := func(text string) {
                plain := normalizeWhitespace(text)
                if plain == "" {
                        return
                }
                chunkCount++
                url := "/" + path
                if currentAnchor != "" {
                        url += "#" + currentAnchor
                }
                chunks = append(chunks, SearchDoc{
                        ID:      path + "::" + strconv.Itoa(chunkCount),
                        URL:     url,
                        Title:   title,
                        Section: currentSection,
                        Body:    plain,
                })
        }

        ast.WalkFunc(doc, func(node ast.Node, entering bool) ast.WalkStatus {
                if !entering {
                        return ast.GoToNext
                }

                switch n := node.(type) {
                case *ast.Heading:
                        if text := extractNodeText(n); text != "" {
                                currentSection = text
                        }
                        if n.HeadingID != "" {
                                currentAnchor = n.HeadingID
                        }
                case *ast.Paragraph:
                        appendChunk(extractNodeText(n))
                case *ast.ListItem:
                        if !hasParagraphChild(n) {
                                appendChunk(extractNodeText(n))
                        }
                }
                return ast.GoToNext
        })

        if len(chunks) == 0 {
                if fallback := normalizeWhitespace(extractNodeText(doc)); fallback != "" {
                        chunks = append(chunks, SearchDoc{
                                ID:      path + "::1",
                                URL:     "/" + path,
                                Title:   title,
                                Section: title,
                                Body:    fallback,
                        })
                }
        }

        return chunks
}

func hasParagraphChild(node ast.Node) bool {
        for _, child := range node.GetChildren() {
                if _, ok := child.(*ast.Paragraph); ok {
                        return true
                }
        }
        return false
}

func extractNodeText(node ast.Node) string {
        var b strings.Builder

        ast.WalkFunc(node, func(n ast.Node, entering bool) ast.WalkStatus {
                if !entering {
                        return ast.GoToNext
                }

                switch n.(type) {
                case *ast.CodeBlock, *ast.HTMLBlock, *ast.MathBlock:
                        return ast.SkipChildren
                case *ast.Softbreak, *ast.Hardbreak, *ast.NonBlockingSpace:
                        b.WriteByte(' ')
                        return ast.GoToNext
                }

                if leaf := n.AsLeaf(); leaf != nil && len(leaf.Literal) > 0 {
                        b.Write(leaf.Literal)
                }
                return ast.GoToNext
        })

        return normalizeWhitespace(b.String())
}

func normalizeWhitespace(s string) string {
        return strings.Join(strings.Fields(s), " ")
}

func extractHeadingsFromHTML(renderedHTML string) []ArticleHeading {
        reHeading := regexp.MustCompile(`(?s)<h([2-6]) id="([^"]+)">(.*?)</h[2-6]>`)
        reTags := regexp.MustCompile(`<[^>]+>`)
        matches := reHeading.FindAllStringSubmatch(renderedHTML, -1)

        headings := make([]ArticleHeading, 0, 16)
        for _, m := range matches {
                level, err := strconv.Atoi(m[1])
                if err != nil {
                        continue
                }
                id := strings.TrimSpace(m[2])
                if id == "" {
                        continue
                }
                text := strings.TrimSpace(stdhtml.UnescapeString(reTags.ReplaceAllString(m[3], "")))
                if text == "" {
                        continue
                }

                headings = append(headings, ArticleHeading{
                        Level: level,
                        ID:    id,
                        Text:  text,
                })
        }

        return headings
}

func buildHeadingTree(headings []ArticleHeading) []*ArticleHeadingNode {
        if len(headings) == 0 {
                return nil
        }

        type stackEntry struct {
                level int
                node  *ArticleHeadingNode
        }

        root := &ArticleHeadingNode{}
        stack := []stackEntry{{level: 1, node: root}}

        for _, h := range headings {
                if h.Level < 2 || h.Level > 6 {
                        continue
                }

                n := &ArticleHeadingNode{
                        ID:       h.ID,
                        Text:     h.Text,
                        Children: make([]*ArticleHeadingNode, 0),
                }

                for len(stack) > 1 && stack[len(stack)-1].level >= h.Level {
                        stack = stack[:len(stack)-1]
                }

                parent := stack[len(stack)-1].node
                parent.Children = append(parent.Children, n)
                stack = append(stack, stackEntry{level: h.Level, node: n})
        }

        return root.Children
}

func (h *handler) buildTree(dir string) (*TreeNode, error) {
        root := &TreeNode{
                Name:     dir,
                Path:     "",
                IsDir:    true,
                Children: []*TreeNode{},
        }

        // Map to hold directories by path for quick lookup
        dirMap := map[string]*TreeNode{
                ".": root,
        }

        // First pass: collect all entries
        var allEntries []struct {
                path  string
                isDir bool
                name  string
        }

        err := fs.WalkDir(h.content, dir, func(path string, d os.DirEntry, err error) error {
                if err != nil {
                        return err
                }

                if path == "." {
                        return nil
                }

                isDir := d.IsDir()
                name := d.Name()

                // Skip hidden files and directories
                if strings.HasPrefix(name, ".") {
                        if isDir {
                                return fs.SkipDir
                        }
                        return nil
                }

                // Only include directories and .md files
                if !isDir && !strings.HasSuffix(path, ".md") {
                        return nil
                }

                allEntries = append(allEntries, struct {
                        path  string
                        isDir bool
                        name  string
                }{
                        path:  path,
                        isDir: isDir,
                        name:  d.Name(),
                })

                return nil
        })

        if err != nil {
                return nil, err
        }

        // Sort entries: directories first, then by name
        sort.Slice(allEntries, func(i, j int) bool {
                if allEntries[i].isDir != allEntries[j].isDir {
                        return allEntries[i].isDir
                }
                return allEntries[i].path < allEntries[j].path
        })

        // Second pass: build tree structure
        for _, entry := range allEntries {
                dir := filepath.Dir(entry.path)
                if dir == "." {
                        dir = "."
                }

                parent, exists := dirMap[dir]
                if !exists {
                        continue
                }

                node := &TreeNode{
                        Name:     entry.name,
                        Path:     entry.path,
                        IsDir:    entry.isDir,
                        Children: []*TreeNode{},
                }

                if entry.isDir {
                        dirMap[entry.path] = node
                } else {
                        // Strip .md extension for display name
                        node.Name = strings.TrimSuffix(entry.name, ".md")
                }

                parent.Children = append(parent.Children, node)
        }

        return root, nil
}
