package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"upimg/internal/config"
	"upimg/internal/naming"
	"upimg/internal/storage"
)

const (
	adminSessionCookie  = "upimg_admin_session"
	maxAdminPreviewSize = 2 << 20
	maxLoginFailures    = 5
	loginFailureWindow  = time.Minute
	loginLockout        = time.Minute
)

type loginFailure struct {
	Count       int
	FirstAt     time.Time
	LockedUntil time.Time
}

type adminLoginRequest struct {
	Key string `json:"key"`
}

type adminSessionResponse struct {
	Success       bool              `json:"success"`
	Authenticated bool              `json:"authenticated"`
	KeyConfigured bool              `json:"keyConfigured"`
	Runtime       *adminRuntimeInfo `json:"runtime,omitempty"`
	Message       string            `json:"message,omitempty"`
}

type adminConfigResponse struct {
	Success bool             `json:"success"`
	Config  config.Config    `json:"config"`
	Runtime adminRuntimeInfo `json:"runtime"`
	Targets []adminTarget    `json:"targets"`
	Message string           `json:"message,omitempty"`
}

type adminListResponse struct {
	Success bool             `json:"success"`
	Result  []storage.Object `json:"result,omitempty"`
	Message string           `json:"message,omitempty"`
}

type adminRuntimeInfo struct {
	ConfigPath         string `json:"configPath"`
	LocalRoot          string `json:"localRoot"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	BasePath           string `json:"basePath"`
	CurrentBackend     string `json:"currentBackend"`
	KeyOverridden      bool   `json:"keyOverridden"`
	PortOverridden     bool   `json:"portOverridden"`
	BasePathOverridden bool   `json:"basePathOverridden"`
	FilePathOverridden bool   `json:"filePathOverridden"`
	RestartRequiredFor string `json:"restartRequiredFor,omitempty"`
}

type adminTarget struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Selected bool     `json:"selected"`
	Valid    bool     `json:"valid"`
	Missing  []string `json:"missing,omitempty"`
	Invalid  []string `json:"invalid,omitempty"`
}

type adminGenericResponse struct {
	Success            bool   `json:"success"`
	Message            string `json:"message,omitempty"`
	HostKeyFingerprint string `json:"hostKeyFingerprint,omitempty"`
	HostKeyCaptured    bool   `json:"hostKeyCaptured,omitempty"`
	HostKeyChanged     bool   `json:"hostKeyChanged,omitempty"`
}

type adminBatchDeleteRequest struct {
	Paths []string `json:"paths"`
}

type adminCreateFolderRequest struct {
	Path string `json:"path"`
}

type adminRenameRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type adminExtractRequest struct {
	Path      string `json:"path"`
	Overwrite bool   `json:"overwrite"`
}

type adminTerminalClientMessage struct {
	Type    string `json:"type"`
	Path    string `json:"path,omitempty"`
	Command string `json:"command,omitempty"`
	Data    string `json:"data,omitempty"`
}

type adminTerminalServerMessage struct {
	Type        string `json:"type"`
	Data        string `json:"data,omitempty"`
	Message     string `json:"message,omitempty"`
	ActualPath  string `json:"actualPath,omitempty"`
	ExitCode    int    `json:"exitCode,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`
}

type adminBatchDeleteResponse struct {
	Success bool     `json:"success"`
	Deleted []string `json:"deleted,omitempty"`
	Message string   `json:"message,omitempty"`
}

type adminRenameResponse struct {
	Success bool   `json:"success"`
	Partial bool   `json:"partial,omitempty"`
	Path    string `json:"path,omitempty"`
	URL     string `json:"url,omitempty"`
	Message string `json:"message,omitempty"`
}

func (a *App) handleAdminAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/admin/login":
		a.handleAdminLogin(w, r)
	case "/api/admin/logout":
		a.handleAdminLogout(w, r)
	case "/api/admin/session":
		a.handleAdminSession(w, r)
	case "/api/admin/config":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminConfig(w, r)
	case "/api/admin/storage/list":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStorageList(w, r)
	case "/api/admin/storage/upload":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStorageUpload(w, r)
	case "/api/admin/storage/folder":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStorageFolder(w, r)
	case "/api/admin/storage/object":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStorageObject(w, r)
	case "/api/admin/storage/object/rename":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStorageObjectRename(w, r)
	case "/api/admin/storage/object/extract":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStorageObjectExtract(w, r)
	case "/api/admin/storage/objects":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStorageObjects(w, r)
	case "/api/admin/storage/preview":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStoragePreview(w, r)
	case "/api/admin/storage/download":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStorageDownload(w, r)
	case "/api/admin/s3/test":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminS3Test(w, r)
	case "/api/admin/webdav/test":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminWebDAVTest(w, r)
	case "/api/admin/sftp/test":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminSFTPTest(w, r)
	case "/api/admin/sftp/terminal":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminSFTPTerminal(w, r)
	default:
		writeAdminStatus(w, http.StatusNotFound, adminGenericResponse{Success: false, Message: "api not found"})
	}
}

