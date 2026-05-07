package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"upimg/internal/naming"
)

type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Local{root: abs}, nil
}

func (l *Local) Root() string {
	return l.root
}

func (l *Local) Type() string {
	return "local"
}

func (l *Local) Put(ctx context.Context, key, fileName string, body io.Reader) (StoredObject, error) {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return StoredObject{}, err
	}
	destination, err := l.safePath(key)
	if err != nil {
		return StoredObject{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return StoredObject{}, err
	}

	temp := filepath.Join(filepath.Dir(destination), fmt.Sprintf(".%s.%d.tmp", filepath.Base(destination), time.Now().UnixNano()))
	file, err := os.Create(temp)
	if err != nil {
		return StoredObject{}, err
	}

	if _, err := io.Copy(file, body); err != nil {
		file.Close()
		_ = os.Remove(temp)
		return StoredObject{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return StoredObject{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(temp)
		return StoredObject{}, err
	}
	if err := os.Rename(temp, destination); err != nil {
		_ = os.Remove(temp)
		return StoredObject{}, err
	}

	return StoredObject{FileName: fileName, URL: destination, Type: l.Type()}, nil
}

func (l *Local) Delete(ctx context.Context, key string) error {
	target, err := l.safePath(key)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("target is not a file")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Remove(target)
}

func (l *Local) FileURL(key, baseURL string) string {
	key = strings.TrimLeft(strings.ReplaceAll(key, "\\", "/"), "/")
	if baseURL == "" {
		return filepath.Join(l.root, filepath.FromSlash(key))
	}
	return strings.TrimRight(baseURL, "/") + "/" + key
}

func (l *Local) List(ctx context.Context, baseURL string) ([]Object, error) {
	var objects []Object
	err := filepath.WalkDir(l.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(l.root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		objects = append(objects, Object{
			Path:    key,
			URL:     l.FileURL(key, baseURL),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Type:    l.Type(),
		})
		return nil
	})
	if os.IsNotExist(err) {
		return objects, nil
	}
	return objects, err
}

func (l *Local) Open(key string) (*os.File, error) {
	target, err := l.safePath(key)
	if err != nil {
		return nil, err
	}
	return os.Open(target)
}

func (l *Local) safePath(key string) (string, error) {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return "", err
	}
	target := filepath.Join(l.root, filepath.FromSlash(key))
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rootWithSep := l.root + string(os.PathSeparator)
	if abs != l.root && !strings.HasPrefix(abs, rootWithSep) {
		return "", fmt.Errorf("path escapes local root")
	}
	return abs, nil
}
