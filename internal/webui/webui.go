// Package webui serves the embedded management UI.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed static
var content embed.FS

// Handler returns an HTTP handler for the embedded UI assets.
func Handler() http.Handler {
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic("webui: embedded assets are missing: " + err.Error())
	}

	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Cache-Control", "no-cache")
		if serveSPAIndex(w, r, sub) {
			return
		}
		files.ServeHTTP(w, r)
	})
}

// serveSPAIndex returns index.html for browser navigations that do not match an
// embedded asset, so inventory routes such as /vms/new survive a refresh.
func serveSPAIndex(w http.ResponseWriter, r *http.Request, root fs.FS) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html") {
		return false
	}
	if r.URL.Path == "/" {
		return false
	}
	clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if clean == "" || clean == "." {
		return false
	}
	if info, err := fs.Stat(root, clean); err == nil && !info.IsDir() {
		return false
	}
	if path.Ext(clean) != "" {
		return false
	}
	data, err := fs.ReadFile(root, "index.html")
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = w.Write(data)
	return true
}
