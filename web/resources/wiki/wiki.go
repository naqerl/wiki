package wiki

import (
	"bytes"
	"embed"
	"encoding/json"
	"encoding/xml"
	stdhtml "html"
	htemplate "html/template"
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

// RSSFeed represents an RSS 2.0 feed
type RSSFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel RSSChannel `xml:"channel"`
}

// RSSChannel represents the channel element in RSS
type RSSChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	LastBuildDate string    `xml:"lastBuildDate"`
	Items         []RSSItem `xml:"item"`
}

// RSSItem represents an individual item in the RSS feed
type RSSItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

//go:embed views/*.html
var views embed.FS

//go:embed static/css/*.css static/js/*.js
var static embed.FS

type handler struct {
	t           templates.Templates
	content     fs.FS
	baseURL     string
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

type articleSummary struct {
	Title       string
	Description string
	Path        string
	URL         string
	LastMod     time.Time
}

type breadcrumb struct {
	Name string
	URL  string
}

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

const (
	siteTitle       = "wiki"
	siteDescription = "Small self-hosted wiki for Markdown files."
)

func InitMux(content fs.FS, baseURL string) *http.ServeMux {
	baseURL = strings.TrimRight(baseURL, "/")

	m := http.NewServeMux()
	h := handler{
		t:       templates.New(views),
		content: content,
		baseURL: baseURL,
	}

	// Serve static files (CSS and JS)
	staticFS, err := fs.Sub(static, "static")
	if err == nil {
		m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	}

	m.HandleFunc("GET /{$}", h.index)
	m.HandleFunc("GET /robots.txt", h.robotsTxt)
	m.HandleFunc("GET /sitemap.xml", h.sitemap)
	m.HandleFunc("GET /rss.xml", h.rssFeed)
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
		h.renderError(w, r, "Error", "Failed to read directory", http.StatusInternalServerError)
		return
	}

	snapshot, err := h.buildSearchSnapshot(".")
	if err == nil && writeNotModified(w, r, snapshot.maxModTime) {
		return
	}
	setLastModified(w, snapshot.maxModTime)

	structuredData := mustJSONLD(map[string]any{
		"@context":    "https://schema.org",
		"@type":       "WebSite",
		"name":        siteTitle,
		"description": siteDescription,
		"url":         h.absoluteURL("/"),
	})

	data := map[string]any{
		"Title":          siteTitle,
		"MetaTitle":      siteTitle,
		"Description":    siteDescription,
		"Tree":           tree,
		"BaseURL":        h.baseURL,
		"CanonicalURL":   h.absoluteURL("/"),
		"StructuredData": structuredData,
	}
	h.t.RenderHTTP(w, r, "index", data)
}

func (h *handler) article(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.PathValue("path"), "/")
	if path == "" {
		h.renderError(w, r, "Not Found", "Article not found", http.StatusNotFound)
		return
	}

	if !strings.HasSuffix(path, ".md") {
		target := "/" + path + ".md"
		if raw := r.URL.RawQuery; raw != "" {
			target += "?" + raw
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	content, err := fs.ReadFile(h.content, path)
	if err != nil {
		h.renderError(w, r, "Not Found", "Article not found", http.StatusNotFound)
		return
	}

	var lastModified time.Time
	if stat, err := fs.Stat(h.content, path); err == nil {
		lastModified = stat.ModTime()
	}
	if writeNotModified(w, r, lastModified) {
		return
	}
	setLastModified(w, lastModified)

	title := extractTitleFromMarkdown(path, string(content))
	description := extractDescriptionFromMarkdown(string(content))
	if description == "" {
		description = siteDescription
	}

	html, refs := templates.RenderMarkdown(content)
	headings := extractHeadingsFromHTML(string(html))
	headingTree := buildHeadingTree(headings)
	canonicalPath := h.articlePath(path)
	canonicalURL := h.absoluteURL(canonicalPath)
	breadcrumbs := buildBreadcrumbs(path)
	related, err := h.buildRelatedArticles(path)
	if err != nil {
		related = nil
	}

	structuredData := mustJSONLD(map[string]any{
		"@context":         "https://schema.org",
		"@type":            "TechArticle",
		"headline":         title,
		"description":      description,
		"url":              canonicalURL,
		"mainEntityOfPage": canonicalURL,
		"dateModified":     formatISOTime(lastModified),
		"isPartOf": map[string]any{
			"@type": "WebSite",
			"name":  siteTitle,
			"url":   h.absoluteURL("/"),
		},
	})

	data := map[string]any{
		"Title":          title,
		"MetaTitle":      title + " | " + siteTitle,
		"Description":    description,
		"Content":        html,
		"HeadingTree":    headingTree,
		"LastModified":   lastModified,
		"CanonicalURL":   canonicalURL,
		"StructuredData": structuredData,
		"Breadcrumbs":    breadcrumbs,
		"Related":        related,
		"References":     refs,
	}
	h.t.RenderHTTP(w, r, "article", data)
}

func (h *handler) searchIndex(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.buildSearchSnapshot(".")
	if err != nil {
		h.renderError(w, r, "Error", "Failed to build search snapshot", http.StatusInternalServerError)
		return
	}

	if payload, ok := h.getCachedSearchJSON(snapshot); ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		setLastModified(w, snapshot.maxModTime)
		_, _ = w.Write(payload)
		return
	}

	docs, err := h.buildSearchDocs(".")
	if err != nil {
		h.renderError(w, r, "Error", "Failed to build search index", http.StatusInternalServerError)
		return
	}

	payload, err := json.Marshal(docs)
	if err != nil {
		h.renderError(w, r, "Error", "Failed to serialize search index", http.StatusInternalServerError)
		return
	}

	h.setCachedSearchJSON(snapshot, payload)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setLastModified(w, snapshot.maxModTime)
	_, _ = w.Write(payload)
}

