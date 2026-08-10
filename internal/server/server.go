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
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"upimg/internal/config"
	"upimg/internal/naming"
	"upimg/internal/storage"
	"upimg/internal/webui"
)

const maxUploadSize = 1024 * 1024 * 1024

type App struct {
	mu            sync.RWMutex
	runtime       config.Runtime
	backend       storage.Backend
	local         storage.Backend
	nameTargets   map[string][]namedTarget
	backendCache  map[string]storage.Backend
	s3Factory     func(context.Context, config.S3Config) (storage.Backend, error)
	webdavFactory func(config.WebDAVConfig) (storage.Backend, error)
	sftpFactory   func(config.SFTPConfig) (storage.Backend, error)
	cacheMu       sync.Mutex

	sessionMu  sync.Mutex
	sessions   map[string]time.Time
	sessionTTL time.Duration

	loginMu        sync.Mutex
	loginFailures  map[string]loginFailure
	previewLinkMu  sync.Mutex
	previewLinks   map[string]adminPreviewLink
	previewLinkTTL time.Duration
}

type UploadResult struct {
	FileName  string `json:"fileName"`
	ImgURL    string `json:"imgUrl"`
	Type      string `json:"type"`
	ObjectKey string `json:"objectKey"`
}

type CapabilitiesResponse struct {
	Success    bool     `json:"success"`
	APIVersion int      `json:"apiVersion"`
	Features   []string `json:"features"`
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

type namedTarget struct {
	ID     string
	Type   string
	S3     config.S3Config
	WebDAV config.WebDAVConfig
	SFTP   config.SFTPConfig
}

func New(runtime config.Runtime, backend storage.Backend) *App {
	app := &App{
		s3Factory: func(ctx context.Context, cfg config.S3Config) (storage.Backend, error) {
			return storage.NewS3(ctx, cfg)
		},
		webdavFactory: func(cfg config.WebDAVConfig) (storage.Backend, error) {
			return storage.NewWebDAV(cfg)
		},
		sftpFactory: func(cfg config.SFTPConfig) (storage.Backend, error) {
			return storage.NewSFTP(cfg)
		},
		sessions:       map[string]time.Time{},
		sessionTTL:     24 * time.Hour,
		loginFailures:  map[string]loginFailure{},
		previewLinks:   map[string]adminPreviewLink{},
		previewLinkTTL: 10 * time.Minute,
	}
	app.setRuntime(runtime, backend)
	return app
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, requestBasePath(r)+"/admin/", http.StatusPermanentRedirect)
	})
	adminHandler := webui.Handler("/admin/")
	if devURL := strings.TrimSpace(os.Getenv("UPIMG_WEB_DEV_URL")); devURL != "" {
		if proxy, err := webui.DevProxy(devURL); err == nil {
			adminHandler = proxy
		}
	}
	mux.Handle("/admin/", adminHandler)
	mux.HandleFunc("/api/admin/", a.handleAdminAPI)
	mux.HandleFunc("/office-preview/", a.handleOfficePreviewFile)
	mux.HandleFunc("/capabilities", a.handleCapabilities)
	mux.HandleFunc("/upload", a.handleUpload)
	mux.HandleFunc("/delete/", a.handleDelete)
	mux.HandleFunc("/files/", a.handleFiles)
	mux.HandleFunc("/list", a.handleList)
	return withCORS(a.withBasePath(mux))
}

func (a *App) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(CapabilitiesResponse{
		Success:    true,
		APIVersion: 2,
		Features:   []string{"upload-object-key", "delete-by-name"},
	})
}

func (a *App) setRuntime(runtime config.Runtime, backend storage.Backend) {
	local := backend
	if backend.Type() != "local" {
		if localBackend, err := storage.NewLocal(runtime.LocalRoot); err == nil {
			local = localBackend
		} else {
			local = nil
		}
	}

	a.mu.Lock()
	keyChanged := a.runtime.Key != runtime.Key
	defer a.mu.Unlock()
	a.runtime = runtime
	a.backend = backend
	a.local = local
	a.nameTargets = namedTargets(runtime.Config)
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	a.backendCache = map[string]storage.Backend{}
	if keyChanged {
		a.clearAdminSessions()
	}
	a.clearAdminPreviewLinks()
}

func (a *App) runtimeSnapshot() config.Runtime {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.runtime
}

func (a *App) currentBackend() storage.Backend {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.backend
}

