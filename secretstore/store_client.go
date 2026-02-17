package secretstore

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UploadTask представляет задачу загрузки файла (folder) по пути RemotePath.
type UploadTask struct {
	FilePath              string // локальный путь к файлу (например zip)
	RemotePath            string // путь на сервере (например "folder/subfolder.zip")
	Retries               int
	MaxRetries            int
	DeleteLocalAfterUpload bool  // удалить локальный файл после успешной загрузки (для временных файлов UploadText)
}

// Client — интерфейс клиента хранилища секретов
type Client interface {
	Close()
	GetQueueSize() int
	WaitForQueueEmpty() error
	UploadFolder(localPath, filePath string) error
	UploadText(text, filename string) error
	ListFiles(password string) ([]FileInfo, error)
	DownloadFile(fileHash, password, outputPath string) error
}

// ClientImpl представляет клиент для работы с хранилищем секретов
type ClientImpl struct {
	baseURL         string
	client          *http.Client
	uploadClient    *http.Client // увеличенный таймаут для загрузки больших файлов (folder)
	uploadQueue     chan *UploadTask
	workerCount     int
	mu              sync.Mutex
	closed          bool
	verbose         bool
	pendingTasks    sync.WaitGroup // счётчик задач (очередь + в работе); WaitForQueueEmpty ждёт его обнуления
	uploadErrMu     sync.Mutex
	uploadErr       error // первая ошибка выгрузки после исчерпания повторов; WaitForQueueEmpty возвращает её
	uploadChunkSize int64 // размер одной части при поточной загрузке (файлы больше — частями); по умолчанию 5 MB
}

// Размер части загрузки по умолчанию (5 MB), если при создании клиента передан 0 или отрицательный.
const defaultUploadChunkSize = 15 * 1024 * 1024

// NewClient создает новый клиент. chunkSize — размер одной части в байтах при разбиении большого файла;
// 0 или отрицательный — по умолчанию 5 MB (ограничение nginx и т.п.).
// checkRedirectPreservePost запрещает следовать редиректу 301/302 для POST (иначе Go повторяет запрос как GET → 405).
func checkRedirectPreservePost(req *http.Request, via []*http.Request) error {
	if len(via) > 0 && via[0].Method == http.MethodPost && req.Method == http.MethodGet {
		return fmt.Errorf("редирект 301/302 меняет POST на GET — настройте прокси/сервер: не делать редирект для этого пути или использовать 307/308")
	}
	return nil
}

func NewClient(baseURL string, verbose bool, chunkSize int64) Client {
	if chunkSize <= 0 {
		chunkSize = defaultUploadChunkSize
	}
	uploadClient := &http.Client{Timeout: 60 * time.Minute}
	uploadClient.CheckRedirect = checkRedirectPreservePost
	c := &ClientImpl{
		baseURL:         baseURL,
		client:          &http.Client{Timeout: 30 * time.Second},
		uploadClient:    uploadClient,
		uploadQueue:     make(chan *UploadTask, 100000),
		workerCount:     3,
		verbose:         verbose,
		uploadChunkSize: chunkSize,
	}

	// Запускаем воркеры для обработки очереди
	for i := 0; i < c.workerCount; i++ {
		go c.uploadWorker()
	}

	return c
}

