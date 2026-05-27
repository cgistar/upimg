package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"upimg/internal/config"
	"upimg/internal/server"
	"upimg/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	target := flag.String("t", "", "upload target directory")
	flag.StringVar(target, "target", "", "upload target directory")
	flag.Parse()

	runtime, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	backend, err := buildBackend(context.Background(), runtime)
	if err != nil {
		return err
	}
	app := server.New(runtime, backend)

	if flag.NArg() > 0 {
		results, err := app.UploadFiles(context.Background(), flag.Args(), *target)
		if err != nil {
			return err
		}
		for _, result := range results {
			fmt.Println(result.ImgURL)
		}
		return nil
	}

	addr := fmt.Sprintf("%s:%d", runtime.Host, runtime.Port)
	log.Printf("upimg server listening on http://%s storage=%s root=%s", addr, backend.Type(), runtime.LocalRoot)
	return http.ListenAndServe(addr, app.Handler())
}

func buildBackend(ctx context.Context, runtime config.Runtime) (storage.Backend, error) {
	if target := strings.TrimSpace(runtime.Config.DefaultTarget); target != "" {
		backend, err := backendForTarget(ctx, runtime, target)
		if err != nil {
			return nil, err
		}
		if err := probeBackend(ctx, backend); err != nil {
			return nil, err
		}
		return backend, nil
	}

	local, err := storage.NewLocal(runtime.LocalRoot)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(os.Getenv("FILEPATH")) == "" && runtime.Config.FilePath == "" {
		log.Printf("FILEPATH and config.filePath are empty, using current directory: %s", runtime.LocalRoot)
	}
	return local, nil
}

func backendForTarget(ctx context.Context, runtime config.Runtime, target string) (storage.Backend, error) {
	if target == "local" {
		return storage.NewLocal(runtime.LocalRoot)
	}
	if strings.HasPrefix(target, "s3:") {
		index, err := strconv.Atoi(strings.TrimPrefix(target, "s3:"))
		if err != nil || index < 0 || index >= len(runtime.Config.S3) {
			return nil, fmt.Errorf("defaultTarget points to missing s3 target")
		}
		cfg := runtime.Config.S3[index]
		if !cfg.Valid() {
			return nil, fmt.Errorf("defaultTarget s3 target is invalid: missing %s", strings.Join(cfg.MissingFields(), ", "))
		}
		return storage.NewS3(ctx, cfg)
	}
	if strings.HasPrefix(target, "webdav:") {
		index, err := strconv.Atoi(strings.TrimPrefix(target, "webdav:"))
		if err != nil || index < 0 || index >= len(runtime.Config.WebDAV) {
			return nil, fmt.Errorf("defaultTarget points to missing webdav target")
		}
		cfg := runtime.Config.WebDAV[index]
		if !cfg.Valid() {
			return nil, fmt.Errorf("defaultTarget webdav target is invalid: %s", webdavConfigError(cfg))
		}
		return storage.NewWebDAV(cfg)
	}
	if strings.HasPrefix(target, "sftp:") {
		index, err := strconv.Atoi(strings.TrimPrefix(target, "sftp:"))
		if err != nil || index < 0 || index >= len(runtime.Config.SFTP) {
			return nil, fmt.Errorf("defaultTarget points to missing sftp target")
		}
		cfg := runtime.Config.SFTP[index]
		if !cfg.Valid() {
			return nil, fmt.Errorf("defaultTarget sftp target is invalid: %s", sftpConfigError(cfg))
		}
		return storage.NewSFTP(cfg)
	}
	return nil, fmt.Errorf("defaultTarget must be local, s3:<index>, webdav:<index>, or sftp:<index>")
}

func probeBackend(ctx context.Context, backend storage.Backend) error {
	if probe, ok := backend.(interface{ Probe(context.Context) error }); ok {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := probe.Probe(probeCtx)
		cancel()
		return err
	}
	return nil
}

func s3Label(cfg config.S3Config) string {
	if name := strings.TrimSpace(cfg.Name); name != "" {
		return "name=" + name
	}
	if bucket := strings.TrimSpace(cfg.Bucket); bucket != "" {
		return "bucket=" + bucket
	}
	if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
		return "endpoint=" + endpoint
	}
	return "unknown"
}

func webdavLabel(cfg config.WebDAVConfig) string {
	if name := strings.TrimSpace(cfg.Name); name != "" {
		return "name=" + name
	}
	if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
		return "endpoint=" + endpoint
	}
	return "unknown"
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
