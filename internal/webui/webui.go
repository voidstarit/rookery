// Package webui serves the embedded management UI.
package webui

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
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

		cacheControl := "no-cache"
		if strings.HasPrefix(r.URL.Path, "/webshark/") &&
			!strings.HasSuffix(r.URL.Path, "/") &&
			r.URL.Path != "/webshark/index.html" {
			cacheControl = "public, max-age=86400"
		}
		w.Header().Set("Cache-Control", cacheControl)
		if serveCompressedWASM(w, r, sub) {
			return
		}
		files.ServeHTTP(w, r)
	})
}

// WebShark ships gzipped Wiregasm assets. Serve the compressed files at the
// uncompressed URLs so the embedded UI stays practical in the Go binary.
func serveCompressedWASM(w http.ResponseWriter, r *http.Request, root fs.FS) bool {
	var path, contentType string
	switch r.URL.Path {
	case "/webshark/assets/wiregasm/wiregasm.wasm":
		path = "webshark/assets/wiregasm/wiregasm.wasm.gz"
		contentType = "application/wasm"
	case "/webshark/assets/wiregasm/wiregasm.data":
		path = "webshark/assets/wiregasm/wiregasm.data.gz"
		contentType = "application/octet-stream"
	default:
		return false
	}
	file, err := root.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Vary", "Accept-Encoding")
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = io.Copy(w, file)
	return true
}
