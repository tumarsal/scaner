package archive

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/svent/sift/gitignore"
)

// FileMapping представляет соответствие между хешем и полным путем файла
type FileMapping struct {
	Hash string `json:"hash"`
	Path string `json:"path"`
}

// folderTask — задача на обход папки (тяжёлая работа filepath выполняется в отдельном воркере).
type folderTask struct {
	path         string
	useGitIgnore bool
}

// ZipArchiver представляет асинхронный архиватор файлов
type ZipArchiver struct {
	zipPath            string
	password           string
	fileQueue          chan string
	folderQueue        chan folderTask
	fileMappings       []FileMapping
	filePaths          []string // используется только при useOriginalNames вместо fileMappings
	fileNamesInArchive []string // при useOriginalNames: имя в архиве (пустая строка = из filePath через archiveEntryName)
	useOriginalNames   bool     // при true имена в архиве — оригинальные пути, fileMappings не используется
	mappingsMu         sync.Mutex
	verbose            bool
	closed             bool
	mu                 sync.Mutex
	workerWg           sync.WaitGroup
}

// NewZipArchiver создает новый архиватор.
// useOriginalNames: при true файлы в архиве сохраняются под оригинальными путями, file_mappings.json не создаётся.
func NewZipArchiver(zipPath, password string, verbose bool, useOriginalNames bool) *ZipArchiver {
	archiver := &ZipArchiver{
		zipPath:            zipPath,
		password:           password,
		fileQueue:          make(chan string, 10000),
		folderQueue:        make(chan folderTask, 100),
		verbose:            verbose,
		useOriginalNames:   useOriginalNames,
		fileMappings:       make([]FileMapping, 0),
		filePaths:          make([]string, 0),
		fileNamesInArchive: make([]string, 0),
	}

	// Один воркер опрашивает оба канала: файлы и задачи по папкам
	archiver.workerWg.Add(1)
	go archiver.processQueues()

	return archiver
}

// AddFile добавляет файл в очередь для архивирования
func (z *ZipArchiver) AddFile(filePath string) error {
	z.mu.Lock()
	defer z.mu.Unlock()

	if z.closed {
		return errors.New("архиватор закрыт")
	}

	select {
	case z.fileQueue <- filePath:
		return nil
	default:
		return errors.New("очередь файлов переполнена")
	}
}

