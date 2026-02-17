package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PathInfo — информация о пути (файл или папка) и его размер.
type PathInfo struct {
	Path  string
	IsDir bool
	Size  int64
}

// zipTask — задача на создание одного zip (путь, опции, логический путь для выгрузки).
type zipTask struct {
	path         string
	useGitIgnore bool
	pathPrefix   string // логический путь архива при выгрузке (например "folderLarge/largeSubfolder.zip")
}

// ArchiveResult — путь к созданному zip и логический путь для выгрузки.
type ArchiveResult struct {
	LocalPath string // путь к zip на диске
	FilePath  string // логический путь при выгрузке (например "folderLarge/largeSubfolder.zip")
}

// ZipsManager создаёт архивы в фоне: пути передаются через канал (Process / ProcessOne).
// Один воркер создаёт zip для каждого пути и отправляет путь к архиву в Ready(), сохраняя в Archives().
// При zipSizeLimit > 0 папки больше лимита разбиваются: дочерние папки упаковываются отдельно и не включаются в родительский zip.
// Process и ProcessOne можно вызывать многократно до вызова Close().
type ZipsManager struct {
	outputDir        string
	password         string
	verbose          bool
	zipSizeLimit     int64 // если > 0, папки больше лимита разбиваются на дочерние папки (каждая — отдельный zip)
	useOriginalNames bool  // при true файлы в архивах сохраняются под оригинальными путями, без file_mappings.json

	pathQueue  chan zipTask      // входная очередь
	readyChan  chan ArchiveResult // готовые архивы (локальный путь + путь для выгрузки)
	archives   []ArchiveResult
	archivesMu sync.Mutex
	closed     bool
	mu         sync.Mutex
	wg         sync.WaitGroup
}

// NewZipsManager создаёт менеджер и запускает фоновую горутину для создания архивов.
// outputDir — каталог для готовых zip. zipSizeLimit — при > 0 папки больше лимита разбиваются на дочерние zip.
// useOriginalNames — при true файлы в архивах не переименовываются в хеши, сохраняются оригинальные пути.
// После завершения добавления путей нужно вызвать Close().
func NewZipsManager(outputDir, password string, verbose bool, zipSizeLimit int64, useOriginalNames bool) *ZipsManager {
	m := &ZipsManager{
		outputDir:        outputDir,
		password:         password,
		verbose:          verbose,
		zipSizeLimit:     zipSizeLimit,
		useOriginalNames: useOriginalNames,
		pathQueue:        make(chan zipTask, 512),
		readyChan:        make(chan ArchiveResult, 64),
		archives:         make([]ArchiveResult, 0),
	}
	m.wg.Add(1)
	go m.worker()
	return m
}

// PathSize возвращает размер пути: для файла — размер файла, для папки — рекурсивная сумма размеров всех файлов.
// Второе возвращаемое значение — true, если путь является директорией.
func PathSize(path string) (size int64, isDir bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false, err
	}
	if !info.IsDir() {
		return info.Size(), false, nil
	}
	var total int64
	err = filepath.WalkDir(path, func(_ string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		total += info.Size()
		return nil
	})
	return total, true, err
}

// PathInfos возвращает информацию (путь, файл/папка, размер) для каждого пути из списка.
// Несуществующие или недоступные пути пропускаются (в срезе только успешные).
func PathInfos(paths []string) []PathInfo {
	var result []PathInfo
	for _, p := range paths {
		size, isDir, err := PathSize(p)
		if err != nil {
			continue
		}
		result = append(result, PathInfo{Path: p, IsDir: isDir, Size: size})
	}
	return result
}

