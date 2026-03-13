package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"wiki/web/resources/wiki"
)

func main() {
	var port string
	var contentPath string
	flag.StringVar(&port, "port", "8080", "Port to run the server on")
	flag.StringVar(&contentPath, "content", ".", "Path to content directory")
	flag.Parse()

	content := os.DirFS(contentPath)
	baseURL := os.Getenv("WIKI_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:" + port
	}

	mux := http.NewServeMux()
	mux.Handle("/", wiki.InitMux(content, baseURL))

	slog.Info("Server starting", "port", port, "content", contentPath)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}
