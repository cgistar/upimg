package webui

import (
	"bytes"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed dist/*
var dist embed.FS

func Handler(prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, prefix)
		name = strings.TrimLeft(strings.ReplaceAll(name, "\\", "/"), "/")
		if name == "" {
			name = "index.html"
		}
		if strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}

		data, err := dist.ReadFile(path.Join("dist", name))
		if err != nil {
			if isAssetPath(name) {
				http.NotFound(w, r)
				return
			}
			data, err = dist.ReadFile("dist/index.html")
			if err != nil {
				http.Error(w, "admin ui is not built", http.StatusNotFound)
				return
			}
			name = "index.html"
		}

		if contentType := mime.TypeByExtension(filepath.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if name == "index.html" {
			data = injectBasePath(data, r.Header.Get("X-Upimg-Base-Path"))
		}
		_, _ = w.Write(data)
	})
}

func DevProxy(rawURL string) (http.Handler, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, url.InvalidHostError(rawURL)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalDirector(r)
		r.Host = target.Host
		r.Header.Del("Accept-Encoding")
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		if !isHTMLResponse(response) {
			return nil
		}
		data, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		data = injectBasePath(data, response.Request.Header.Get("X-Upimg-Base-Path"))
		response.Body = io.NopCloser(bytes.NewReader(data))
		response.ContentLength = int64(len(data))
		response.Header.Set("Content-Length", strconv.Itoa(len(data)))
		response.Header.Del("Content-Encoding")
		return nil
	}
	return proxy, nil
}

func isAssetPath(name string) bool {
	if strings.HasPrefix(name, "assets/") {
		return true
	}
	_, err := fs.Stat(dist, path.Join("dist", name))
	return err != nil && filepath.Ext(name) != ""
}

func injectBasePath(data []byte, basePath string) []byte {
	payload, err := json.Marshal(map[string]string{"basePath": strings.TrimRight(basePath, "/")})
	if err != nil {
		return data
	}
	script := []byte("<script>window.__UPIMG_ADMIN__=" + string(payload) + ";</script>")
	if bytes.Contains(data, script) {
		return data
	}
	if index := bytes.Index(data, []byte("</head>")); index >= 0 {
		result := make([]byte, 0, len(data)+len(script))
		result = append(result, data[:index]...)
		result = append(result, script...)
		result = append(result, data[index:]...)
		return result
	}
	return append(script, data...)
}

func isHTMLResponse(response *http.Response) bool {
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") {
		return true
	}
	if response.Request == nil || response.Request.URL == nil {
		return false
	}
	return strings.HasSuffix(response.Request.URL.Path, "/") || strings.HasSuffix(response.Request.URL.Path, "index.html")
}