// AddFolder добавляет в архив все файлы из папки (рекурсивно).
// Обход папки выполняется в отдельной горутине; функция возвращает управление после постановки задачи в очередь.
// folderPath должен быть путём к существующей директории.
func (z *ZipArchiver) AddFolder(folderPath string) error {
	z.mu.Lock()
	defer z.mu.Unlock()

	if z.closed {
		return errors.New("архиватор закрыт")
	}

	info, err := os.Stat(folderPath)
	if err != nil {
		return fmt.Errorf("ошибка доступа к папке %s: %w", folderPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("путь не является папкой: %s", folderPath)
	}

	select {
	case z.folderQueue <- folderTask{path: folderPath, useGitIgnore: false}:
		return nil
	default:
		return errors.New("очередь папок переполнена")
	}
}

// AddGitFolder добавляет в архив все файлы из папки с учётом .gitignore.
// Обход и проверка git check-ignore выполняются в отдельной горутине.
// Требуется наличие git в PATH и что folderPath — корень репозитория или внутри него.
func (z *ZipArchiver) AddGitFolder(folderPath string) error {
	z.mu.Lock()
	defer z.mu.Unlock()

	if z.closed {
		return errors.New("архиватор закрыт")
	}

	info, err := os.Stat(folderPath)
	if err != nil {
		return fmt.Errorf("ошибка доступа к папке %s: %w", folderPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("путь не является папкой: %s", folderPath)
	}

	select {
	case z.folderQueue <- folderTask{path: folderPath, useGitIgnore: true}:
		return nil
	default:
		return errors.New("очередь папок переполнена")
	}
}

// processQueues в одной горутине опрашивает fileQueue и folderQueue.
func (z *ZipArchiver) processQueues() {
	defer z.workerWg.Done()
	fq := z.fileQueue
	fldq := z.folderQueue
	for fq != nil || fldq != nil {
		select {
		case path, ok := <-fq:
			if !ok {
				fq = nil
				continue
			}
			if err := z.processFile(path, true, ""); err != nil {
				z.logf("Ошибка обработки файла %s: %v\n", path, err)
			}
		case task, ok := <-fldq:
			if !ok {
				fldq = nil
				continue
			}
			if task.useGitIgnore {
				z.addGitFolderWalkDirect(task.path)
			} else {
				z.addFolderWalkDirect(task.path)
			}
		}
	}
}

// addFolderWalkDirect рекурсивно обходит папку и вызывает processFile для каждого файла.
func (z *ZipArchiver) addFolderWalkDirect(folderPath string) {
	if folderPath == "" {
		z.logf("AddFolder: пустой путь\n")
		return
	}
	info, err := os.Stat(folderPath)
	if err != nil {
		z.logf("AddFolder %s: путь не существует или недоступен: %v\n", folderPath, err)
		return
	}
	if !info.IsDir() {
		z.logf("AddFolder %s: путь не является директорией\n", folderPath)
		return
	}

	err = filepath.WalkDir(folderPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			z.logf("Ошибка обхода %s: %v\n", path, walkErr)
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if path == "" {
			return nil
		}
		if strings.Contains(path, ".DS_Store") {
			return nil
		}
		if _, statErr := os.Stat(path); statErr != nil {
			z.logf("Пропуск несуществующего пути %s: %v\n", path, statErr)
			return nil
		}

		rel, _ := filepath.Rel(folderPath, path)
		_ = z.processFile(path, false, filepath.ToSlash(rel))
		z.logf("Добавлен файл в архив: %s\n", path)
		return nil
	})
	if err != nil {
		z.logf("Ошибка обхода папки %s: %v\n", folderPath, err)
	}
}

// addGitFolderWalkDirect обходит папку с учётом .gitignore и вызывает processFile для каждого файла.
func (z *ZipArchiver) addGitFolderWalkDirect(folderPath string) {
	if folderPath == "" {
		z.logf("AddGitFolder: пустой путь\n")
		return
	}
	info, err := os.Stat(folderPath)
	if err != nil {
		z.logf("AddGitFolder %s: путь не существует или недоступен: %v\n", folderPath, err)
		return
	}
	if !info.IsDir() {
		z.logf("AddGitFolder %s: путь не является директорией\n", folderPath)
		return
	}

	var checker *gitignore.Checker
	c := gitignore.NewChecker()
	if err := c.LoadBasePath(folderPath); err != nil {
		z.logf("AddGitFolder %s: .gitignore не используется: %v\n", folderPath, err)
	} else {
		checker = c
	}

	errWalk := filepath.WalkDir(folderPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			z.logf("Ошибка обхода %s: %v\n", path, walkErr)
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.Contains(path, ".DS_Store") {
			return nil
		}
		if strings.Contains(path, "node_modules") {
			return nil
		}
		if path == "" {
			return nil
		}
		if checker != nil {
			fileInfo, err := d.Info()
			if err != nil {
				return nil
			}
			if checker.Check(path, fileInfo) {
				return nil
			}
		}
		if _, statErr := os.Stat(path); statErr != nil {
			z.logf("Пропуск несуществующего пути %s: %v\n", path, statErr)
			return nil
		}

		rel, _ := filepath.Rel(folderPath, path)
		_ = z.processFile(path, false, filepath.ToSlash(rel))
		return nil
	})
	if errWalk != nil {
		z.logf("Ошибка обхода папки %s: %v\n", folderPath, errWalk)
	}
}

