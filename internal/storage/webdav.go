package storage

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"upimg/internal/config"
	"upimg/internal/naming"
)

type WebDAV struct {
	cfg      config.WebDAVConfig
	endpoint *url.URL
	client   *http.Client
	rootPath string
}

func NewWebDAV(cfg config.WebDAVConfig) (*WebDAV, error) {
	if !cfg.Valid() {
		return nil, fmt.Errorf("webdav config is incomplete")
	}
	endpoint, err := url.Parse(strings.TrimSpace(cfg.Endpoint))
	if err != nil {
		return nil, err
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("webdav endpoint must use http or https")
	}
	if endpoint.Host == "" {
		return nil, fmt.Errorf("webdav endpoint host is empty")
	}
	rootPath, err := cleanOptionalPath(cfg.RootPath)
	if err != nil {
		return nil, fmt.Errorf("invalid webdav rootPath: %w", err)
	}
	return &WebDAV{
		cfg:      cfg,
		endpoint: endpoint,
		client:   http.DefaultClient,
		rootPath: rootPath,
	}, nil
}

func (w *WebDAV) Type() string {
	return "webdav"
}

func (w *WebDAV) UploadPath() string {
	return w.cfg.UploadPath
}

func (w *WebDAV) Probe(ctx context.Context) error {
	req, err := w.newRequest(ctx, "PROPFIND", "", true, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Depth", "0")
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("webdav probe failed: %s", resp.Status)
}

func (w *WebDAV) Put(ctx context.Context, key, fileName string, body io.Reader) (StoredObject, error) {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return StoredObject{}, err
	}
	if err := w.ensureParentCollections(ctx, key); err != nil {
		return StoredObject{}, err
	}
	req, err := w.newRequest(ctx, "PUT", w.remoteObjectPath(key), false, body)
	if err != nil {
		return StoredObject{}, err
	}
	if contentType := imageContentType(fileName); contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return StoredObject{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return StoredObject{}, fmt.Errorf("webdav put failed: %s", resp.Status)
	}
	return StoredObject{FileName: fileName, URL: w.FileURL(key, ""), Type: w.Type()}, nil
}

func (w *WebDAV) Delete(ctx context.Context, key string) error {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return err
	}
	req, err := w.newRequest(ctx, "DELETE", w.remoteObjectPath(key), false, nil)
	if err != nil {
		return err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webdav delete failed: %s", resp.Status)
	}
	return nil
}

func (w *WebDAV) CreateDir(ctx context.Context, key string) error {
	key, err := normalizeDirectoryKey(key)
	if err != nil {
		return err
	}
	remotePath := w.remoteObjectPath(key)
	parent := path.Dir(remotePath)
	if parent != "." && parent != "/" && parent != "" {
		if err := w.ensureCollectionPath(ctx, parent); err != nil {
			return err
		}
	}
	req, err := w.newRequest(ctx, "MKCOL", remotePath, true, nil)
	if err != nil {
		return err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webdav create collection %q failed: %s", remotePath, resp.Status)
	}
	return nil
}

func (w *WebDAV) DeleteDir(ctx context.Context, key string) error {
	key, err := normalizeDirectoryKey(key)
	if err != nil {
		return err
	}
	req, err := w.newRequest(ctx, "DELETE", w.remoteObjectPath(key), true, nil)
	if err != nil {
		return err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webdav delete collection failed: %s", resp.Status)
	}
	return nil
}

func (w *WebDAV) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return nil, err
	}
	req, err := w.newRequest(ctx, "GET", w.remoteObjectPath(key), false, nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("webdav get failed: %s", resp.Status)
	}
	return resp.Body, nil
}

func (w *WebDAV) FileURL(key, _ string) string {
	key = strings.TrimLeft(strings.ReplaceAll(key, "\\", "/"), "/")
	objectPath := key
	if w.rootPath != "" {
		objectPath = path.Join(w.rootPath, key)
	}
	if prefix := strings.TrimSpace(w.cfg.URLPrefix); prefix != "" {
		return strings.TrimRight(prefix, "/") + "/" + objectPath
	}
	return w.remoteURL(objectPath, false)
}

func (w *WebDAV) List(ctx context.Context, _ string, dir string) ([]Object, error) {
	dir, err := normalizeListDir(dir)
	if err != nil {
		return nil, err
	}
	remoteDir := w.remoteObjectPath(dir)
	req, err := w.newRequest(ctx, "PROPFIND", remoteDir, true, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return []Object{}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("webdav list failed: %s", resp.Status)
	}

	var multi davMultiStatus
	if err := xml.NewDecoder(resp.Body).Decode(&multi); err != nil {
		return nil, err
	}

	var objects []Object
	for _, response := range multi.Responses {
		key, ok := w.keyFromHref(response.Href)
		if !ok || key == "" {
			continue
		}
		if dir != "" && key == strings.TrimRight(dir, "/") {
			continue
		}
		prop := response.successProp()
		modTime := parseDAVTime(prop.GetLastModified)
		object := Object{
			Path:    key,
			Size:    parseDAVSize(prop.GetContentLength),
			ModTime: modTime,
			Type:    w.Type(),
			IsDir:   prop.ResourceType.Collection,
		}
		if object.ModTime.IsZero() {
			object.ModTime = time.Unix(0, 0).UTC()
		}
		if object.IsDir {
			object.Path = strings.TrimRight(object.Path, "/") + "/"
			object.Size = 0
		} else {
			object.URL = w.FileURL(object.Path, "")
		}
		objects = append(objects, object)
	}
	sortObjects(objects)
	return objects, nil
}

