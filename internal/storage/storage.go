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
