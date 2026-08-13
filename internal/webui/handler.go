package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded console and falls back to index.html for SPA routes.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		cleaned := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if cleaned == "" || cleaned == "." {
			cleaned = "index.html"
		}
		if f, err := sub.Open(cleaned); err == nil {
			_ = f.Close()
			files.ServeHTTP(w, r)
			return
		}
		// Vue history 路由没有对应静态文件时回 index.html。
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