func (w *WebDAV) ensureParentCollections(ctx context.Context, key string) error {
	remoteDir := path.Dir(w.remoteObjectPath(key))
	if remoteDir == "." || remoteDir == "/" || remoteDir == "" {
		return nil
	}
	return w.ensureCollectionPath(ctx, remoteDir)
}

func (w *WebDAV) ensureCollectionPath(ctx context.Context, remoteDir string) error {
	var current string
	for _, part := range strings.Split(remoteDir, "/") {
		if part == "" {
			continue
		}
		current = path.Join(current, part)
		req, err := w.newRequest(ctx, "MKCOL", current, true, nil)
		if err != nil {
			return err
		}
		resp, err := w.client.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusMethodNotAllowed {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("webdav create collection %q failed: %s", current, resp.Status)
		}
	}
	return nil
}

func (w *WebDAV) newRequest(ctx context.Context, method, remotePath string, collection bool, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, w.remoteURL(remotePath, collection), body)
	if err != nil {
		return nil, err
	}
	if w.cfg.Username != "" || w.cfg.Password != "" {
		req.SetBasicAuth(w.cfg.Username, w.cfg.Password)
	}
	return req, nil
}

func (w *WebDAV) remoteObjectPath(key string) string {
	key = strings.Trim(strings.ReplaceAll(key, "\\", "/"), "/")
	if w.rootPath == "" {
		return key
	}
	if key == "" {
		return w.rootPath
	}
	return path.Join(w.rootPath, key)
}

func (w *WebDAV) remoteURL(remotePath string, collection bool) string {
	u := *w.endpoint
	joined := joinURLPath(u.Path, remotePath)
	if collection && joined != "/" {
		joined = strings.TrimRight(joined, "/") + "/"
	}
	u.Path = joined
	u.RawPath = ""
	return u.String()
}

func (w *WebDAV) keyFromHref(href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", false
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	hrefPath := parsed.EscapedPath()
	if hrefPath == "" {
		hrefPath = parsed.Path
	}
	unescaped, err := url.PathUnescape(hrefPath)
	if err != nil {
		return "", false
	}
	base := joinURLPath(w.endpoint.Path, w.rootPath)
	normalized := strings.TrimRight(unescaped, "/")
	base = strings.TrimRight(base, "/")
	if normalized != base && !strings.HasPrefix(normalized, base+"/") {
		return "", false
	}
	rel := strings.TrimPrefix(normalized, base)
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return "", true
	}
	key, err := naming.SafeRelative(rel)
	if err != nil {
		return "", false
	}
	return key, true
}

func cleanOptionalPath(value string) (string, error) {
	value = strings.Trim(strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")), "/")
	if value == "" {
		return "", nil
	}
	return naming.SafeRelative(value)
}

func joinURLPath(basePath, remotePath string) string {
	basePath = strings.TrimRight(basePath, "/")
	remotePath = strings.Trim(remotePath, "/")
	switch {
	case basePath == "" && remotePath == "":
		return "/"
	case basePath == "":
		return "/" + remotePath
	case remotePath == "":
		return basePath
	default:
		return basePath + "/" + remotePath
	}
}

type davMultiStatus struct {
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href     string        `xml:"href"`
	PropStat []davPropStat `xml:"propstat"`
}

type davPropStat struct {
	Status string  `xml:"status"`
	Prop   davProp `xml:"prop"`
}

type davProp struct {
	GetContentLength string          `xml:"getcontentlength"`
	GetLastModified  string          `xml:"getlastmodified"`
	ResourceType     davResourceType `xml:"resourcetype"`
}

type davResourceType struct {
	Collection bool `xml:"collection"`
}

func (r *davResourceType) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch item := token.(type) {
		case xml.StartElement:
			if item.Name.Local == "collection" {
				r.Collection = true
			}
		case xml.EndElement:
			if item.Name == start.Name {
				return nil
			}
		}
	}
}

func (r davResponse) successProp() davProp {
	for _, propStat := range r.PropStat {
		if strings.Contains(propStat.Status, " 2") {
			return propStat.Prop
		}
	}
	if len(r.PropStat) > 0 {
		return r.PropStat[0].Prop
	}
	return davProp{}
}

func parseDAVSize(value string) int64 {
	size, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || size < 0 {
		return 0
	}
	return size
}

func parseDAVTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return parsed
	}
	return time.Time{}
}