func (h *handler) rssFeed(w http.ResponseWriter, r *http.Request) {
	items, err := h.buildRSSItems(".")
	if err != nil {
		h.renderError(w, r, "Error", "Failed to build RSS feed", http.StatusInternalServerError)
		return
	}

	snapshot, _ := h.buildSearchSnapshot(".")
	if writeNotModified(w, r, snapshot.maxModTime) {
		return
	}
	setLastModified(w, snapshot.maxModTime)

	feed := RSSFeed{
		Version: "2.0",
		Channel: RSSChannel{
			Title:         siteTitle,
			Link:          h.baseURL,
			Description:   siteDescription,
			LastBuildDate: time.Now().UTC().Format(time.RFC1123),
			Items:         items,
		},
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(feed); err != nil {
		h.renderError(w, r, "Error", "Failed to encode RSS feed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (h *handler) robotsTxt(w http.ResponseWriter, r *http.Request) {
	body := "User-agent: *\nAllow: /\nSitemap: " + h.absoluteURL("/sitemap.xml") + "\n"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (h *handler) sitemap(w http.ResponseWriter, r *http.Request) {
	articles, err := h.collectArticleSummaries(".")
	if err != nil {
		h.renderError(w, r, "Error", "Failed to build sitemap", http.StatusInternalServerError)
		return
	}

	var latest time.Time
	urls := make([]sitemapURL, 0, len(articles)+1)
	urls = append(urls, sitemapURL{
		Loc:     h.absoluteURL("/"),
		LastMod: formatISODate(maxArticleModTime(articles)),
	})
	for _, article := range articles {
		if article.LastMod.After(latest) {
			latest = article.LastMod
		}
		urls = append(urls, sitemapURL{
			Loc:     article.URL,
			LastMod: formatISODate(article.LastMod),
		})
	}
	if writeNotModified(w, r, latest) {
		return
	}
	setLastModified(w, latest)

	payload := sitemapURLSet{
		XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  urls,
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(payload); err != nil {
		h.renderError(w, r, "Error", "Failed to encode sitemap", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (h *handler) buildRSSItems(dir string) ([]RSSItem, error) {
	items := make([]RSSItem, 0, 32)

	err := fs.WalkDir(h.content, dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if shouldSkipEntry(path, d) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		raw, err := fs.ReadFile(h.content, path)
		if err != nil {
			return err
		}

		// Get file modification time
		var modTime time.Time
		if info, err := d.Info(); err == nil {
			modTime = info.ModTime()
		}

		// Extract title from markdown
		title := extractTitleFromMarkdown(path, string(raw))

		// Extract description (first paragraph or first 200 chars)
		description := extractDescriptionFromMarkdown(string(raw))

		// Build URL
		url := h.absoluteURL(h.articlePath(path))

		items = append(items, RSSItem{
			Title:       title,
			Link:        url,
			Description: description,
			PubDate:     modTime.UTC().Format(time.RFC1123),
			GUID:        url,
		})

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort by pub date descending (newest first)
	sort.Slice(items, func(i, j int) bool {
		return items[i].PubDate > items[j].PubDate
	})

	return items, nil
}

func extractTitleFromMarkdown(path, markdown string) string {
	// Try to get title from first H1 heading
	lines := strings.Split(markdown, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			candidate := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			if candidate != "" {
				return candidate
			}
		}
	}
	// Fallback to filename without extension
	return strings.TrimSuffix(filepath.Base(path), ".md")
}

func extractDescriptionFromMarkdown(markdown string) string {
	// Parse markdown to extract first paragraph
	extensions := parser.CommonExtensions
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse([]byte(markdown))

	var firstParagraph strings.Builder
	found := false

	ast.WalkFunc(doc, func(node ast.Node, entering bool) ast.WalkStatus {
		if !entering || found {
			return ast.GoToNext
		}

		switch n := node.(type) {
		case *ast.Paragraph:
			text := extractNodeText(n)
			if text != "" {
				firstParagraph.WriteString(text)
				found = true
				return ast.SkipChildren
			}
		}
		return ast.GoToNext
	})

	desc := normalizeWhitespace(firstParagraph.String())
	if len(desc) > 300 {
		desc = desc[:300] + "..."
	}
	return desc
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
		if shouldSkipEntry(path, d) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
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
		if shouldSkipEntry(path, d) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
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

func (h *handler) renderError(w http.ResponseWriter, r *http.Request, title, description string, status int) {
	canonicalURL := h.absoluteURL(r.URL.Path)
	h.t.RenderError(w, r, title, description, status, map[string]any{
		"MetaTitle":    title + " | " + siteTitle,
		"CanonicalURL": canonicalURL,
	})
}

func (h *handler) absoluteURL(path string) string {
	if path == "" || path == "/" {
		return h.baseURL + "/"
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return h.baseURL + path
}

func (h *handler) articlePath(path string) string {
	return "/" + strings.TrimPrefix(path, "/")
}

func (h *handler) collectArticleSummaries(dir string) ([]articleSummary, error) {
	articles := make([]articleSummary, 0, 32)

	err := fs.WalkDir(h.content, dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if shouldSkipEntry(path, d) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		raw, err := fs.ReadFile(h.content, path)
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		title := extractTitleFromMarkdown(path, string(raw))
		description := extractDescriptionFromMarkdown(string(raw))
		articles = append(articles, articleSummary{
			Title:       title,
			Description: description,
			Path:        path,
			URL:         h.absoluteURL(h.articlePath(path)),
			LastMod:     info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(articles, func(i, j int) bool {
		if articles[i].Path == articles[j].Path {
			return articles[i].Title < articles[j].Title
		}
		return articles[i].Path < articles[j].Path
	})

	return articles, nil
}

func (h *handler) buildRelatedArticles(currentPath string) ([]articleSummary, error) {
	articles, err := h.collectArticleSummaries(".")
	if err != nil {
		return nil, err
	}

	currentDir := filepath.Dir(currentPath)
	related := make([]articleSummary, 0, 5)
	for _, article := range articles {
		if article.Path == currentPath {
			continue
		}
		if filepath.Dir(article.Path) == currentDir {
			related = append(related, article)
		}
	}
	if len(related) == 0 {
		for _, article := range articles {
			if article.Path == currentPath {
				continue
			}
			related = append(related, article)
			if len(related) == 5 {
				break
			}
		}
	}
	if len(related) > 5 {
		related = related[:5]
	}
	return related, nil
}

func buildBreadcrumbs(articlePath string) []breadcrumb {
	segments := strings.Split(strings.TrimSuffix(articlePath, ".md"), "/")
	if len(segments) <= 1 {
		return nil
	}

	crumbs := make([]breadcrumb, 0, len(segments)-1)
	for _, segment := range segments[:len(segments)-1] {
		crumbs = append(crumbs, breadcrumb{Name: segment})
	}
	return crumbs
}

func writeNotModified(w http.ResponseWriter, r *http.Request, lastModified time.Time) bool {
	if lastModified.IsZero() {
		return false
	}
	lastModified = lastModified.UTC().Truncate(time.Second)
	ifModifiedSince := r.Header.Get("If-Modified-Since")
	if ifModifiedSince == "" {
		return false
	}
	t, err := time.Parse(http.TimeFormat, ifModifiedSince)
	if err != nil {
		return false
	}
	if !lastModified.After(t.UTC()) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

func setLastModified(w http.ResponseWriter, lastModified time.Time) {
	if !lastModified.IsZero() {
		w.Header().Set("Last-Modified", lastModified.UTC().Truncate(time.Second).Format(http.TimeFormat))
	}
}

func shouldSkipEntry(path string, d os.DirEntry) bool {
	if path == "." {
		return false
	}
	base := filepath.Base(path)
	return strings.HasPrefix(base, ".")
}

func mustJSONLD(v any) htemplate.JS {
	payload, err := json.Marshal(v)
	if err != nil {
		return htemplate.JS("{}")
	}
	return htemplate.JS(payload)
}

func maxArticleModTime(articles []articleSummary) time.Time {
	var latest time.Time
	for _, article := range articles {
		if article.LastMod.After(latest) {
			latest = article.LastMod
		}
	}
	return latest
}

func formatISODate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func formatISOTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
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

	// Prune empty directories - only keep directories that contain files
	root.Children = pruneEmptyDirs(root.Children)

	return root, nil
}

// pruneEmptyDirs recursively removes directories with no files
func pruneEmptyDirs(nodes []*TreeNode) []*TreeNode {
	result := make([]*TreeNode, 0, len(nodes))
	for _, node := range nodes {
		if node.IsDir {
			// Recursively prune children first
			node.Children = pruneEmptyDirs(node.Children)
			// Only keep this directory if it has children (files or non-empty subdirs)
			if len(node.Children) > 0 {
				result = append(result, node)
			}
		} else {
			// Keep all files
			result = append(result, node)
		}
	}
	return result
}