// Close закрывает архиватор и создает финальный zip архив.
// Закрываются оба канала, воркер дообрабатывает очередь, затем создаётся архив.
func (z *ZipArchiver) Close() error {
	z.mu.Lock()
	if z.closed {
		z.mu.Unlock()
		return nil
	}
	z.closed = true
	z.mu.Unlock()

	close(z.folderQueue)
	close(z.fileQueue)
	z.workerWg.Wait()

	return z.createFinalZip()
}

// processFile обрабатывает один файл. logOnSuccess: при true и z.verbose выводится лог (очередь, AddGitFolder).
// nameInArchive: при добавлении из папки — путь в архиве относительно корня папки; пустая строка — использовать полный путь (AddFile).
func (z *ZipArchiver) processFile(filePath string, logOnSuccess bool, nameInArchive string) error {
	// Проверяем существование файла
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("файл не существует: %s", filePath)
	}

	var hash string
	z.mappingsMu.Lock()
	if z.useOriginalNames {
		z.filePaths = append(z.filePaths, filePath)
		z.fileNamesInArchive = append(z.fileNamesInArchive, nameInArchive)
	} else {
		hash = z.createHash(filePath)
		z.fileMappings = append(z.fileMappings, FileMapping{
			Hash: hash,
			Path: filePath,
		})
	}
	z.mappingsMu.Unlock()

	if logOnSuccess && z.verbose {
		if z.useOriginalNames {
			z.logf("Добавлен файл в архив: %s\n", filePath)
		} else {
			z.logf("Добавлен файл в архив: %s (хеш: %s)\n", filePath, hash)
		}
	}

	return nil
}

// createHash создает хеш от строки
func (z *ZipArchiver) createHash(input string) string {
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h)[:16] // Используем первые 16 символов хеша
}

// archiveEntryName возвращает имя записи в архиве для пути (оригинальный путь без тома на Windows).
func (z *ZipArchiver) archiveEntryName(path string) string {
	name := filepath.ToSlash(path)
	if vol := filepath.VolumeName(path); vol != "" {
		name = strings.TrimPrefix(name, vol)
		name = strings.TrimPrefix(name, "/")
	}
	return name
}

// filesCount возвращает количество файлов для архивирования
func (z *ZipArchiver) filesCount() int {
	z.mappingsMu.Lock()
	defer z.mappingsMu.Unlock()
	if z.useOriginalNames {
		return len(z.filePaths)
	}
	return len(z.fileMappings)
}

// createFinalZip создает финальный zip архив со всеми файлами
func (z *ZipArchiver) createFinalZip() error {
	count := z.filesCount()
	if count == 0 {
		return errors.New("нет файлов для архивирования")
	}

	z.logf("Создание архива %s с %d файлами\n", z.zipPath, count)

	// Если пароль не пустой, создаем зашифрованный архив
	if z.password != "" {
		return z.createEncryptedZip()
	}

	// Создаем обычный архив
	return z.createRegularZip()
}

