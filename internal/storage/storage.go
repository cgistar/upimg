package storage

import (
	"context"
	"io"
	"time"
)

type Object struct {
	Path    string    `json:"path"`
	URL     string    `json:"url"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Type    string    `json:"type"`
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
	List(ctx context.Context, baseURL string) ([]Object, error)
}
