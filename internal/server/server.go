package server

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"upimg/internal/config"
	"upimg/internal/naming"
	"upimg/internal/storage"
)

const maxUploadSize = 1024 * 1024 * 1024

type App struct {
	runtime    config.Runtime
	backend    storage.Backend
	local      storage.Backend
	s3Targets  map[string]config.S3Config
	s3Backends map[string]storage.Backend
	s3Factory  func(context.Context, config.S3Config) (storage.Backend, error)
	s3Mu       sync.Mutex
}

type UploadResult struct {
	FileName string `json:"fileName"`
	ImgURL   string `json:"imgUrl"`
	Type     string `json:"type"`
}

type UploadResponse struct {
	Success    bool           `json:"success"`
	Result     []string       `json:"result,omitempty"`
	FullResult []UploadResult `json:"fullResult,omitempty"`
	Message    string         `json:"message,omitempty"`
}

type ListResponse struct {
	Success bool             `json:"success"`
	Result  []storage.Object `json:"result,omitempty"`
	Message string           `json:"message,omitempty"`
}

type uploadJSON struct {
	List []string `json:"list"`
}

func New(runtime config.Runtime, backend storage.Backend) *App {
	local := backend
	if backend.Type() != "local" {
		if localBackend, err := storage.NewLocal(runtime.LocalRoot); err == nil {
			local = localBackend
		} else {
			local = nil
		}
	}
	return &App{
		runtime:    runtime,
		backend:    backend,
		local:      local,
		s3Targets:  namedS3Targets(runtime.Config.S3),
		s3Backends: map[string]storage.Backend{},
		s3Factory: func(ctx context.Context, cfg config.S3Config) (storage.Backend, error) {
			return storage.NewS3(ctx, cfg)
		},
	}
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", a.handleUpload)
	mux.HandleFunc("/delete/", a.handleDelete)
	mux.HandleFunc("/files/", a.handleFiles)
	mux.HandleFunc("/list", a.handleList)
	return withCORS(mux)
}