func (a *App) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}

	var payload adminLoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "invalid json"})
		return
	}

	runtime := a.runtimeSnapshot()
	client := loginClientKey(r)
	if runtime.Key != "" {
		if retryAfter := a.loginRetryAfter(client); retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			writeAdminStatus(w, http.StatusTooManyRequests, adminGenericResponse{Success: false, Message: "too many login attempts"})
			return
		}
	}
	if runtime.Key != "" && payload.Key != runtime.Key {
		a.recordLoginFailure(client)
		writeAdminStatus(w, http.StatusUnauthorized, adminGenericResponse{Success: false, Message: "invalid key"})
		return
	}
	a.clearLoginFailure(client)

	token, err := randomToken()
	if err != nil {
		writeAdminStatus(w, http.StatusInternalServerError, adminGenericResponse{Success: false, Message: "create session failed"})
		return
	}
	expires := time.Now().Add(a.sessionTTL)
	a.sessionMu.Lock()
	a.sessions[token] = expires
	a.sessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isHTTPS(r),
		Expires:  expires,
		MaxAge:   int(a.sessionTTL.Seconds()),
	})
	writeAdmin(w, adminGenericResponse{Success: true, Message: "logged in"})
}

func (a *App) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	if cookie, err := r.Cookie(adminSessionCookie); err == nil {
		a.sessionMu.Lock()
		delete(a.sessions, cookie.Value)
		a.sessionMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: adminSessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeAdmin(w, adminGenericResponse{Success: true, Message: "logged out"})
}

func (a *App) handleAdminSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	runtime := a.runtimeSnapshot()
	authenticated := a.adminAuthenticated(r)
	response := adminSessionResponse{
		Success:       true,
		Authenticated: authenticated,
		KeyConfigured: runtime.Key != "",
	}
	if authenticated {
		info := a.adminRuntimeInfo(runtime)
		response.Runtime = &info
	}
	writeAdmin(w, response)
}

func (a *App) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		runtime := a.runtimeSnapshot()
		writeAdmin(w, adminConfigResponse{
			Success: true,
			Config:  runtime.Config,
			Runtime: a.adminRuntimeInfo(runtime),
			Targets: a.adminTargets(runtime),
		})
	case http.MethodPut:
		var cfg config.Config
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&cfg); err != nil {
			writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "invalid json"})
			return
		}
		cfg = normalizeAdminConfigForWrite(cfg)
		if err := validateAdminConfig(cfg); err != nil {
			writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
			return
		}

		current := a.runtimeSnapshot()
		path, err := config.WritablePath(current.ConfigPath)
		if err != nil {
			writeAdminStatus(w, http.StatusInternalServerError, adminGenericResponse{Success: false, Message: err.Error()})
			return
		}
		runtime, err := config.RuntimeFromConfig(cfg, path)
		if err != nil {
			writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
			return
		}
		backend, err := a.buildRuntimeBackend(r.Context(), runtime)
		if err != nil {
			writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
			return
		}
		if err := config.Write(path, cfg); err != nil {
			writeAdminStatus(w, http.StatusInternalServerError, adminGenericResponse{Success: false, Message: err.Error()})
			return
		}
		a.setRuntime(runtime, backend)
		writeAdmin(w, adminConfigResponse{
			Success: true,
			Config:  runtime.Config,
			Runtime: a.adminRuntimeInfo(runtime),
			Targets: a.adminTargets(runtime),
			Message: "config saved",
		})
	default:
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
	}
}

func (a *App) handleAdminStorageList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	backend, err := a.adminBackend(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminListResponse{Success: false, Message: err.Error()})
		return
	}
	objects, err := backend.List(r.Context(), a.localBaseURL(baseURL(r)), r.URL.Query().Get("path"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminListResponse{Success: false, Message: err.Error()})
		return
	}
	writeAdmin(w, adminListResponse{Success: true, Result: objects})
}

func (a *App) handleAdminStorageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	if !isMultipart(r.Header.Get("Content-Type")) {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "multipart/form-data is required"})
		return
	}
	backend, err := a.adminBackend(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	results, err := a.uploadMultipartOriginalName(r, backend, strings.TrimSpace(r.URL.Query().Get("path")))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, UploadResponse{Success: false, Message: err.Error()})
		return
	}
	urls := make([]string, 0, len(results))
	for _, result := range results {
		urls = append(urls, result.ImgURL)
	}
	writeAdmin(w, UploadResponse{Success: true, Result: urls, FullResult: results})
}