// createRegularZip создает обычный zip архив
func (z *ZipArchiver) createRegularZip() error {
	// Создаем директорию для архива если не существует
	if err := os.MkdirAll(filepath.Dir(z.zipPath), 0755); err != nil {
		return fmt.Errorf("ошибка создания директории: %w", err)
	}

	// Создаем zip файл
	zipFile, err := os.Create(z.zipPath)
	if err != nil {
		return fmt.Errorf("ошибка создания zip файла: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	z.mappingsMu.Lock()
	total := 0
	if z.useOriginalNames {
		total = len(z.filePaths)
	} else {
		total = len(z.fileMappings)
	}
	showProgress := z.verbose && total > 0
	z.mappingsMu.Unlock()

	if z.useOriginalNames {
		z.mappingsMu.Lock()
		paths := make([]string, len(z.filePaths))
		copy(paths, z.filePaths)
		namesInArchive := make([]string, len(z.fileNamesInArchive))
		copy(namesInArchive, z.fileNamesInArchive)
		z.mappingsMu.Unlock()
		for i, path := range paths {
			nameInArchive := namesInArchive[i]
			if nameInArchive == "" {
				nameInArchive = z.archiveEntryName(path)
			}
			if err := z.addFileToZip(zipWriter, path, nameInArchive); err != nil {
				z.logf("Ошибка добавления файла %s: %v\n", path, err)
				continue
			}
			if showProgress {
				z.printZipProgress(i+1, total)
			}
		}
	} else {
		z.mappingsMu.Lock()
		mappings := make([]FileMapping, len(z.fileMappings))
		copy(mappings, z.fileMappings)
		z.mappingsMu.Unlock()
		for i, mapping := range mappings {
			if err := z.addFileToZip(zipWriter, mapping.Path, mapping.Hash); err != nil {
				z.logf("Ошибка добавления файла %s: %v\n", mapping.Path, err)
				continue
			}
			if showProgress {
				z.printZipProgress(i+1, total)
			}
		}
		// Добавляем файл с маппингом только при переименовании
		if err := z.addMappingFile(zipWriter); err != nil {
			return fmt.Errorf("ошибка добавления файла маппинга: %w", err)
		}
	}

	if showProgress {
		fmt.Fprint(os.Stderr, "\n")
	}

	z.logf("Архив успешно создан: %s\n", z.zipPath)
	return nil
}

// createEncryptedZip создает зашифрованный zip архив с помощью системной команды
func (z *ZipArchiver) createEncryptedZip() error {
	// Проверяем доступность команды zip
	if _, err := exec.LookPath("zip"); err != nil {
		return fmt.Errorf("команда zip не найдена: %w", err)
	}

	// Создаем временную директорию для файлов
	tempDir, err := os.MkdirTemp("", "zip_archiver_*")
	if err != nil {
		return fmt.Errorf("ошибка создания временной директории: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if z.useOriginalNames {
		z.mappingsMu.Lock()
		paths := make([]string, len(z.filePaths))
		copy(paths, z.filePaths)
		namesInArchive := make([]string, len(z.fileNamesInArchive))
		copy(namesInArchive, z.fileNamesInArchive)
		z.mappingsMu.Unlock()
		for i, path := range paths {
			nameInArchive := namesInArchive[i]
			if nameInArchive == "" {
				nameInArchive = z.archiveEntryName(path)
			}
			destPath := filepath.Join(tempDir, nameInArchive)
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				z.logf("Ошибка создания директории для %s: %v\n", path, err)
				continue
			}
			if err := z.copyFile(path, destPath); err != nil {
				z.logf("Ошибка копирования файла %s: %v\n", path, err)
				continue
			}
		}
		// Создаем зашифрованный архив с сохранением структуры путей
		cmd := exec.Command("zip", "-P", z.password, "-r", z.zipPath, ".")
		cmd.Dir = tempDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ошибка создания зашифрованного архива: %w", err)
		}
	} else {
		z.mappingsMu.Lock()
		mappings := make([]FileMapping, len(z.fileMappings))
		copy(mappings, z.fileMappings)
		z.mappingsMu.Unlock()

		var filesForZip []string
		for _, mapping := range mappings {
			destPath := filepath.Join(tempDir, mapping.Hash)
			if err := z.copyFile(mapping.Path, destPath); err != nil {
				z.logf("Ошибка копирования файла %s: %v\n", mapping.Path, err)
				continue
			}
			filesForZip = append(filesForZip, destPath)
		}

		// Создаем файл маппинга
		mappingPath := filepath.Join(tempDir, "file_mappings.json")
		if err := z.saveMappingFile(mappingPath); err != nil {
			return fmt.Errorf("ошибка создания файла маппинга: %w", err)
		}
		filesForZip = append(filesForZip, mappingPath)

		// Создаем зашифрованный архив
		args := []string{"-P", z.password, "-r", z.zipPath}
		args = append(args, filesForZip...)
		cmd := exec.Command("zip", args...)
		cmd.Dir = tempDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ошибка создания зашифрованного архива: %w", err)
		}
	}

	z.logf("Зашифрованный архив успешно создан: %s\n", z.zipPath)
	return nil
}