// worker обрабатывает пути из pathQueue и создаёт для каждого архив.
func (m *ZipsManager) worker() {
	defer m.wg.Done()
	defer close(m.readyChan)

	if err := os.MkdirAll(m.outputDir, 0755); err != nil {
		m.logf("ZipsManager: ошибка создания каталога %s: %v\n", m.outputDir, err)
		return
	}

	usedNames := make(map[string]struct{})
	index := 0

	for task := range m.pathQueue {
		path := task.path
		info, err := os.Stat(path)
		if err != nil {
			m.logf("ZipsManager: пропуск %s: %v\n", path, err)
			continue
		}

		if info.IsDir() && m.zipSizeLimit > 0 {
			size, _, err := PathSize(path)
			if err != nil {
				m.logf("ZipsManager: ошибка размера %s: %v\n", path, err)
				continue
			}
			if size > m.zipSizeLimit {
				m.processLargeFolder(task, path, &index, usedNames)
				continue
			}
		}

		zipName := m.uniqueZipName(path, info.IsDir(), index, usedNames)
		index++
		zipPath := filepath.Join(m.outputDir, zipName)
		archiver := NewZipArchiver(zipPath, m.password, m.verbose, m.useOriginalNames)
		if info.IsDir() {
			if task.useGitIgnore {
				err := archiver.AddGitFolder(path)
				if err != nil {
					m.logf("ZipsManager: ошибка добавления git folder")
				}
			} else {
				err = archiver.AddFolder(path)
				if err != nil {
					m.logf("ZipsManager: ошибка добавления folder")
				}
			}
		} else {
			err = archiver.AddFile(path)
			if err != nil {
				m.logf("ZipsManager: ошибка добавления file")
			}
		}
		if err := archiver.Close(); err != nil {
			m.logf("ZipsManager: ошибка создания архива %s: %v\n", zipPath, err)
			continue
		}

		filePath := m.taskFilePath(task, path, info.IsDir())
		m.appendArchive(zipPath, filePath)
		select {
		case m.readyChan <- ArchiveResult{LocalPath: zipPath, FilePath: filePath}:
		default:
		}
	}
}

// taskFilePath возвращает логический путь архива при выгрузке.
func (m *ZipsManager) taskFilePath(task zipTask, path string, isDir bool) string {
	if task.pathPrefix != "" {
		return task.pathPrefix
	}
	base := filepath.Base(path)
	if base == "." || base == "/" {
		base = "archive"
	}
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return base + ".zip"
}

// processLargeFolder обрабатывает папку, размер которой превышает zipSizeLimit: дочерние папки отправляются отдельными zip, в текущий zip попадают только файлы из корня папки.
func (m *ZipsManager) processLargeFolder(task zipTask, path string, index *int, usedNames map[string]struct{}) {
	parentLogical := task.pathPrefix
	if parentLogical == "" {
		parentLogical = filepath.Base(path)
	} else {
		parentLogical = strings.TrimSuffix(parentLogical, ".zip")
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		m.logf("ZipsManager: ошибка чтения папки %s: %v\n", path, err)
		return
	}

	var directFiles []string
	for _, e := range entries {
		fullPath := filepath.Join(path, e.Name())
		if e.IsDir() {
			childPrefix := parentLogical + "/" + e.Name() + ".zip"
			select {
			case m.pathQueue <- zipTask{path: fullPath, useGitIgnore: task.useGitIgnore, pathPrefix: childPrefix}:
			default:
				m.logf("ZipsManager: очередь полна, пропуск дочерней папки %s\n", fullPath)
			}
		} else {
			directFiles = append(directFiles, fullPath)
		}
	}

	if len(directFiles) > 0 {
		zipName := m.uniqueZipName(path, true, *index, usedNames)
		*index++
		zipPath := filepath.Join(m.outputDir, zipName)
		archiver := NewZipArchiver(zipPath, m.password, m.verbose, m.useOriginalNames)
		for _, f := range directFiles {
			_ = archiver.AddFile(f)
		}
		if err := archiver.Close(); err != nil {
			m.logf("ZipsManager: ошибка создания архива %s: %v\n", zipPath, err)
			return
		}

		filePath := m.taskFilePath(task, path, true)
		m.appendArchive(zipPath, filePath)
		select {
		case m.readyChan <- ArchiveResult{LocalPath: zipPath, FilePath: filePath}:
		default:
		}
	}
}

