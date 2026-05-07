package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"
)

const DefaultTemplate = "{fname}{ext}"

func ObjectKey(fileName, target, template string, now time.Time) (string, error) {
	if strings.TrimSpace(fileName) == "" {
		return "", fmt.Errorf("file name is empty")
	}
	if target != "" {
		dir, err := SafeRelative(target)
		if err != nil {
			return "", err
		}
		return path.Join(dir, fileName), nil
	}
	if strings.TrimSpace(template) == "" {
		template = DefaultTemplate
	}
	return RenderTemplate(template, fileName, now)
}

func RenderTemplate(template, fileName string, now time.Time) (string, error) {
	stem, ext := splitNameExt(fileName)
	hash := shortHash(fileName)

	rendered := strings.NewReplacer(
		"{year}", fmt.Sprintf("%04d", now.Year()),
		"{month}", fmt.Sprintf("%02d", int(now.Month())),
		"{day}", fmt.Sprintf("%02d", now.Day()),
		"{unix_ts}", fmt.Sprintf("%d", now.Unix()),
		"{fname_hash}", hash,
		"{filename}", fileName,
		"{fname}", stem,
		"{ext}", ext,
	).Replace(template)

	return SafeRelative(rendered)
}

func splitNameExt(fileName string) (string, string) {
	index := strings.Index(fileName, ".")
	if index < 0 {
		return fileName, ""
	}
	return fileName[:index], fileName[index:]
}

func SafeRelative(value string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimLeft(value, "/\\"))
	if trimmed == "" {
		return "", fmt.Errorf("path is empty")
	}

	cleaned := path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || path.IsAbs(cleaned) {
		return "", fmt.Errorf("path contains unsafe component")
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return "", fmt.Errorf("path contains unsafe component")
		}
	}
	return cleaned, nil
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}