func (a *App) UploadFiles(ctx context.Context, files []string, target string) ([]UploadResult, error) {
	var results []UploadResult
	for _, file := range files {
		result, err := a.uploadPath(ctx, a.backend, file, target, a.localBaseURL(""))
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body><h1>upimg /upload</h1><p>POST JSON {"list":["/path/a.png"]} or multipart/form-data files.</p></body></html>`)
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if !a.verifyKey(r) {
		writeUpload(w, UploadResponse{Success: false, Message: "server key is uncorrect"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	backend, err := a.uploadBackend(r)
	if err != nil {
		writeUpload(w, UploadResponse{Success: false, Message: err.Error()})
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("path"))

	var results []UploadResult
	if isMultipart(r.Header.Get("Content-Type")) {
		results, err = a.uploadMultipart(r, backend, target)
	} else {
		results, err = a.uploadJSON(r, backend, target)
	}
	if err != nil {
		writeUpload(w, UploadResponse{Success: false, Message: err.Error()})
		return
	}
	if len(results) == 0 {
		writeUpload(w, UploadResponse{Success: false, Message: "empty upload list is not supported"})
		return
	}

	urls := make([]string, 0, len(results))
	for _, result := range results {
		urls = append(urls, result.ImgURL)
	}
	if isRawUpload(r) {
		writeRawURLs(w, urls)
		return
	}
	writeUpload(w, UploadResponse{Success: true, Result: urls, FullResult: results})
}

func (a *App) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	if !a.verifyKey(r) {
		writeUpload(w, UploadResponse{Success: false, Message: "server key is uncorrect"})
		return
	}
	key, err := routePath(r.URL.Path, "/delete/")
	if err != nil {
		writeUploadStatus(w, http.StatusNotFound, UploadResponse{Success: false, Message: err.Error()})
		return
	}
	if err := a.backend.Delete(r.Context(), key); err != nil {
		writeUploadStatus(w, http.StatusNotFound, UploadResponse{Success: false, Message: "file not found"})
		return
	}
	writeUpload(w, UploadResponse{Success: true, Message: "deleted"})
}

func (a *App) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	key, err := routePath(r.URL.Path, "/files/")
	if err != nil {
		writeUploadStatus(w, http.StatusNotFound, UploadResponse{Success: false, Message: err.Error()})
		return
	}
	if local, ok := a.backend.(*storage.Local); ok {
		file, err := local.Open(key)
		if err != nil {
			writeUploadStatus(w, http.StatusNotFound, UploadResponse{Success: false, Message: "file not found"})
			return
		}
		defer file.Close()
		if contentType := mime.TypeByExtension(filepath.Ext(key)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		http.ServeContent(w, r, filepath.Base(key), time.Time{}, file)
		return
	}
	http.Redirect(w, r, a.backend.FileURL(key, ""), http.StatusFound)
}

func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	objects, err := a.backend.List(r.Context(), a.localBaseURL(baseURL(r)))
	if err != nil {
		writeList(w, ListResponse{Success: false, Message: err.Error()})
		return
	}
	writeList(w, ListResponse{Success: true, Result: objects})
}

func (a *App) uploadJSON(r *http.Request, backend storage.Backend, target string) ([]UploadResult, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	body = []byte(strings.TrimSpace(string(body)))
	payload := uploadJSON{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("Not sending data in JSON format")
		}
	}
	var results []UploadResult
	for _, file := range payload.List {
		result, err := a.uploadPath(r.Context(), backend, file, target, baseURL(r))
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (a *App) uploadMultipart(r *http.Request, backend storage.Backend, target string) ([]UploadResult, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, fmt.Errorf("Error processing formData")
	}
	var results []UploadResult
	for _, headers := range r.MultipartForm.File {
		for _, header := range headers {
			file, err := header.Open()
			if err != nil {
				return nil, fmt.Errorf("Error processing formData")
			}
			result, err := a.uploadReader(r.Context(), backend, sanitizeFileName(header.Filename), target, baseURL(r), file)
			_ = file.Close()
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
	}
	return results, nil
}

func (a *App) uploadPath(ctx context.Context, backend storage.Backend, source, target, baseURL string) (UploadResult, error) {
	file, err := os.Open(source)
	if err != nil {
		return UploadResult{}, err
	}
	defer file.Close()
	return a.uploadReader(ctx, backend, filepath.Base(source), target, baseURL, file)
}

func (a *App) uploadReader(ctx context.Context, backend storage.Backend, fileName, target, baseURL string, reader io.Reader) (UploadResult, error) {
	body, md5sum, cleanup, err := prepareUploadBody(reader)
	if err != nil {
		return UploadResult{}, err
	}
	defer cleanup()

	if target == "" {
		target = a.uploadDir(backend)
	}
	key, err := naming.ObjectKeyWithMD5(fileName, target, a.renameTemplate(), md5sum, time.Now())
	if err != nil {
		return UploadResult{}, err
	}
	stored, err := backend.Put(ctx, key, fileName, body)
	if err != nil {
		return UploadResult{}, err
	}
	if backend.Type() == "local" {
		if localBaseURL := a.localBaseURL(baseURL); localBaseURL != "" {
			stored.URL = backend.FileURL(key, localBaseURL)
		}
	}
	return UploadResult{FileName: stored.FileName, ImgURL: stored.URL, Type: stored.Type}, nil
}

func (a *App) uploadDir(backend storage.Backend) string {
	if s3Backend, ok := backend.(interface{ UploadPath() string }); ok {
		if uploadPath := strings.TrimSpace(s3Backend.UploadPath()); uploadPath != "" {
			return uploadPath
		}
	}
	return ""
}

func (a *App) renameTemplate() string {
	return a.runtime.Config.Rename
}

func (a *App) uploadBackend(r *http.Request) (storage.Backend, error) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		return a.backend, nil
	}
	if name == "local" {
		if a.local == nil {
			return nil, fmt.Errorf("local storage is not available")
		}
		return a.local, nil
	}
	cfg, ok := a.s3Targets[name]
	if !ok {
		return nil, fmt.Errorf("s3 config name %q not found", name)
	}
	if !cfg.Valid() {
		return nil, fmt.Errorf("s3 config name %q is invalid: missing %s", name, strings.Join(cfg.MissingFields(), ", "))
	}

	a.s3Mu.Lock()
	defer a.s3Mu.Unlock()
	if backend, ok := a.s3Backends[name]; ok {
		return backend, nil
	}
	backend, err := a.s3Factory(r.Context(), cfg)
	if err != nil {
		return nil, fmt.Errorf("create s3 config name %q: %w", name, err)
	}
	a.s3Backends[name] = backend
	return backend, nil
}

func namedS3Targets(items []config.S3Config) map[string]config.S3Config {
	targets := map[string]config.S3Config{}
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		targets[name] = item
	}
	return targets
}

func prepareUploadBody(reader io.Reader) (io.Reader, string, func(), error) {
	hasher := md5.New()
	if seeker, ok := reader.(io.ReadSeeker); ok {
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return nil, "", func() {}, err
		}
		if _, err := io.Copy(hasher, seeker); err != nil {
			return nil, "", func() {}, err
		}
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return nil, "", func() {}, err
		}
		return seeker, hex.EncodeToString(hasher.Sum(nil)), func() {}, nil
	}

	temp, err := os.CreateTemp("", "upimg-upload-*")
	if err != nil {
		return nil, "", func() {}, err
	}
	cleanup := func() {
		name := temp.Name()
		_ = temp.Close()
		_ = os.Remove(name)
	}
	if _, err := io.Copy(hasher, io.TeeReader(reader, temp)); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	return temp, hex.EncodeToString(hasher.Sum(nil)), cleanup, nil
}

func (a *App) localBaseURL(fallback string) string {
	if prefix := strings.TrimSpace(a.runtime.Config.URLPrefix); prefix != "" {
		return prefix
	}
	if fallback == "" {
		return ""
	}
	return strings.TrimRight(fallback, "/") + "/files"
}

func (a *App) verifyKey(r *http.Request) bool {
	if a.runtime.Key == "" {
		return true
	}
	return r.URL.Query().Get("key") == a.runtime.Key
}

func routePath(rawPath, prefix string) (string, error) {
	if !strings.HasPrefix(rawPath, prefix) {
		return "", fmt.Errorf("file not found")
	}
	return naming.SafeRelative(strings.TrimPrefix(rawPath, prefix))
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = strings.Split(forwarded, ",")[0]
	}
	return scheme + "://" + r.Host
}

func isMultipart(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data")
}

func isRawUpload(r *http.Request) bool {
	return r.URL.Query().Get("f") == "raw"
}

func sanitizeFileName(value string) string {
	value = filepath.Base(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimSpace(value)
	if value == "." || value == "/" || value == "" {
		return "upload"
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		default:
			return r
		}
	}, value)
}

func writeUpload(w http.ResponseWriter, response UploadResponse) {
	writeUploadStatus(w, http.StatusOK, response)
}

func writeUploadStatus(w http.ResponseWriter, status int, response UploadResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func writeRawURLs(w http.ResponseWriter, urls []string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, strings.Join(urls, "\n"))
}

func writeList(w http.ResponseWriter, response ListResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(response)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
