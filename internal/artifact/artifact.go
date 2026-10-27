package artifact

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luuna-ai/luuna-beacon/internal/model"
)

func Digest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func Snapshot(kind, name, source string) ([]model.ArtifactFile, error) {
	if !validName(name) {
		return nil, fmt.Errorf("artifact name must be 1-80 lowercase letters, digits, dots, dashes, or underscores, beginning with a letter or digit")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("source symlinks are not allowed")
	}
	files := []model.ArtifactFile{}
	if !info.IsDir() {
		file, err := encode(filepath.Base(source), source)
		return []model.ArtifactFile{file}, err
	}
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		relative, _ := filepath.Rel(source, path)
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source symlinks are not allowed: %s", relative)
		}
		if entry.IsDir() {
			if relative == ".git" || relative == "node_modules" || relative == ".luuna" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= 256 {
			return fmt.Errorf("artifact file limit exceeded")
		}
		file, err := encode(relative, path)
		if err != nil {
			return err
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if kind == "skill" && !hasFile(files, "SKILL.md") {
		return nil, fmt.Errorf("skill must contain SKILL.md")
	}
	if kind == "bundle" && !hasFile(files, ".claude-plugin/plugin.json") {
		return nil, fmt.Errorf("bundle must contain .claude-plugin/plugin.json")
	}
	return files, nil
}
func encode(logical, path string) (model.ArtifactFile, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return model.ArtifactFile{}, err
	}
	if len(content) > 4*1024*1024 {
		return model.ArtifactFile{}, fmt.Errorf("artifact file exceeds 4 MiB: %s", logical)
	}
	for _, part := range strings.Split(strings.ToLower(filepath.Base(logical)), ".") {
		if strings.Contains(part, "secret") || strings.Contains(part, "token") || strings.Contains(part, "password") {
			return model.ArtifactFile{}, fmt.Errorf("credential-like file rejected: %s", logical)
		}
	}
	encoding := "utf8"
	data := string(content)
	if !isUTF8(content) {
		encoding = "base64"
		data = base64.StdEncoding.EncodeToString(content)
	}
	info, _ := os.Stat(path)
	return model.ArtifactFile{Path: logical, Encoding: encoding, Content: data, SHA256: Digest(content), Executable: info != nil && info.Mode()&0o111 != 0}, nil
}
func isUTF8(value []byte) bool { return strings.ToValidUTF8(string(value), "\uFFFD") == string(value) }
func hasFile(files []model.ArtifactFile, path string) bool {
	for _, file := range files {
		if file.Path == path {
			return true
		}
	}
	return false
}
func Verify(record model.Artifact) error {
	if record.Kind != "skill" && record.Kind != "agent" && record.Kind != "bundle" {
		return fmt.Errorf("invalid artifact kind")
	}
	if !validName(record.Name) {
		return fmt.Errorf("invalid artifact name: %s", record.Name)
	}
	if record.Version < 1 || len(record.Files) == 0 {
		return fmt.Errorf("invalid artifact record")
	}
	for _, file := range record.Files {
		data, err := Decode(file)
		if err != nil {
			return err
		}
		if Digest(data) != file.SHA256 {
			return fmt.Errorf("artifact hash mismatch: %s", file.Path)
		}
	}
	if Digest(manifestBytes(record.Files)) != record.SHA256 {
		return fmt.Errorf("artifact manifest hash mismatch")
	}
	return nil
}
func Decode(file model.ArtifactFile) ([]byte, error) {
	if file.Encoding == "base64" {
		return base64.StdEncoding.DecodeString(file.Content)
	}
	return []byte(file.Content), nil
}
func New(kind, name string, files []model.ArtifactFile, version int) model.Artifact {
	return model.Artifact{ID: "artifact-" + fmt.Sprintf("%x", time.Now().UnixNano()), Kind: kind, Name: name, Version: version, Files: files, SHA256: Digest(manifestBytes(files)), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}

func manifestBytes(files []model.ArtifactFile) []byte {
	manifest := make([]struct {
		Path       string `json:"path"`
		SHA256     string `json:"sha256"`
		Executable bool   `json:"executable"`
	}, 0, len(files))
	for _, file := range files {
		manifest = append(manifest, struct {
			Path       string `json:"path"`
			SHA256     string `json:"sha256"`
			Executable bool   `json:"executable"`
		}{file.Path, file.SHA256, file.Executable})
	}
	encoded, _ := json.Marshal(manifest)
	return encoded
}

func validName(name string) bool {
	if len(name) == 0 || len(name) > 80 || name == "." || name == ".." {
		return false
	}
	for index, character := range name {
		if index == 0 && !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')) {
			return false
		}
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '-' || character == '_') {
			return false
		}
	}
	return true
}
