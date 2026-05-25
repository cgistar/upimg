package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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
}

type adminGenericResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type adminBatchDeleteRequest struct {
	Paths []string `json:"paths"`
}

type adminBatchDeleteResponse struct {
	Success bool     `json:"success"`
	Deleted []string `json:"deleted,omitempty"`
	Message string   `json:"message,omitempty"`
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
	case "/api/admin/storage/object":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminStorageObject(w, r)
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
	case "/api/admin/s3/test":
		if !a.requireAdmin(w, r) {
			return
		}
		a.handleAdminS3Test(w, r)
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
	results, err := a.uploadMultipart(r, backend, strings.TrimSpace(r.URL.Query().Get("path")))
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

func (a *App) handleAdminStorageObject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
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
	if err := backend.Delete(r.Context(), key); err != nil {
		writeAdminStatus(w, http.StatusNotFound, adminGenericResponse{Success: false, Message: "file not found"})
		return
	}
	writeAdmin(w, adminGenericResponse{Success: true, Message: "deleted"})
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
		key, err := naming.SafeRelative(raw)
		if err != nil {
			writeAdminStatus(w, http.StatusBadRequest, adminBatchDeleteResponse{Success: false, Deleted: deleted, Message: err.Error()})
			return
		}
		if err := backend.Delete(r.Context(), key); err != nil {
			writeAdminStatus(w, http.StatusNotFound, adminBatchDeleteResponse{Success: false, Deleted: deleted, Message: "file not found: " + key})
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
	if !strings.HasPrefix(target, "s3:") {
		return nil, fmt.Errorf("unknown target %q", target)
	}
	index, err := strconv.Atoi(strings.TrimPrefix(target, "s3:"))
	if err != nil || index < 0 {
		return nil, fmt.Errorf("invalid s3 target")
	}
	runtime := a.runtimeSnapshot()
	if index >= len(runtime.Config.S3) {
		return nil, fmt.Errorf("s3 target not found")
	}
	cfg := runtime.Config.S3[index]
	if !cfg.Valid() {
		return nil, fmt.Errorf("s3 target is invalid: missing %s", strings.Join(cfg.MissingFields(), ", "))
	}

	cacheKey := fmt.Sprintf("s3:%d", index)
	a.s3Mu.Lock()
	defer a.s3Mu.Unlock()
	if backend, ok := a.s3Backends[cacheKey]; ok {
		return backend, nil
	}
	backend, err := a.s3Factory(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create s3 target: %w", err)
	}
	a.s3Backends[cacheKey] = backend
	return backend, nil
}

func (a *App) buildRuntimeBackend(ctx context.Context, runtime config.Runtime) (storage.Backend, error) {
	for _, selected := range runtime.Config.S3 {
		if !selected.Selected || !selected.Valid() {
			continue
		}
		backend, err := a.s3Factory(ctx, selected)
		if err != nil {
			continue
		}
		if probe, ok := backend.(interface{ Probe(context.Context) error }); ok {
			probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = probe.Probe(probeCtx)
			cancel()
			if err != nil {
				continue
			}
		}
		return backend, nil
	}
	return storage.NewLocal(runtime.LocalRoot)
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
	targets := []adminTarget{{
		ID:       "local",
		Type:     "local",
		Name:     "Local",
		Selected: false,
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
			Selected: item.Selected,
			Valid:    item.Valid(),
			Missing:  item.MissingFields(),
		})
	}
	return targets
}

func validateAdminConfig(cfg config.Config) error {
	if cfg.Port < 0 || cfg.Port > 65535 {
		return fmt.Errorf("port must be 0 or between 1 and 65535")
	}
	for i, item := range cfg.S3 {
		if !s3ConfigPresent(item) {
			continue
		}
		if !item.Valid() {
			return fmt.Errorf("s3[%d] missing %s", i, strings.Join(item.MissingFields(), ", "))
		}
	}
	return nil
}

func s3ConfigPresent(cfg config.S3Config) bool {
	return cfg.Selected ||
		strings.TrimSpace(cfg.Name) != "" ||
		strings.TrimSpace(cfg.Bucket) != "" ||
		strings.TrimSpace(cfg.Region) != "" ||
		strings.TrimSpace(cfg.AccessKeyID) != "" ||
		strings.TrimSpace(cfg.SecretAccessKey) != "" ||
		strings.TrimSpace(cfg.Endpoint) != "" ||
		strings.TrimSpace(cfg.URLPrefix) != "" ||
		strings.TrimSpace(cfg.UploadPath) != ""
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
