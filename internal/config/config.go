package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultHost = "0.0.0.0"
	DefaultPort = 17788
)

type Config struct {
	Host      string     `json:"host"`
	Port      int        `json:"port"`
	Key       string     `json:"key"`
	Rename    string     `json:"rename"`
	FilePath  string     `json:"filePath"`
	URLPrefix string     `json:"url_prefix"`
	S3        []S3Config `json:"s3"`
}

type S3Config struct {
	Bucket     string `json:"bucket"`
	Region     string `json:"region"`
	AccessKey  string `json:"access_key"`
	SecretKey  string `json:"secret_key"`
	Endpoint   string `json:"endpoint"`
	URLPrefix  string `json:"url_prefix"`
	Selected   bool   `json:"selected"`
	Name       string `json:"name"`
	ConfigName string `json:"_configName"`
}

type Runtime struct {
	Config     Config
	ConfigPath string
	LocalRoot  string
	Key        string
	Host       string
	Port       int
}

func LoadRuntime() (Runtime, error) {
	cfg, cfgPath, err := Load()
	if err != nil {
		return Runtime{}, err
	}

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
	}, nil
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
	return strings.TrimSpace(s.Bucket) != "" &&
		strings.TrimSpace(s.Region) != "" &&
		strings.TrimSpace(s.AccessKey) != "" &&
		strings.TrimSpace(s.SecretKey) != ""
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func IsNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
