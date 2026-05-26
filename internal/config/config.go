package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"upimg/internal/naming"
)

const (
	DefaultHost = "0.0.0.0"
	DefaultPort = 17788
)

type Config struct {
	Host          string         `json:"host"`
	Port          int            `json:"port"`
	BasePath      string         `json:"basePath"`
	Key           string         `json:"key"`
	Rename        string         `json:"rename"`
	FilePath      string         `json:"filePath"`
	URLPrefix     string         `json:"urlPrefix"`
	DefaultTarget string         `json:"defaultTarget"`
	S3            []S3Config     `json:"s3"`
	WebDAV        []WebDAVConfig `json:"webdav"`
}

type S3Config struct {
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
	Endpoint        string `json:"endpoint"`
	URLPrefix       string `json:"urlPrefix"`
	UploadPath      string `json:"uploadPath"`
	Selected        bool   `json:"selected,omitempty"`
	Name            string `json:"name"`
}

type WebDAVConfig struct {
	Name       string `json:"name"`
	Endpoint   string `json:"endpoint"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	RootPath   string `json:"rootPath"`
	URLPrefix  string `json:"urlPrefix"`
	UploadPath string `json:"uploadPath"`
	Selected   bool   `json:"selected,omitempty"`
}

type Runtime struct {
	Config     Config
	ConfigPath string
	LocalRoot  string
	Key        string
	Host       string
	Port       int
	BasePath   string
}

func LoadRuntime() (Runtime, error) {
	cfg, cfgPath, err := Load()
	if err != nil {
		return Runtime{}, err
	}
	return RuntimeFromConfig(cfg, cfgPath)
}

func RuntimeFromConfig(cfg Config, cfgPath string) (Runtime, error) {
	var err error

	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = DefaultHost
	}

	port := cfg.Port
	if raw := strings.TrimSpace(os.Getenv("PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 65535 {
			return Runtime{}, fmt.Errorf("invalid PORT: %q", raw)
		}
		port = parsed
	}
	if port == 0 {
		port = DefaultPort
	}

	basePath := strings.TrimSpace(os.Getenv("BASE_PATH"))
	if basePath == "" {
		basePath = strings.TrimSpace(cfg.BasePath)
	}
	basePath, err = NormalizeBasePath(basePath)
	if err != nil {
		return Runtime{}, err
	}

	root := strings.TrimSpace(os.Getenv("FILEPATH"))
	if root == "" {
		root = strings.TrimSpace(cfg.FilePath)
	}
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return Runtime{}, fmt.Errorf("locate current directory: %w", err)
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Runtime{}, fmt.Errorf("resolve local root: %w", err)
	}

	key := strings.TrimSpace(os.Getenv("KEY"))
	if key == "" {
		key = strings.TrimSpace(cfg.Key)
	}

	return Runtime{
		Config:     cfg,
		ConfigPath: cfgPath,
		LocalRoot:  root,
		Key:        key,
		Host:       host,
		Port:       port,
		BasePath:   basePath,
	}, nil
}

func NormalizeBasePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || value == "/" {
		return "", nil
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	value = strings.TrimRight(value, "/")
	if strings.Contains(value, "//") || strings.Contains(value, "/../") || strings.HasSuffix(value, "/..") || strings.Contains(value, "/./") || strings.HasSuffix(value, "/.") {
		return "", fmt.Errorf("invalid basePath: %q", value)
	}
	return value, nil
}

func Write(path string, cfg Config) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is empty")
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".config.json-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempName)
		}
	}()

	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func WritablePath(current string) (string, error) {
	if strings.TrimSpace(current) != "" {
		return current, nil
	}
	if data := strings.TrimSpace(os.Getenv("DATA")); data != "" {
		return filepath.Join(data, "config.json"), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate current directory: %w", err)
	}
	return filepath.Join(wd, "config.json"), nil
}

func Load() (Config, string, error) {
	path, err := Path()
	if err != nil {
		return Config{}, "", err
	}
	if path == "" {
		return Config{}, "", nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, "", fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, "", fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, path, nil
}

func Path() (string, error) {
	if data := strings.TrimSpace(os.Getenv("DATA")); data != "" {
		path := filepath.Join(data, "config.json")
		if isFile(path) {
			return path, nil
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate current directory: %w", err)
	}
	path := filepath.Join(wd, "config.json")
	if isFile(path) {
		return path, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	path = filepath.Join(filepath.Dir(exe), "config.json")
	if isFile(path) {
		return path, nil
	}
	return "", nil
}

func SelectedS3(cfg Config) (S3Config, bool) {
	for _, item := range cfg.S3 {
		if item.Selected && item.Valid() {
			return item, true
		}
	}
	return S3Config{}, false
}

func (s S3Config) Valid() bool {
	return len(s.MissingFields()) == 0
}

func (s S3Config) MissingFields() []string {
	var missing []string
	if strings.TrimSpace(s.Bucket) == "" {
		missing = append(missing, "bucket")
	}
	if strings.TrimSpace(s.Region) == "" {
		missing = append(missing, "region")
	}
	if strings.TrimSpace(s.AccessKeyID) == "" {
		missing = append(missing, "accessKeyID")
	}
	if strings.TrimSpace(s.SecretAccessKey) == "" {
		missing = append(missing, "secretAccessKey")
	}
	return missing
}

func (w WebDAVConfig) Valid() bool {
	return len(w.MissingFields()) == 0 && len(w.InvalidFields()) == 0
}

func (w WebDAVConfig) MissingFields() []string {
	var missing []string
	endpoint := strings.TrimSpace(w.Endpoint)
	if endpoint == "" {
		missing = append(missing, "endpoint")
	}
	return missing
}

func (w WebDAVConfig) InvalidFields() []string {
	var invalid []string
	endpoint := strings.TrimSpace(w.Endpoint)
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			invalid = append(invalid, "endpoint")
		}
	}
	if rootPath := strings.TrimSpace(w.RootPath); rootPath != "" {
		if _, err := naming.SafeRelative(strings.Trim(rootPath, "/\\")); err != nil {
			invalid = append(invalid, "rootPath")
		}
	}
	return invalid
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func IsNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
