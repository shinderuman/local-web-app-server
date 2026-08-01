package server

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type staticHandler struct {
	root string
}

func (handler staticHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	escaped := request.URL.EscapedPath()
	decoded, err := url.PathUnescape(escaped)
	if err != nil || hasUnsafePathSegment(decoded) {
		http.Error(writer, "invalid path", http.StatusBadRequest)
		return
	}
	cleaned := path.Clean("/" + decoded)
	relative := strings.TrimPrefix(cleaned, "/")
	if relative == "" {
		relative = "index.html"
	}
	target := filepath.Join(handler.root, filepath.FromSlash(relative))
	info, err := os.Stat(target)
	if err == nil && info.IsDir() {
		target = filepath.Join(target, "index.html")
		info, err = os.Stat(target)
	}
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(writer, request)
		return
	}
	if err != nil || !info.Mode().IsRegular() {
		http.Error(writer, "file unavailable", http.StatusForbidden)
		return
	}
	http.ServeFile(writer, request, target)
}

func hasUnsafePathSegment(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return strings.ContainsRune(value, 0)
}