func (a *App) handleAdminStorageFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	var payload adminCreateFolderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "invalid json"})
		return
	}
	backend, err := a.adminBackend(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	creator, ok := backend.(storage.DirectoryCreator)
	if !ok {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "folder creation is not supported"})
		return
	}
	if err := creator.CreateDir(r.Context(), payload.Path); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	writeAdmin(w, adminGenericResponse{Success: true, Message: "folder created"})
}

func (a *App) handleAdminStorageObject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	backend, err := a.adminBackend(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	key, status, err := deleteStoragePath(r.Context(), backend, r.URL.Query().Get("path"))
	if err != nil {
		writeAdminStatus(w, status, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	writeAdmin(w, adminGenericResponse{Success: true, Message: "deleted: " + key})
}

func (a *App) handleAdminStorageObjectRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminRenameResponse{Success: false, Message: "method not allowed"})
		return
	}
	var payload adminRenameRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminRenameResponse{Success: false, Message: "invalid json"})
		return
	}
	sourceKey, destinationKey, err := renameDestination(payload.Path, payload.Name)
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminRenameResponse{Success: false, Message: err.Error()})
		return
	}
	backend, err := a.adminBackend(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminRenameResponse{Success: false, Message: err.Error()})
		return
	}
	renamer, ok := backend.(storage.FileRenamer)
	if !ok {
		writeAdminStatus(w, http.StatusBadRequest, adminRenameResponse{Success: false, Message: "file rename is not supported"})
		return
	}
	if err := renamer.Rename(r.Context(), sourceKey, destinationKey); err != nil {
		var partial *storage.PartialRenameError
		if errors.As(err, &partial) {
			writeAdminStatus(w, http.StatusConflict, adminRenameResponse{
				Success: false,
				Partial: true,
				Path:    destinationKey,
				URL:     backend.FileURL(destinationKey, a.localBaseURL(baseURL(r))),
				Message: partial.Error(),
			})
			return
		}
		writeAdminStatus(w, http.StatusBadRequest, adminRenameResponse{Success: false, Message: err.Error()})
		return
	}
	writeAdmin(w, adminRenameResponse{
		Success: true,
		Path:    destinationKey,
		URL:     backend.FileURL(destinationKey, a.localBaseURL(baseURL(r))),
		Message: "renamed",
	})
}

func (a *App) handleAdminStorageObjectExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	var payload adminExtractRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "invalid json"})
		return
	}
	target := r.URL.Query().Get("target")
	if !strings.HasPrefix(strings.TrimSpace(target), "sftp:") {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "archive extraction is only supported for sftp targets"})
		return
	}
	key, err := naming.SafeRelative(payload.Path)
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	if isDirectoryDeletePath(payload.Path) {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "directory extraction is not supported"})
		return
	}
	if !isSupportedArchivePath(key) {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "unsupported archive format"})
		return
	}
	backend, err := a.adminBackend(r.Context(), target)
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	extractor, ok := backend.(storage.ArchiveExtractor)
	if !ok {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "archive extraction is not supported"})
		return
	}
	if err := extractor.ExtractArchive(r.Context(), key, payload.Overwrite); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	writeAdmin(w, adminGenericResponse{Success: true, Message: "解压完成"})
}

func (a *App) handleAdminStorageObjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	var payload adminBatchDeleteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "invalid json"})
		return
	}
	if len(payload.Paths) == 0 {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "paths is required"})
		return
	}
	backend, err := a.adminBackend(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}

	deleted := make([]string, 0, len(payload.Paths))
	for _, raw := range payload.Paths {
		key, status, err := deleteStoragePath(r.Context(), backend, raw)
		if err != nil {
			writeAdminStatus(w, status, adminBatchDeleteResponse{Success: false, Deleted: deleted, Message: err.Error()})
			return
		}
		deleted = append(deleted, key)
	}
	writeAdmin(w, adminBatchDeleteResponse{Success: true, Deleted: deleted, Message: "deleted"})
}