// addFileToZip добавляет файл в zip архив. nameInArchive — имя записи в архиве (хеш или оригинальный путь).
func (z *ZipArchiver) addFileToZip(zipWriter *zip.Writer, filePath, nameInArchive string) error {
	// Открываем исходный файл
	srcFile, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("ошибка открытия файла: %w", err)
	}
	defer srcFile.Close()

	// Получаем информацию о файле
	fileInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("ошибка получения информации о файле: %w", err)
	}

	// Создаем заголовок файла в архиве
	header, err := zip.FileInfoHeader(fileInfo)
	if err != nil {
		return fmt.Errorf("ошибка создания заголовка файла: %w", err)
	}

	// Устанавливаем имя файла в архиве (хеш при переименовании или оригинальный путь)
	header.Name = nameInArchive

	// Создаем writer для файла в архиве
	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("ошибка создания writer для файла: %w", err)
	}

	// Копируем содержимое файла
	_, err = io.Copy(writer, srcFile)
	if err != nil {
		return fmt.Errorf("ошибка копирования содержимого файла: %w", err)
	}

	return nil
}

// addMappingFile добавляет файл с маппингом в zip архив
func (z *ZipArchiver) addMappingFile(zipWriter *zip.Writer) error {
	// Создаем JSON с маппингом
	mappingData, err := json.MarshalIndent(z.fileMappings, "", "  ")
	if err != nil {
		return fmt.Errorf("ошибка сериализации маппинга: %w", err)
	}

	// Создаем заголовок для файла маппинга
	header := &zip.FileHeader{
		Name:   "file_mappings.json",
		Method: zip.Deflate,
	}

	// Создаем writer для файла маппинга
	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("ошибка создания writer для файла маппинга: %w", err)
	}

	// Записываем данные маппинга
	_, err = writer.Write(mappingData)
	if err != nil {
		return fmt.Errorf("ошибка записи данных маппинга: %w", err)
	}

	return nil
}

// saveMappingFile сохраняет файл маппинга на диск
func (z *ZipArchiver) saveMappingFile(filePath string) error {
	mappingData, err := json.MarshalIndent(z.fileMappings, "", "  ")
	if err != nil {
		return fmt.Errorf("ошибка сериализации маппинга: %w", err)
	}

	return os.WriteFile(filePath, mappingData, 0644)
}

// copyFile копирует файл из источника в назначение
func (z *ZipArchiver) copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// logf выводит сообщение только если включен verbose режим
func (z *ZipArchiver) logf(format string, args ...interface{}) {
	if z.verbose {
		fmt.Printf(format, args...)
	}
}

// printZipProgress выводит прогресс записи в zip (одна строка, обновляется через \r)
func (z *ZipArchiver) printZipProgress(current, total int) {
	pct := 0
	if total > 0 {
		pct = current * 100 / total
	}
	barWidth := 20
	filled := barWidth * current / total
	if filled > barWidth {
		filled = barWidth
	}
	bar := make([]byte, barWidth+2)
	bar[0] = '['
	for i := 0; i < barWidth; i++ {
		if i < filled {
			bar[i+1] = '='
		} else {
			bar[i+1] = ' '
		}
	}
	bar[barWidth+1] = ']'
	z.logf("\rЗапись в архив: %s %d/%d (%d%%)", bar, current, total, pct)
}

// GetQueueSize возвращает текущий размер очереди
func (z *ZipArchiver) GetQueueSize() int {
	return len(z.fileQueue)
}

// GetMappingsCount возвращает количество файлов для архивирования
func (z *ZipArchiver) GetMappingsCount() int {
	z.mappingsMu.Lock()
	defer z.mappingsMu.Unlock()
	if z.useOriginalNames {
		return len(z.filePaths)
	}
	return len(z.fileMappings)
}