// checkInternetConnection проверяет доступность сервера по GET /api/secrets/check.
func (c *ClientImpl) checkInternetConnection() bool {
	u := strings.TrimSuffix(c.baseURL, "/") + "/api/secrets/check"
	resp, err := c.client.Get(u)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
func (c *ClientImpl) logf(format string, args ...interface{}) {
	if c.verbose {
		fmt.Printf(format, args...)
	}
}

// uploadWorker обрабатывает задачи из очереди загрузки
func (c *ClientImpl) uploadWorker() {
	for task := range c.uploadQueue {
		// Проверяем интернет соединение перед загрузкой
		if !c.checkInternetConnection() {
			c.logf("No internet connection, retrying...\n")
			if task.Retries < task.MaxRetries {
				task.Retries++
				time.Sleep(5 * time.Second)
				c.uploadQueue <- task
			} else {
				c.recordUploadError(fmt.Errorf("нет связи с интернетом"))
				c.pendingTasks.Done()
			}
			continue
		}
		if task.Retries == 0 {
			c.logf("Uploading folder: %s -> %s\n", task.FilePath, task.RemotePath)
		}
		err := c.uploadFolderTaskInternal(task.FilePath, task.RemotePath, task.Retries == 0)
		if err != nil {
			if task.Retries < task.MaxRetries {
				c.logf("Ошибка выгрузки, повтор %d/%d: %v\n", task.Retries+1, task.MaxRetries, err)
				task.Retries++
				time.Sleep(2 * time.Second)
				c.uploadQueue <- task
			} else {
				c.logf("Выгрузка %s не удалась после %d попыток: %v\n", task.RemotePath, task.MaxRetries+1, err)
				c.recordUploadError(fmt.Errorf("выгрузка %s: %w", task.RemotePath, err))
				c.pendingTasks.Done()
			}
		} else {
			if task.DeleteLocalAfterUpload {
				_ = os.Remove(task.FilePath)
			}
			c.pendingTasks.Done()
		}
	}
}

// formatByteSize форматирует размер в байтах (например "5.2 MB").
func formatByteSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// uploadFolderTaskInternal выполняет загрузку файла (например zip) по пути filePath на сервер.
// Если файл больше c.uploadChunkSize — отправка по частям (заголовки X-Upload-Id, X-Part-Index, X-Total-Parts).
// Иначе — потоковая отправка через io.Pipe (без загрузки всего файла в память).
// logProgress — выводить ли прогресс (части) в лог; при повторах не выводим, чтобы не дублировать.
func (c *ClientImpl) uploadFolderTaskInternal(localPath, filePath string, logProgress bool) error {
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}
	size := stat.Size()
	filename := filepath.Base(localPath)

	if size > c.uploadChunkSize {
		return c.uploadFolderChunked(file, size, filename, filePath, logProgress)
	}
	return c.uploadFolderStreaming(file, filename, filePath)
}

// uploadFolderStreaming отправляет файл одним запросом потоково (multipart через Pipe) на /api/secrets/folder/{filePath}.
func (c *ClientImpl) uploadFolderStreaming(file *os.File, filename, filePath string) error {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			pw.CloseWithError(fmt.Errorf("create form file: %w", err))
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			pw.CloseWithError(fmt.Errorf("copy content: %w", err))
			return
		}
		if err := writer.Close(); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	base := strings.TrimSuffix(c.baseURL, "/")
	u := base + "/api/secrets/folder/" + filePath
	req, err := http.NewRequest("POST", u, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.uploadClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload folder failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result UploadFolderResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("invalid JSON response: %w", err)
	}
	return nil
}

// uploadFolderChunked отправляет файл частями (каждая часть — отдельный POST с заголовками).
func (c *ClientImpl) uploadFolderChunked(file *os.File, size int64, filename, filePath string, logProgress bool) error {
	uploadID := make([]byte, 16)
	if _, err := rand.Read(uploadID); err != nil {
		return fmt.Errorf("failed to generate upload id: %w", err)
	}
	uploadIDStr := hex.EncodeToString(uploadID)

	chunkSize := c.uploadChunkSize
	totalParts := int((size + chunkSize - 1) / chunkSize)
	base := strings.TrimSuffix(c.baseURL, "/")
	u := base + "/api/secrets/folder/" + filePath
	if logProgress {
		c.logf("Uploading in %d parts (%d MB each)\n", totalParts, chunkSize/(1024*1024))
	}

	buf := make([]byte, chunkSize)
	var lastResp *UploadFolderResponse
	startTime := time.Now()
	for i := 0; i < totalParts; i++ {
		n, err := io.ReadFull(file, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return fmt.Errorf("failed to read part %d: %w", i, err)
		}
		if n == 0 {
			break
		}
		chunk := buf[:n]

		req, err := http.NewRequest("POST", u, bytes.NewReader(chunk))
		if err != nil {
			return fmt.Errorf("failed to create request for part %d: %w", i, err)
		}
		req.Header.Set("X-Upload-Id", uploadIDStr)
		req.Header.Set("X-Part-Index", fmt.Sprintf("%d", i))
		req.Header.Set("X-Total-Parts", fmt.Sprintf("%d", totalParts))
		req.Header.Set("X-Filename", filename)
		req.ContentLength = int64(len(chunk))

		resp, err := c.uploadClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send part %d: %w", i, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read response for part %d: %w", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("upload part %d failed with status %d: %s", i, resp.StatusCode, string(body))
		}
		var partResult UploadFolderResponse
		_ = json.Unmarshal(body, &partResult)
		if partResult.FileHash != "" {
			lastResp = &partResult
		}
		if logProgress {
			elapsed := time.Since(startTime)
			progressPct := (i + 1) * 100 / totalParts
			eta := time.Duration(0)
			if i+1 < totalParts && i+1 > 0 {
				eta = time.Duration(int64(elapsed) * int64(totalParts-i-1) / int64(i+1))
			}
			c.logf("Part %d/%d (%s, %d%%, осталось ~%s)\n", i+1, totalParts, formatByteSize(int64(len(chunk))), progressPct, eta.Round(time.Second))
		}
	}
	if lastResp == nil {
		return fmt.Errorf("сервер не вернул финальный ответ с fileHash после последней части (ожидается JSON с полем filehash при сборке файла на сервере)")
	}
	return nil
}