func (a *App) handleAdminStoragePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	key, err := naming.SafeRelative(r.URL.Query().Get("path"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	backend, err := a.adminBackend(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	readerBackend, ok := backend.(interface {
		OpenReader(context.Context, string) (io.ReadCloser, error)
	})
	if !ok {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "preview is not supported"})
		return
	}
	reader, err := readerBackend.OpenReader(r.Context(), key)
	if err != nil {
		writeAdminStatus(w, http.StatusNotFound, adminGenericResponse{Success: false, Message: "file not found"})
		return
	}
	defer reader.Close()

	limited := io.LimitReader(reader, maxAdminPreviewSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	if len(data) > maxAdminPreviewSize {
		writeAdminStatus(w, http.StatusRequestEntityTooLarge, adminGenericResponse{Success: false, Message: "preview file is too large"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (a *App) handleAdminStorageDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	rawPath := r.URL.Query().Get("path")
	if isDirectoryDeletePath(rawPath) {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "directory download is not supported"})
		return
	}
	key, err := naming.SafeRelative(rawPath)
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	backend, err := a.adminBackend(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	readerBackend, ok := backend.(interface {
		OpenReader(context.Context, string) (io.ReadCloser, error)
	})
	if !ok {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "download is not supported"})
		return
	}
	reader, err := readerBackend.OpenReader(r.Context(), key)
	if err != nil {
		writeAdminStatus(w, http.StatusNotFound, adminGenericResponse{Success: false, Message: "file not found"})
		return
	}
	defer reader.Close()

	fileName := path.Base(key)
	if contentType := mime.TypeByExtension(path.Ext(fileName)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(fileName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func deleteStoragePath(ctx context.Context, backend storage.Backend, rawPath string) (string, int, error) {
	key, err := naming.SafeRelative(rawPath)
	if err != nil {
		return "", http.StatusBadRequest, err
	}
	if isDirectoryDeletePath(rawPath) {
		deleter, ok := backend.(storage.DirectoryDeleter)
		if !ok {
			return "", http.StatusBadRequest, fmt.Errorf("directory deletion is not supported")
		}
		if err := deleter.DeleteDir(ctx, key); err != nil {
			return key + "/", http.StatusNotFound, fmt.Errorf("directory not found: %s", key)
		}
		return key + "/", http.StatusOK, nil
	}
	if err := backend.Delete(ctx, key); err != nil {
		return key, http.StatusNotFound, fmt.Errorf("file not found: %s", key)
	}
	return key, http.StatusOK, nil
}

func renameDestination(rawPath, rawName string) (string, string, error) {
	if isDirectoryDeletePath(rawPath) {
		return "", "", fmt.Errorf("directory rename is not supported")
	}
	sourceKey, err := naming.SafeRelative(rawPath)
	if err != nil {
		return "", "", err
	}
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "", "", fmt.Errorf("file name is required")
	}
	if name == "." || name == ".." {
		return "", "", fmt.Errorf("file name is invalid")
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return "", "", fmt.Errorf("file name must not contain path separators")
	}
	dir := path.Dir(sourceKey)
	destinationKey := name
	if dir != "." && dir != "/" {
		destinationKey = path.Join(dir, name)
	}
	destinationKey, err = naming.SafeRelative(destinationKey)
	if err != nil {
		return "", "", err
	}
	if destinationKey == sourceKey {
		return "", "", fmt.Errorf("file name is unchanged")
	}
	return sourceKey, destinationKey, nil
}

func isDirectoryDeletePath(value string) bool {
	return strings.HasSuffix(strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")), "/")
}

func isSupportedArchivePath(value string) bool {
	lower := strings.ToLower(value)
	for _, suffix := range []string{".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".gz", ".bz2", ".xz"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func (a *App) handleAdminS3Test(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	var cfg config.S3Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&cfg); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "invalid json"})
		return
	}
	if !cfg.Valid() {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "missing " + strings.Join(cfg.MissingFields(), ", ")})
		return
	}
	backend, err := a.s3Factory(r.Context(), cfg)
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	if probe, ok := backend.(interface{ Probe(context.Context) error }); ok {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		err = probe.Probe(ctx)
		cancel()
		if err != nil {
			writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
			return
		}
	}
	writeAdmin(w, adminGenericResponse{Success: true, Message: "s3 reachable"})
}

func (a *App) handleAdminWebDAVTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	var cfg config.WebDAVConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&cfg); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "invalid json"})
		return
	}
	if !cfg.Valid() {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: webdavConfigError(cfg)})
		return
	}
	backend, err := a.webdavFactory(cfg)
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	if probe, ok := backend.(interface{ Probe(context.Context) error }); ok {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		err = probe.Probe(ctx)
		cancel()
		if err != nil {
			writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
			return
		}
	}
	writeAdmin(w, adminGenericResponse{Success: true, Message: "webdav reachable"})
}

