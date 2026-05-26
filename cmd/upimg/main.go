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

	for i, selected := range runtime.Config.S3 {
		if !selected.Selected {
			continue
		}
		if !selected.Valid() {
			log.Printf("selected s3 config is invalid, fallback to local storage: %s missing %s", s3Label(selected), strings.Join(selected.MissingFields(), ", "))
			continue
		}

		s3Backend, err := storage.NewS3(ctx, selected)
		if err == nil {
			if err := probeBackend(ctx, s3Backend); err == nil {
				return s3Backend, nil
			} else {
				log.Printf("selected s3 is not reachable, fallback to local storage: %s target=s3:%d error=%v", s3Label(selected), i, err)
			}
		} else {
			log.Printf("selected s3 config is invalid, fallback to local storage: %s error=%v", s3Label(selected), err)
		}
	}
	for i, selected := range runtime.Config.WebDAV {
		if !selected.Selected {
			continue
		}
		if !selected.Valid() {
			log.Printf("selected webdav config is invalid, fallback to local storage: %s %s", webdavLabel(selected), webdavConfigError(selected))
			continue
		}
		webdavBackend, err := storage.NewWebDAV(selected)
		if err == nil {
			if err := probeBackend(ctx, webdavBackend); err == nil {
				return webdavBackend, nil
			} else {
				log.Printf("selected webdav is not reachable, fallback to local storage: %s target=webdav:%d error=%v", webdavLabel(selected), i, err)
			}
		} else {
			log.Printf("selected webdav config is invalid, fallback to local storage: %s error=%v", webdavLabel(selected), err)
		}
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
	return nil, fmt.Errorf("defaultTarget must be local, s3:<index>, or webdav:<index>")
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
