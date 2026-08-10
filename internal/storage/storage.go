package storage

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"upimg/internal/naming"
)

type Object struct {
	Path    string    `json:"path"`
	URL     string    `json:"url"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Type    string    `json:"type"`
	IsDir   bool      `json:"isDir"`
}

type StoredObject struct {
	FileName string
	URL      string
	Type     string
}

type Backend interface {
	Type() string
	Put(ctx context.Context, key, fileName string, body io.Reader) (StoredObject, error)
	Delete(ctx context.Context, key string) error
	FileURL(key, baseURL string) string
	List(ctx context.Context, baseURL, dir string) ([]Object, error)
}

// SizedReadCloser 在保持流式读取的同时暴露对象的可信长度。
// Size 返回负数表示上游没有提供长度。
type SizedReadCloser interface {
	io.ReadCloser
	Size() int64
}

type sizedReadCloser struct {
	io.ReadCloser
	size int64
}

func (r *sizedReadCloser) Size() int64 {
	return r.size
}

type DirectoryCreator interface {
	CreateDir(ctx context.Context, key string) error
}

type DirectoryDeleter interface {
	DeleteDir(ctx context.Context, key string) error
}

type FileRenamer interface {
	Rename(ctx context.Context, sourceKey, destinationKey string) error
}

type ArchiveExtractor interface {
	ExtractArchive(ctx context.Context, key string, overwrite bool) error
}

type CommandSession interface {
	ActualDir() string
	WriteCommand(command string) error
	WriteInput(input string) error
	Interrupt() error
	Close() error
	Wait() error
}

type CommandSessionOpener interface {
	OpenCommandSession(ctx context.Context, dir string, stdout, stderr io.Writer) (CommandSession, error)
}

type PartialRenameError struct {
	SourceKey      string
	DestinationKey string
	Cause          error
}

func (e *PartialRenameError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause == nil {
		return fmt.Sprintf("已复制到 %s，但删除源文件 %s 失败", e.DestinationKey, e.SourceKey)
	}
	return fmt.Sprintf("已复制到 %s，但删除源文件 %s 失败: %v", e.DestinationKey, e.SourceKey, e.Cause)
}

func (e *PartialRenameError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func normalizeListDir(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if strings.Trim(value, "/") == "" {
		return "", nil
	}
	return naming.SafeRelative(value)
}

func normalizeDirectoryKey(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if strings.Trim(value, "/") == "" {
		return "", fmt.Errorf("directory path is empty")
	}
	return naming.SafeRelative(value)
}

func sortObjects(objects []Object) {
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].IsDir != objects[j].IsDir {
			return objects[i].IsDir
		}
		return objects[i].Path < objects[j].Path
	})
}

// encodeURLPath escapes each path segment for use in an HTTP URL path,
// while preserving "/" separators. Empty input returns "".
//
// Encoding is stricter than url.PathEscape: only RFC 3986 unreserved
// characters (ALPHA / DIGIT / "-" / "." / "_" / "~") stay raw. This
// avoids reverse-proxy pitfalls such as treating "+" as a space.
func encodeURLPath(key string) string {
	key = strings.TrimLeft(strings.ReplaceAll(key, "\\", "/"), "/")
	if key == "" {
		return ""
	}
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = escapePathSegment(part)
	}
	return strings.Join(parts, "/")
}

func escapePathSegment(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isURLUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		// Percent-encode one byte at a time (UTF-8 multibyte chars are encoded per byte).
		b.WriteByte('%')
		b.WriteByte(upperHex(c >> 4))
		b.WriteByte(upperHex(c & 0xf))
	}
	return b.String()
}

func isURLUnreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '-' || c == '.' || c == '_' || c == '~':
		return true
	default:
		return false
	}
}

func upperHex(v byte) byte {
	const hex = "0123456789ABCDEF"
	return hex[v]
}

// joinObjectURL joins a base URL prefix with an object key, escaping the key path.
func joinObjectURL(prefix, key string) string {
	prefix = strings.TrimRight(prefix, "/")
	escaped := encodeURLPath(key)
	if escaped == "" {
		return prefix
	}
	if prefix == "" {
		return escaped
	}
	return prefix + "/" + escaped
}