func (a *App) handleAdminSFTPTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	var cfg config.SFTPConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&cfg); err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "invalid json"})
		return
	}
	if !cfg.Valid() {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: sftpConfigError(cfg)})
		return
	}
	backend, err := a.sftpFactory(cfg)
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	if probe, ok := backend.(interface{ Probe(context.Context) error }); ok {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		err = probe.Probe(ctx)
		cancel()
		if err != nil {
			var hostKeyErr *storage.SFTPHostKeyError
			if errors.As(err, &hostKeyErr) && hostKeyErr.Fingerprint != "" {
				if strings.TrimSpace(cfg.HostKeyFingerprint) == "" {
					writeAdmin(w, adminGenericResponse{
						Success:            true,
						Message:            "sftp host key fingerprint captured; accept it only if you trust the server identity",
						HostKeyFingerprint: hostKeyErr.Fingerprint,
						HostKeyCaptured:    true,
					})
					return
				}
				writeAdmin(w, adminGenericResponse{
					Success:            true,
					Message:            "sftp host key fingerprint changed; update it only if you trust the server identity",
					HostKeyFingerprint: hostKeyErr.Fingerprint,
					HostKeyChanged:     true,
				})
				return
			}
			writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
			return
		}
	}
	writeAdmin(w, adminGenericResponse{Success: true, Message: "sftp reachable"})
}