func (m *ZipsManager) appendArchive(localPath, filePath string) {
	m.archivesMu.Lock()
	m.archives = append(m.archives, ArchiveResult{LocalPath: localPath, FilePath: filePath})
	m.archivesMu.Unlock()
}

// Process отправляет все пути во входную очередь; архивы создаются в фоне. Можно вызывать многократно.
func (m *ZipsManager) Process(paths []string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("ZipsManager закрыт")
	}
	m.mu.Unlock()

	for _, path := range paths {
		m.pathQueue <- zipTask{path: path, useGitIgnore: false}
	}
	return nil
}

// ProcessOne отправляет один путь во входную очередь (папка — AddFolder, файл — AddFile). Можно вызывать многократно.
func (m *ZipsManager) ProcessOne(path string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("ZipsManager закрыт")
	}
	m.mu.Unlock()

	m.pathQueue <- zipTask{path: path, useGitIgnore: false}
	return nil
}

// ProcessGitOne отправляет одну папку во входную очередь с учётом .gitignore (вызывается AddGitFolder). Можно вызывать многократно.
func (m *ZipsManager) ProcessGitOne(path string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("ZipsManager закрыт")
	}
	m.mu.Unlock()

	m.pathQueue <- zipTask{path: path, useGitIgnore: true}
	return nil
}

// uniqueZipName возвращает уникальное имя zip-файла (без коллизий в рамках одной выдачи).
func (m *ZipsManager) uniqueZipName(path string, isDir bool, index int, used map[string]struct{}) string {
	base := filepath.Base(path)
	if base == "." || base == "/" {
		base = fmt.Sprintf("archive_%d", index)
	}
	ext := filepath.Ext(base)
	if ext != "" {
		base = base[:len(base)-len(ext)]
	}
	name := base + ".zip"
	for {
		if _, exists := used[name]; !exists {
			used[name] = struct{}{}
			return name
		}
		index++
		name = fmt.Sprintf("%s_%d.zip", base, index)
	}
}

// Ready возвращает канал с готовыми архивами (LocalPath + FilePath). Закрывается после вызова Close() и завершения воркера.
func (m *ZipsManager) Ready() <-chan ArchiveResult {
	return m.readyChan
}

// Archives возвращает срез локальных путей к созданным архивам (безопасен для одновременного доступа).
func (m *ZipsManager) Archives() []string {
	m.archivesMu.Lock()
	defer m.archivesMu.Unlock()
	out := make([]string, len(m.archives))
	for i, a := range m.archives {
		out[i] = a.LocalPath
	}
	return out
}

// ArchivesWithPaths возвращает срез созданных архивов с логическими путями для выгрузки.
func (m *ZipsManager) ArchivesWithPaths() []ArchiveResult {
	m.archivesMu.Lock()
	defer m.archivesMu.Unlock()
	out := make([]ArchiveResult, len(m.archives))
	copy(out, m.archives)
	return out
}

// Close закрывает входную очередь и блокируется до завершения воркера. После Close вызовы Process/ProcessOne возвращают ошибку.
func (m *ZipsManager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.pathQueue)
	m.mu.Unlock()
	m.wg.Wait()
}

// DeleteCreatedArchives удаляет все созданные zip-файлы с диска. Вызывать после завершения загрузки и т.п.
func (m *ZipsManager) DeleteCreatedArchives() error {
	m.archivesMu.Lock()
	list := make([]string, len(m.archives))
	for i, a := range m.archives {
		list[i] = a.LocalPath
	}
	m.archivesMu.Unlock()

	var firstErr error
	for _, path := range list {
		if err := os.Remove(path); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Wait блокируется до завершения фонового воркера. Воркер завершается только после Close();
// без вызова Close() Wait() будет ждать бесконечно.
func (m *ZipsManager) Wait() {
	m.wg.Wait()
}

func (m *ZipsManager) logf(format string, args ...interface{}) {
	if m.verbose {
		fmt.Printf(format, args...)
	}
}
