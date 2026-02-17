package archive

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewZipArchiver(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "out.zip")
	z := NewZipArchiver(zipPath, "", false, false)
	if z == nil {
		t.Fatal("NewZipArchiver вернул nil")
	}
	// Закрываем без добавления файлов — должна быть ошибка
	if err := z.Close(); err == nil {
		t.Error("Close() без файлов должен вернуть ошибку")
	}
}

func TestAddFile_Closed(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "out.zip")
	z := NewZipArchiver(zipPath, "", false, false)
	_ = z.Close()
	err := z.AddFile("/any/path")
	if err == nil {
		t.Fatal("AddFile после Close должен вернуть ошибку")
	}
	if !strings.Contains(err.Error(), "закрыт") {
		t.Errorf("ожидалась ошибка про закрытый архиватор, получено: %v", err)
	}
}

func TestAddFile_NotExists(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "out.zip")
	z := NewZipArchiver(zipPath, "", false, false)
	defer func() {
		_ = z.Close() // может вернуть ошибку "нет файлов"
	}()

	err := z.AddFile(filepath.Join(dir, "nonexistent.txt"))
	if err != nil {
		t.Fatalf("AddFile не должен возвращать ошибку при постановке в очередь: %v", err)
	}
	// При Close воркер обработает путь и не найдёт файл; в маппинге 0 файлов
	if err := z.Close(); err == nil {
		t.Error("Close() при несуществующем файле в очереди должен привести к ошибке (нет файлов для архивирования)")
	}
}

func TestAddFolder_NotDir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file.txt")
	_ = os.WriteFile(f, nil, 0644)
	z := NewZipArchiver(filepath.Join(dir, "out.zip"), "", false, false)
	defer func() { _ = z.Close() }()

	err := z.AddFolder(f)
	if err == nil {
		t.Fatal("AddFolder(файл) должен вернуть ошибку")
	}
	if !strings.Contains(err.Error(), "папкой") && !strings.Contains(err.Error(), "директори") {
		t.Errorf("ожидалась ошибка про папку: %v", err)
	}
}

func TestAddFolder_NotExists(t *testing.T) {
	dir := t.TempDir()
	z := NewZipArchiver(filepath.Join(dir, "out.zip"), "", false, false)
	defer func() { _ = z.Close() }()

	err := z.AddFolder(filepath.Join(dir, "nonexistent"))
	if err == nil {
		t.Fatal("AddFolder(несуществующий путь) должен вернуть ошибку")
	}
}

func TestAddFolder_Recursive(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "out.zip")
	// Создаём папку с двумя файлами
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	z := NewZipArchiver(zipPath, "", false, false)
	if err := z.AddFolder(sub); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var mappings []FileMapping
	for _, f := range r.File {
		if f.Name == "file_mappings.json" {
			rc, _ := f.Open()
			err := json.NewDecoder(rc).Decode(&mappings)
			_ = rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if len(mappings) != 2 {
		t.Fatalf("ожидалось 2 файла в маппинге, получено %d", len(mappings))
	}
}

func TestClose_Idempotent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(f, []byte("x"), 0644)
	z := NewZipArchiver(filepath.Join(dir, "out.zip"), "", false, false)
	_ = z.AddFile(f)
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	// Второй Close не должен паниковать и возвращает nil
	if err := z.Close(); err != nil {
		t.Errorf("второй Close() должен вернуть nil: %v", err)
	}
}

func TestAddFolder_Closed(t *testing.T) {
	dir := t.TempDir()
	z := NewZipArchiver(filepath.Join(dir, "out.zip"), "", false, false)
	_ = z.Close()

	err := z.AddFolder(dir)
	if err == nil {
		t.Fatal("AddFolder после Close должен вернуть ошибку")
	}
	if !strings.Contains(err.Error(), "закрыт") {
		t.Errorf("ожидалась ошибка про закрытый архиватор: %v", err)
	}
}

func TestAddGitFolder_NotDir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(f, nil, 0644)
	z := NewZipArchiver(filepath.Join(dir, "out.zip"), "", false, false)
	defer func() { _ = z.Close() }()

	err := z.AddGitFolder(f)
	if err == nil {
		t.Fatal("AddGitFolder(файл) должен вернуть ошибку")
	}
}

// TestAddGitFolder_WithGitignore проверяет, что при AddGitFolder игнорируются файлы из .gitignore.
func TestAddGitFolder_WithGitignore(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "out.zip")
	// Инициализируем git-репозиторий
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("kept"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("ignored"), 0644); err != nil {
		t.Fatal(err)
	}

	z := NewZipArchiver(zipPath, "", false, false)
	if err := z.AddGitFolder(dir); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var mappings []FileMapping
	for _, f := range r.File {
		if f.Name == "file_mappings.json" {
			rc, _ := f.Open()
			_ = json.NewDecoder(rc).Decode(&mappings)
			_ = rc.Close()
			break
		}
	}
	// В архив должен попасть только kept.txt (ignored.txt игнорируется)
	if len(mappings) != 1 {
		t.Fatalf("ожидался 1 файл (kept.txt), в маппинге %d", len(mappings))
	}
	if !strings.HasSuffix(mappings[0].Path, "kept.txt") {
		t.Errorf("в архиве должен быть kept.txt, получен путь: %s", mappings[0].Path)
	}
}

func TestGetQueueSize_GetMappingsCount(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(f, nil, 0644)
	z := NewZipArchiver(filepath.Join(dir, "out.zip"), "", false, false)

	// До добавления очередь может быть 0
	_ = z.AddFile(f)
	// GetQueueSize возвращает len(fileQueue) — после неблокирующей отправки может быть 0
	_ = z.GetQueueSize()
	_ = z.GetMappingsCount()

	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	// После Close можно всё ещё вызывать (очередь закрыта, маппинг остаётся)
	_ = z.GetQueueSize()
	_ = z.GetMappingsCount()
}