func (a *App) handleAdminSFTPTerminal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminStatus(w, http.StatusMethodNotAllowed, adminGenericResponse{Success: false, Message: "method not allowed"})
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	if !strings.HasPrefix(target, "sftp:") {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "terminal is only supported for sftp targets"})
		return
	}
	backend, err := a.adminBackend(r.Context(), target)
	if err != nil {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: err.Error()})
		return
	}
	opener, ok := backend.(storage.CommandSessionOpener)
	if !ok {
		writeAdminStatus(w, http.StatusBadRequest, adminGenericResponse{Success: false, Message: "terminal is not supported"})
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	connCtx, cancelConn := context.WithCancel(context.Background())
	defer cancelConn()
	sender := &adminTerminalSender{conn: conn, ctx: connCtx}

	var init adminTerminalClientMessage
	readCtx, cancelRead := context.WithTimeout(connCtx, 30*time.Second)
	err = wsjson.Read(readCtx, conn, &init)
	cancelRead()
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "init message is required")
		return
	}
	if init.Type != "init" {
		_ = sender.send(adminTerminalServerMessage{Type: "error", Message: "first message must be init"})
		_ = conn.Close(websocket.StatusPolicyViolation, "first message must be init")
		return
	}

	_ = sender.send(adminTerminalServerMessage{Type: "status", Message: "正在建立 SSH 连接..."})
	commandSession, err := opener.OpenCommandSession(
		connCtx,
		init.Path,
		adminTerminalWriter{kind: "stdout", sender: sender},
		adminTerminalWriter{kind: "stderr", sender: sender},
	)
	if err != nil {
		_ = sender.send(adminTerminalServerMessage{Type: "error", Message: err.Error()})
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer commandSession.Close()
	_ = sender.send(adminTerminalServerMessage{
		Type:       "ready",
		Message:    "SSH 已连接",
		ActualPath: commandSession.ActualDir(),
	})

	done := make(chan error, 1)
	go func() {
		done <- commandSession.Wait()
	}()

	readErr := make(chan error, 1)
	go func() {
		for {
			var msg adminTerminalClientMessage
			if err := wsjson.Read(connCtx, conn, &msg); err != nil {
				readErr <- err
				return
			}
			switch msg.Type {
			case "command":
				command := msg.Command
				if command == "" {
					command = msg.Data
				}
				if err := commandSession.WriteCommand(command); err != nil {
					_ = sender.send(adminTerminalServerMessage{Type: "error", Message: err.Error()})
				}
			case "interrupt":
				_ = sender.send(adminTerminalServerMessage{Type: "status", Message: "正在中断命令..."})
				if err := commandSession.Interrupt(); err != nil {
					_ = sender.send(adminTerminalServerMessage{Type: "error", Message: err.Error()})
				}
			case "close":
				readErr <- nil
				return
			}
		}
	}()

	select {
	case err := <-done:
		if err != nil {
			_ = sender.send(adminTerminalServerMessage{Type: "error", Message: err.Error()})
		}
		_ = sender.send(adminTerminalServerMessage{Type: "done"})
	case <-readErr:
		_ = commandSession.Close()
		_ = sender.send(adminTerminalServerMessage{Type: "done"})
	case <-connCtx.Done():
		_ = commandSession.Close()
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

type adminTerminalSender struct {
	conn *websocket.Conn
	ctx  context.Context
	mu   sync.Mutex
}

func (s *adminTerminalSender) send(message adminTerminalServerMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(ctx, s.conn, message)
}

type adminTerminalWriter struct {
	kind   string
	sender *adminTerminalSender
}

func (w adminTerminalWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if err := w.sender.send(adminTerminalServerMessage{Type: w.kind, Data: string(data)}); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (a *App) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if a.adminAuthenticated(r) {
		return true
	}
	writeAdminStatus(w, http.StatusUnauthorized, adminGenericResponse{Success: false, Message: "login required"})
	return false
}

func (a *App) adminAuthenticated(r *http.Request) bool {
	runtime := a.runtimeSnapshot()
	if runtime.Key == "" {
		return true
	}
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	now := time.Now()
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	expires, ok := a.sessions[cookie.Value]
	if !ok {
		return false
	}
	if now.After(expires) {
		delete(a.sessions, cookie.Value)
		return false
	}
	return true
}

func (a *App) clearAdminSessions() {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	a.sessions = map[string]time.Time{}
}

func (a *App) adminBackend(ctx context.Context, target string) (storage.Backend, error) {
	return a.backendByTarget(ctx, target)
}

func (a *App) backendByTarget(ctx context.Context, target string) (storage.Backend, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("target is required")
	}
	if target == "local" {
		a.mu.RLock()
		local := a.local
		a.mu.RUnlock()
		if local == nil {
			return nil, fmt.Errorf("local storage is not available")
		}
		return local, nil
	}
	runtime := a.runtimeSnapshot()

	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	if backend, ok := a.backendCache[target]; ok {
		return backend, nil
	}
	backend, err := a.backendForTargetConfig(ctx, runtime, target)
	if err != nil {
		return nil, err
	}
	a.backendCache[target] = backend
	return backend, nil
}

func (a *App) buildRuntimeBackend(ctx context.Context, runtime config.Runtime) (storage.Backend, error) {
	if target := strings.TrimSpace(runtime.Config.DefaultTarget); target != "" {
		backend, err := a.backendForTargetConfig(ctx, runtime, target)
		if err != nil {
			return nil, err
		}
		if err := probeBackend(ctx, backend); err != nil {
			return nil, err
		}
		return backend, nil
	}

	return storage.NewLocal(runtime.LocalRoot)
}

func (a *App) backendForTargetConfig(ctx context.Context, runtime config.Runtime, target string) (storage.Backend, error) {
	target = strings.TrimSpace(target)
	if target == "local" {
		return storage.NewLocal(runtime.LocalRoot)
	}
	if strings.HasPrefix(target, "s3:") {
		index, err := strconv.Atoi(strings.TrimPrefix(target, "s3:"))
		if err != nil || index < 0 {
			return nil, fmt.Errorf("invalid s3 target")
		}
		if index >= len(runtime.Config.S3) {
			return nil, fmt.Errorf("s3 target not found")
		}
		cfg := runtime.Config.S3[index]
		if !cfg.Valid() {
			return nil, fmt.Errorf("s3 target is invalid: missing %s", strings.Join(cfg.MissingFields(), ", "))
		}
		backend, err := a.s3Factory(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("create s3 target: %w", err)
		}
		return backend, nil
	}
	if strings.HasPrefix(target, "webdav:") {
		index, err := strconv.Atoi(strings.TrimPrefix(target, "webdav:"))
		if err != nil || index < 0 {
			return nil, fmt.Errorf("invalid webdav target")
		}
		if index >= len(runtime.Config.WebDAV) {
			return nil, fmt.Errorf("webdav target not found")
		}
		cfg := runtime.Config.WebDAV[index]
		if !cfg.Valid() {
			return nil, fmt.Errorf("webdav target is invalid: %s", webdavConfigError(cfg))
		}
		backend, err := a.webdavFactory(cfg)
		if err != nil {
			return nil, fmt.Errorf("create webdav target: %w", err)
		}
		return backend, nil
	}
	if strings.HasPrefix(target, "sftp:") {
		index, err := strconv.Atoi(strings.TrimPrefix(target, "sftp:"))
		if err != nil || index < 0 {
			return nil, fmt.Errorf("invalid sftp target")
		}
		if index >= len(runtime.Config.SFTP) {
			return nil, fmt.Errorf("sftp target not found")
		}
		cfg := runtime.Config.SFTP[index]
		if !cfg.Valid() {
			return nil, fmt.Errorf("sftp target is invalid: %s", sftpConfigError(cfg))
		}
		backend, err := a.sftpFactory(cfg)
		if err != nil {
			return nil, fmt.Errorf("create sftp target: %w", err)
		}
		return backend, nil
	}
	return nil, fmt.Errorf("unknown target %q", target)
}

func probeBackend(ctx context.Context, backend storage.Backend) error {
	if probe, ok := backend.(interface{ Probe(context.Context) error }); ok {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := probe.Probe(probeCtx)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *App) adminRuntimeInfo(runtime config.Runtime) adminRuntimeInfo {
	backend := a.currentBackend()
	backendType := ""
	if backend != nil {
		backendType = backend.Type()
	}

	restart := ""
	if strings.TrimSpace(os.Getenv("PORT")) == "" {
		if runtime.Config.Port != 0 && runtime.Config.Port != runtime.Port {
			restart = "port"
		}
	}

	return adminRuntimeInfo{
		ConfigPath:         runtime.ConfigPath,
		LocalRoot:          runtime.LocalRoot,
		Host:               runtime.Host,
		Port:               runtime.Port,
		BasePath:           runtime.BasePath,
		CurrentBackend:     backendType,
		KeyOverridden:      strings.TrimSpace(os.Getenv("KEY")) != "",
		PortOverridden:     strings.TrimSpace(os.Getenv("PORT")) != "",
		BasePathOverridden: strings.TrimSpace(os.Getenv("BASE_PATH")) != "",
		FilePathOverridden: strings.TrimSpace(os.Getenv("FILEPATH")) != "",
		RestartRequiredFor: restart,
	}
}

func (a *App) adminTargets(runtime config.Runtime) []adminTarget {
	defaultTarget := effectiveDefaultTarget(runtime.Config)
	targets := []adminTarget{{
		ID:       "local",
		Type:     "local",
		Name:     "Local",
		Selected: defaultTarget == "local",
		Valid:    true,
	}}
	for i, item := range runtime.Config.S3 {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = strings.TrimSpace(item.Bucket)
		}
		if name == "" {
			name = fmt.Sprintf("S3 %d", i+1)
		}
		targets = append(targets, adminTarget{
			ID:       fmt.Sprintf("s3:%d", i),
			Type:     "aws-s3",
			Name:     name,
			Selected: defaultTarget == fmt.Sprintf("s3:%d", i),
			Valid:    item.Valid(),
			Missing:  item.MissingFields(),
		})
	}
	for i, item := range runtime.Config.WebDAV {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = strings.TrimSpace(item.Endpoint)
		}
		if name == "" {
			name = fmt.Sprintf("WebDAV %d", i+1)
		}
		targets = append(targets, adminTarget{
			ID:       fmt.Sprintf("webdav:%d", i),
			Type:     "webdav",
			Name:     name,
			Selected: defaultTarget == fmt.Sprintf("webdav:%d", i),
			Valid:    item.Valid(),
			Missing:  item.MissingFields(),
			Invalid:  item.InvalidFields(),
		})
	}
	for i, item := range runtime.Config.SFTP {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = strings.TrimSpace(item.Host)
		}
		if name == "" {
			name = fmt.Sprintf("SFTP %d", i+1)
		}
		targets = append(targets, adminTarget{
			ID:       fmt.Sprintf("sftp:%d", i),
			Type:     "sftp",
			Name:     name,
			Selected: defaultTarget == fmt.Sprintf("sftp:%d", i),
			Valid:    item.Valid(),
			Missing:  item.MissingFields(),
			Invalid:  item.InvalidFields(),
		})
	}
	return targets
}