func (a *App) UploadFiles(ctx context.Context, files []string, target string) ([]UploadResult, error) {
	var results []UploadResult
	backend := a.currentBackend()
	for _, file := range files {
		result, err := a.uploadPath(ctx, backend, file, target, a.localBaseURL(""))
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
	backend, err := a.uploadBackend(r)
	if err != nil {
		writeUploadStatus(w, http.StatusBadRequest, UploadResponse{Success: false, Message: err.Error()})
		return
	}
	if err := backend.Delete(r.Context(), key); err != nil {
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
	backend := a.currentBackend()
	if local, ok := backend.(*storage.Local); ok {
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
	http.Redirect(w, r, backend.FileURL(key, ""), http.StatusFound)
}

func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	backend := a.currentBackend()
	objects, err := backend.List(r.Context(), a.localBaseURL(baseURL(r)), r.URL.Query().Get("path"))
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
	return a.uploadMultipartWith(r, func(fileName string, reader io.Reader) (UploadResult, error) {
		return a.uploadReader(r.Context(), backend, fileName, target, baseURL(r), reader)
	})
}

func (a *App) uploadMultipartOriginalName(r *http.Request, backend storage.Backend, target string) ([]UploadResult, error) {
	return a.uploadMultipartWith(r, func(fileName string, reader io.Reader) (UploadResult, error) {
		return a.uploadReaderOriginalName(r.Context(), backend, fileName, target, baseURL(r), reader)
	})
}

func (a *App) uploadMultipartWith(r *http.Request, upload func(string, io.Reader) (UploadResult, error)) ([]UploadResult, error) {
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
			result, err := upload(sanitizeFileName(header.Filename), file)
			_ = file.Close()
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
	}
	return results, nil
}

func (a *App) uploadReaderOriginalName(ctx context.Context, backend storage.Backend, fileName, target, baseURL string, reader io.Reader) (UploadResult, error) {
	key, err := originalUploadKey(fileName, target)
	if err != nil {
		return UploadResult{}, err
	}
	stored, err := backend.Put(ctx, key, fileName, reader)
	if err != nil {
		return UploadResult{}, err
	}
	return a.uploadResult(backend, key, baseURL, stored), nil
}

func originalUploadKey(fileName, target string) (string, error) {
	fileName, err := naming.SafeRelative(sanitizeFileName(fileName))
	if err != nil {
		return "", err
	}

	target = strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	if strings.Trim(target, "/") == "" {
		return fileName, nil
	}
	target, err = naming.SafeRelative(target)
	if err != nil {
		return "", err
	}
	return path.Join(target, fileName), nil
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
	return a.uploadResult(backend, key, baseURL, stored), nil
}

func (a *App) uploadResult(backend storage.Backend, key, baseURL string, stored storage.StoredObject) UploadResult {
	if backend.Type() == "local" {
		if localBaseURL := a.localBaseURL(baseURL); localBaseURL != "" {
			stored.URL = backend.FileURL(key, localBaseURL)
		}
	}
	return UploadResult{FileName: stored.FileName, ImgURL: stored.URL, Type: stored.Type, ObjectKey: key}
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
	runtime := a.runtimeSnapshot()
	return runtime.Config.Rename
}

func (a *App) uploadBackend(r *http.Request) (storage.Backend, error) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		return a.currentBackend(), nil
	}
	if name == "local" {
		a.mu.RLock()
		local := a.local
		a.mu.RUnlock()
		if local == nil {
			return nil, fmt.Errorf("local storage is not available")
		}
		return local, nil
	}
	a.mu.RLock()
	matches, ok := a.nameTargets[name]
	a.mu.RUnlock()
	if !ok || len(matches) == 0 {
		return nil, fmt.Errorf("storage config name %q not found", name)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("storage config name %q is ambiguous", name)
	}
	target := matches[0]
	return a.backendByTarget(r.Context(), target.ID)
}

func namedTargets(cfg config.Config) map[string][]namedTarget {
	targets := map[string][]namedTarget{}
	for i, item := range cfg.S3 {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		targets[name] = append(targets[name], namedTarget{
			ID:   fmt.Sprintf("s3:%d", i),
			Type: "aws-s3",
			S3:   item,
		})
	}
	for i, item := range cfg.WebDAV {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		targets[name] = append(targets[name], namedTarget{
			ID:     fmt.Sprintf("webdav:%d", i),
			Type:   "webdav",
			WebDAV: item,
		})
	}
	for i, item := range cfg.SFTP {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		targets[name] = append(targets[name], namedTarget{
			ID:   fmt.Sprintf("sftp:%d", i),
			Type: "sftp",
			SFTP: item,
		})
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
	runtime := a.runtimeSnapshot()
	if prefix := strings.TrimSpace(runtime.Config.URLPrefix); prefix != "" {
		return prefix
	}
	if fallback == "" {
		return ""
	}
	return strings.TrimRight(fallback, "/") + "/files"
}

func (a *App) verifyKey(r *http.Request) bool {
	runtime := a.runtimeSnapshot()
	if runtime.Key == "" {
		return true
	}
	return r.URL.Query().Get("key") == runtime.Key
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
		scheme = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	prefix := strings.TrimRight(requestBasePath(r), "/")
	return scheme + "://" + host + prefix
}

func (a *App) withBasePath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runtime := a.runtimeSnapshot()
		basePath := runtime.BasePath
		forwardedBasePath := forwardedBasePath(r)
		if basePath == "" {
			next.ServeHTTP(w, withRequestBasePath(r, forwardedBasePath))
			return
		}
		if r.URL.Path == basePath {
			http.Redirect(w, r, basePath+"/", http.StatusPermanentRedirect)
			return
		}
		if !strings.HasPrefix(r.URL.Path, basePath+"/") {
			http.NotFound(w, r)
			return
		}

		cloned := r.Clone(r.Context())
		cloned.URL.Path = strings.TrimPrefix(r.URL.Path, basePath)
		if cloned.URL.Path == "" {
			cloned.URL.Path = "/"
		}
		if r.URL.RawPath != "" {
			cloned.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, basePath)
		}
		next.ServeHTTP(w, withRequestBasePath(cloned, basePath))
	})
}

func forwardedBasePath(r *http.Request) string {
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Prefix"))
	if forwarded == "" {
		return ""
	}
	basePath, err := config.NormalizeBasePath(strings.Split(forwarded, ",")[0])
	if err != nil {
		return ""
	}
	return basePath
}

func withRequestBasePath(r *http.Request, basePath string) *http.Request {
	cloned := r.Clone(r.Context())
	cloned.Header = r.Header.Clone()
	cloned.Header.Set("X-Upimg-Base-Path", basePath)
	return cloned
}

func requestBasePath(r *http.Request) string {
	basePath, err := config.NormalizeBasePath(r.Header.Get("X-Upimg-Base-Path"))
	if err != nil {
		return ""
	}
	return basePath
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