// Close закрывает клиент и останавливает воркеры
func (c *ClientImpl) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		c.closed = true
		close(c.uploadQueue)
	}
}

// GetQueueSize возвращает текущий размер очереди
func (c *ClientImpl) GetQueueSize() int {
	return len(c.uploadQueue)
}

// recordUploadError сохраняет первую ошибку выгрузки; WaitForQueueEmpty вернёт её.
func (c *ClientImpl) recordUploadError(err error) {
	c.uploadErrMu.Lock()
	if c.uploadErr == nil {
		c.uploadErr = err
	}
	c.uploadErrMu.Unlock()
}

// WaitForQueueEmpty ждёт, пока очередь не опустеет и все задачи не завершатся.
// Возвращает ошибку, если хотя бы одна выгрузка не удалась после всех повторов.
func (c *ClientImpl) WaitForQueueEmpty() error {
	for len(c.uploadQueue) > 0 {
		c.logf("Queue size: %d\n", len(c.uploadQueue))
		time.Sleep(100 * time.Millisecond)
	}
	c.pendingTasks.Wait()
	c.uploadErrMu.Lock()
	err := c.uploadErr
	c.uploadErrMu.Unlock()
	return err
}

// fileHash возвращает SHA256-хеш содержимого файла в виде hex-строки (потоково, без загрузки в память).
func (c *ClientImpl) fileHash(localPath string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open file for hash: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to compute file hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// UploadFolder ставит загрузку файла (например zip) в очередь воркера.
// filePath — путь на сервере (например "folderLarge/largeSubfolder.zip"); clientIp сервер берёт из запроса.
// Если filePath не задан (пустая строка), в качестве пути используется SHA256-хеш содержимого файла (как в прежнем API).
// Реальное выполнение — в uploadWorker. Дождаться завершения всех загрузок: WaitForQueueEmpty().
func (c *ClientImpl) UploadFolder(localPath, filePath string) error {
	if _, err := os.Stat(localPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file does not exist: %s", localPath)
		}
		return fmt.Errorf("failed to stat file: %w", err)
	}

	if strings.TrimSpace(filePath) == "" {
		hash, err := c.fileHash(localPath)
		if err != nil {
			return err
		}
		filePath = hash
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("client is closed")
	}
	task := &UploadTask{
		FilePath:   localPath,
		RemotePath: filePath,
		Retries:    0,
		MaxRetries: 3,
	}
	return c.enqueueTask(task)
}

// UploadText записывает текст во временный файл и ставит его загрузку в очередь (путь на сервере — по хешу содержимого).
// filename задаёт имя файла на сервере (в метаданных); временный файл удаляется после успешной загрузки.
func (c *ClientImpl) UploadText(text, filename string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("client is closed")
	}
	c.mu.Unlock()

	suffix := filename
	if suffix == "" {
		suffix = "file.txt"
	} else {
		suffix = filepath.Base(suffix)
		if suffix == "" || suffix == "." {
			suffix = "file.txt"
		}
	}
	tmp, err := os.CreateTemp("", "uploadtext.*."+suffix)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	hash, err := c.fileHash(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		os.Remove(tmpPath)
		return fmt.Errorf("client is closed")
	}
	task := &UploadTask{
		FilePath:               tmpPath,
		RemotePath:             hash,
		Retries:                0,
		MaxRetries:             3,
		DeleteLocalAfterUpload: true,
	}
	return c.enqueueTask(task)
}

// enqueueTask ставит задачу в очередь загрузки (вызывать при захваченном c.mu для проверки closed).
func (c *ClientImpl) enqueueTask(task *UploadTask) error {
	c.pendingTasks.Add(1)
	select {
	case c.uploadQueue <- task:
		c.mu.Unlock()
		return nil
	default:
		c.pendingTasks.Done()
		c.mu.Unlock()
		return fmt.Errorf("upload queue is full")
	}
}

// ListFiles получает список всех файлов
func (c *ClientImpl) ListFiles(password string) ([]FileInfo, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/secrets/list?password=%s", c.baseURL, password), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Files []FileInfo `json:"files"`
		Count int        `json:"count"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Files, nil
}

// DownloadFile скачивает файл по хешу
func (c *ClientImpl) DownloadFile(fileHash, password, outputPath string) error {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/secrets/files/%s?password=%s", c.baseURL, fileHash, password), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body))
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	return nil
}