func validateAdminConfig(cfg config.Config) error {
	if cfg.Port < 0 || cfg.Port > 65535 {
		return fmt.Errorf("port must be 0 or between 1 and 65535")
	}
	if err := validateDefaultTarget(cfg); err != nil {
		return err
	}
	for i, item := range cfg.S3 {
		if !s3ConfigPresent(item) {
			continue
		}
		if !item.Valid() {
			return fmt.Errorf("s3[%d] missing %s", i, strings.Join(item.MissingFields(), ", "))
		}
	}
	for i, item := range cfg.WebDAV {
		if !webdavConfigPresent(item) {
			continue
		}
		if !item.Valid() {
			return fmt.Errorf("webdav[%d] %s", i, webdavConfigError(item))
		}
	}
	for i, item := range cfg.SFTP {
		if !sftpConfigPresent(item) {
			continue
		}
		if !item.Valid() {
			return fmt.Errorf("sftp[%d] %s", i, sftpConfigError(item))
		}
	}
	return nil
}

func normalizeAdminConfigForWrite(cfg config.Config) config.Config {
	if strings.TrimSpace(cfg.DefaultTarget) == "" {
		cfg.DefaultTarget = effectiveDefaultTarget(cfg)
	}
	return cfg
}

func validateDefaultTarget(cfg config.Config) error {
	target := strings.TrimSpace(cfg.DefaultTarget)
	if target == "" || target == "local" {
		return nil
	}
	if strings.HasPrefix(target, "s3:") {
		index, err := strconv.Atoi(strings.TrimPrefix(target, "s3:"))
		if err != nil || index < 0 || index >= len(cfg.S3) {
			return fmt.Errorf("defaultTarget points to missing s3 target")
		}
		return nil
	}
	if strings.HasPrefix(target, "webdav:") {
		index, err := strconv.Atoi(strings.TrimPrefix(target, "webdav:"))
		if err != nil || index < 0 || index >= len(cfg.WebDAV) {
			return fmt.Errorf("defaultTarget points to missing webdav target")
		}
		return nil
	}
	if strings.HasPrefix(target, "sftp:") {
		index, err := strconv.Atoi(strings.TrimPrefix(target, "sftp:"))
		if err != nil || index < 0 || index >= len(cfg.SFTP) {
			return fmt.Errorf("defaultTarget points to missing sftp target")
		}
		return nil
	}
	return fmt.Errorf("defaultTarget must be local, s3:<index>, webdav:<index>, or sftp:<index>")
}

