package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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
	if selected, ok := config.SelectedS3(runtime.Config); ok {
		s3Backend, err := storage.NewS3(ctx, selected)
		if err == nil {
			probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = s3Backend.Probe(probeCtx)
			cancel()
			if err == nil {
				return s3Backend, nil
			}
			log.Printf("selected s3 is not reachable, fallback to local storage: %v", err)
		} else {
			log.Printf("selected s3 config is invalid, fallback to local storage: %v", err)
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