func effectiveDefaultTarget(cfg config.Config) string {
	if target := strings.TrimSpace(cfg.DefaultTarget); target != "" {
		return target
	}
	return "local"
}

func s3ConfigPresent(cfg config.S3Config) bool {
	return strings.TrimSpace(cfg.Name) != "" ||
		strings.TrimSpace(cfg.Bucket) != "" ||
		strings.TrimSpace(cfg.Region) != "" ||
		strings.TrimSpace(cfg.AccessKeyID) != "" ||
		strings.TrimSpace(cfg.SecretAccessKey) != "" ||
		strings.TrimSpace(cfg.Endpoint) != "" ||
		strings.TrimSpace(cfg.URLPrefix) != "" ||
		strings.TrimSpace(cfg.UploadPath) != ""
}

func webdavConfigPresent(cfg config.WebDAVConfig) bool {
	return strings.TrimSpace(cfg.Name) != "" ||
		strings.TrimSpace(cfg.Endpoint) != "" ||
		strings.TrimSpace(cfg.Username) != "" ||
		strings.TrimSpace(cfg.Password) != "" ||
		strings.TrimSpace(cfg.RootPath) != "" ||
		strings.TrimSpace(cfg.URLPrefix) != "" ||
		strings.TrimSpace(cfg.UploadPath) != ""
}

func sftpConfigPresent(cfg config.SFTPConfig) bool {
	return strings.TrimSpace(cfg.Name) != "" ||
		strings.TrimSpace(cfg.Host) != "" ||
		cfg.Port != 0 ||
		strings.TrimSpace(cfg.Username) != "" ||
		strings.TrimSpace(cfg.Password) != "" ||
		strings.TrimSpace(cfg.PrivateKey) != "" ||
		strings.TrimSpace(cfg.Passphrase) != "" ||
		strings.TrimSpace(cfg.HostKeyFingerprint) != "" ||
		strings.TrimSpace(cfg.RootPath) != "" ||
		strings.TrimSpace(cfg.URLPrefix) != "" ||
		strings.TrimSpace(cfg.UploadPath) != ""
}

func webdavConfigError(cfg config.WebDAVConfig) string {
	var parts []string
	if missing := cfg.MissingFields(); len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(missing, ", "))
	}
	if invalid := cfg.InvalidFields(); len(invalid) > 0 {
		parts = append(parts, "invalid "+strings.Join(invalid, ", "))
	}
	return strings.Join(parts, "; ")
}

func sftpConfigError(cfg config.SFTPConfig) string {
	var parts []string
	if missing := cfg.MissingFields(); len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(missing, ", "))
	}
	if invalid := cfg.InvalidFields(); len(invalid) > 0 {
		parts = append(parts, "invalid "+strings.Join(invalid, ", "))
	}
	return strings.Join(parts, "; ")
}

func (a *App) loginRetryAfter(client string) time.Duration {
	now := time.Now()
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	failure, ok := a.loginFailures[client]
	if !ok {
		return 0
	}
	if now.After(failure.LockedUntil) && now.Sub(failure.FirstAt) > loginFailureWindow {
		delete(a.loginFailures, client)
		return 0
	}
	if now.Before(failure.LockedUntil) {
		return time.Until(failure.LockedUntil).Round(time.Second)
	}
	return 0
}

func (a *App) recordLoginFailure(client string) {
	now := time.Now()
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	failure := a.loginFailures[client]
	if failure.FirstAt.IsZero() || now.Sub(failure.FirstAt) > loginFailureWindow {
		failure = loginFailure{FirstAt: now}
	}
	failure.Count++
	if failure.Count >= maxLoginFailures {
		failure.LockedUntil = now.Add(loginLockout)
	}
	a.loginFailures[client] = failure
}

func (a *App) clearLoginFailure(client string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	delete(a.loginFailures, client)
}

func loginClientKey(r *http.Request) string {
	forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if forwarded != "" {
		return forwarded
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0], "https")
}

func writeAdmin(w http.ResponseWriter, response any) {
	writeAdminStatus(w, http.StatusOK, response)
}

func writeAdminStatus(w http.ResponseWriter, status int, response any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
