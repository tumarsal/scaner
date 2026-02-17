package secretstore

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FileInfo содержит метаинформацию о загруженном файле
type FileInfo struct {
	IP       string    `json:"ip"`
	Filename string    `json:"filename"`
	FilePath string    `json:"filepath"` // Полный путь к файлу от клиента (например: /home/user/documents/file.txt)
	FileHash string    `json:"filehash"`
	Size     int64     `json:"size"`
	Uploaded time.Time `json:"uploaded"`
	MimeType string    `json:"mime_type"`
}

// PathItem представляет элемент пути (папку или файл)
type PathItem struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	IsFile   bool      `json:"is_file"`
	FileInfo *FileInfo `json:"file_info,omitempty"`
}

// UploadFolderResponse — ответ UploadFolderHandler в формате JSON
type UploadFolderResponse struct {
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	FileHash string `json:"filehash"`
	FilePath string `json:"filepath"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

// Store представляет хранилище секретных файлов
type Store struct {
	secretsDir string
	filesDB    string
	password   string

	// поточная загрузка частями (большие файлы)
	uploadsMu sync.Mutex
	uploads   map[string]*chunkedUploadState
}

func (s *Store) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/secrets/check", s.CheckHandler)
	mux.HandleFunc("/api/secrets/list/", s.ListHandler)
	mux.HandleFunc("/api/secrets/files/", s.FileHandler)
	mux.HandleFunc("/api/secrets/folder/", s.UploadFolderHandler)
}

// chunkedUploadState — состояние загрузки по частям (upload_id → части на диске)
type chunkedUploadState struct {
	clientIP   string
	path       string
	filePath   string
	filename   string
	totalParts int
	dir        string
	received   []bool
}

// NewStore создает новый экземпляр хранилища
func NewStore(secretsDir, password string) *Store {
	return &Store{
		secretsDir: secretsDir,
		filesDB:    filepath.Join(secretsDir, "files.jsonl"),
		password:   password,
		uploads:    make(map[string]*chunkedUploadState),
	}
}

// Init инициализирует хранилище, создавая необходимые директории
func (s *Store) Init() error {
	if err := os.MkdirAll(s.secretsDir, 0755); err != nil {
		return fmt.Errorf("failed to create secrets directory: %w", err)
	}
	return nil
}

// mux.HandleFunc("/api/secrets/upload", s.secretStore.UploadHandler)
// mux.HandleFunc("/api/secrets/list/", s.secretStore.ListHandler)
// mux.HandleFunc("/api/secrets/files/", s.secretStore.FileHandler)
// mux.HandleFunc("/api/secrets/folder/", s.secretStore.UploadFolderHandler)
// mux.HandleFunc("/api/secrets/check", s.secretStore.CheckHandler)

// CheckHandler отвечает на GET /api/secrets/check — проверка доступности сервера (для checkInternetConnection клиента).
func (s *Store) CheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// UploadFolderHandler обрабатывает загрузку файлов (например zip-архивов папки) по пути path.
// URL: POST /api/secrets/folder/path — clientIp берётся из запроса (как в UploadHandler), path задаёт filepath под этим IP.
// Если в запросе есть заголовки X-Upload-Id, X-Part-Index, X-Total-Parts — обрабатывается как часть большого файла (поточная загрузка).
// OPTIONS возвращает 200 с Allow: POST (CORS preflight). 405 при GET часто из-за редиректа 301/302 (клиент повторяет запрос как GET).
func (s *Store) UploadFolderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "POST, OPTIONS")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := s.getClientIP(r)
	if clientIP == "" {
		http.Error(w, "Unable to determine client IP", http.StatusBadRequest)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/secrets/folder/")
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		http.Error(w, "Path not specified", http.StatusBadRequest)
		return
	}

	log.Printf("Upload started: clientIP=%s path=%s", clientIP, path)

	filePath := "/" + path

	uploadID := r.Header.Get("X-Upload-Id")
	partIndexStr := r.Header.Get("X-Part-Index")
	totalPartsStr := r.Header.Get("X-Total-Parts")
	if uploadID != "" && partIndexStr != "" && totalPartsStr != "" {
		s.uploadFolderPartHandler(w, r, clientIP, path, filePath, uploadID, partIndexStr, totalPartsStr)
		return
	}

	s.uploadFolderSingleFile(w, r, clientIP, path, filePath)
}

// uploadFolderSingleFile обрабатывает загрузку одного файла (не по частям): multipart form, потоковая запись, хеш по имени.
func (s *Store) uploadFolderSingleFile(w http.ResponseWriter, r *http.Request, clientIP, path, filePath string) {
	// Не загружаем тело в память целиком — потоковая запись на диск (поддержка больших файлов, в т.ч. ~1 GB).
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		log.Printf("Upload single file: clientIP=%s path=%s parse form error: %v", clientIP, path, err)
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		log.Printf("Upload single file: clientIP=%s path=%s no file in form: %v", clientIP, path, err)
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ipDir := filepath.Join(s.secretsDir, clientIP)
	if err := os.MkdirAll(ipDir, 0755); err != nil {
		log.Printf("Upload single file: clientIP=%s path=%s mkdir error: %v", clientIP, path, err)
		http.Error(w, "Failed to create directory", http.StatusInternalServerError)
		return
	}

	// Временный файл: пишем потоково, хеш считаем по ходу; переименуем в fileHash после записи.
	destFile, err := os.CreateTemp(ipDir, ".tmp.upload.")
	if err != nil {
		log.Printf("Upload single file: clientIP=%s path=%s create temp file error: %v", clientIP, path, err)
		http.Error(w, "Failed to create temp file", http.StatusInternalServerError)
		return
	}
	tmpPath := destFile.Name()
	defer destFile.Close()
	defer os.Remove(tmpPath) // удалим при ошибке; при успехе переименуем

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(destFile, hasher), file); err != nil {
		log.Printf("Upload single file: clientIP=%s path=%s copy/save error: %v", clientIP, path, err)
		http.Error(w, "Failed to read/write file", http.StatusInternalServerError)
		return
	}
	if err := destFile.Sync(); err != nil {
		log.Printf("Upload single file: clientIP=%s path=%s sync error: %v", clientIP, path, err)
		http.Error(w, "Failed to sync file", http.StatusInternalServerError)
		return
	}
	destFile.Close()

	fileHash := fmt.Sprintf("%x", hasher.Sum(nil))
	destPath := filepath.Join(ipDir, fileHash)
	if err := os.Rename(tmpPath, destPath); err != nil {
		log.Printf("Upload single file: clientIP=%s path=%s rename error: %v", clientIP, path, err)
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	statInfo, err := os.Stat(destPath)
	if err != nil {
		log.Printf("Upload single file: clientIP=%s path=%s stat error: %v", clientIP, path, err)
		http.Error(w, "Failed to stat file", http.StatusInternalServerError)
		return
	}
	fileSize := statInfo.Size()

	// Имя файла — как на клиенте: последний компонент пути (логическое имя) или из заголовка multipart
	fileName := ""
	if filePath != "" && filePath != "/" {
		fileName = filepath.Base(filePath)
	}
	if fileName == "" {
		fileName = header.Filename
	}
	if fileName == "" {
		fileName = "file"
	}

	fileInfo := FileInfo{
		IP:       clientIP,
		Filename: fileName,
		FilePath: filePath,
		FileHash: fileHash,
		Size:     fileSize,
		Uploaded: time.Now(),
		MimeType: header.Header.Get("Content-Type"),
	}
	if err := s.saveFileInfo(fileInfo); err != nil {
		log.Printf("Upload single file: clientIP=%s path=%s saveFileInfo error: %v", clientIP, path, err)
		http.Error(w, "Failed to save file info", http.StatusInternalServerError)
		return
	}

	log.Printf("Upload single file completed: clientIP=%s path=%s fileHash=%s size=%d", clientIP, path, fileHash, fileSize)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(UploadFolderResponse{
		OK:       true,
		Message:  "File uploaded successfully",
		FileHash: fileHash,
		FilePath: filePath,
		Filename: fileName,
		Size:     fileSize,
	})
}

// uploadFolderPartHandler обрабатывает одну часть при поточной загрузке большого файла (API folder).
// Заголовки: X-Upload-Id, X-Part-Index, X-Total-Parts; опционально X-Filename. Тело — сырые байты части.
func (s *Store) uploadFolderPartHandler(w http.ResponseWriter, r *http.Request, clientIP, path, filePath, uploadID, partIndexStr, totalPartsStr string) {
	filename := r.Header.Get("X-Filename")
	if filename == "" {
		filename = filepath.Base(path)
	}
	if filename == "" {
		filename = "file"
	}
	s.uploadChunkedPartHandler(w, r, clientIP, uploadID, partIndexStr, totalPartsStr, path, filePath, filename, "uploadFolder")
}

// uploadChunkedPartHandler — общая логика приёма одной части поточной загрузки (folder и upload).
// pathMatch — ключ совпадения сессии (для folder = path из URL, для upload = X-Filepath); filePath и filename — для FileInfo.
// apiName — для логов ("uploadFolder" или "upload").
func (s *Store) uploadChunkedPartHandler(w http.ResponseWriter, r *http.Request, clientIP, uploadID, partIndexStr, totalPartsStr, pathMatch, filePath, filename, apiName string) {
	partIndex, err := strconv.Atoi(partIndexStr)
	if err != nil || partIndex < 0 {
		log.Printf("[%s] upload part: invalid X-Part-Index: %q", apiName, partIndexStr)
		http.Error(w, "Invalid X-Part-Index", http.StatusBadRequest)
		return
	}
	totalParts, err := strconv.Atoi(totalPartsStr)
	if err != nil || totalParts < 1 || partIndex >= totalParts {
		log.Printf("[%s] upload part: invalid X-Total-Parts=%q or X-Part-Index=%q", apiName, totalPartsStr, partIndexStr)
		http.Error(w, "Invalid X-Total-Parts or X-Part-Index", http.StatusBadRequest)
		return
	}

	ipDir := filepath.Join(s.secretsDir, clientIP)
	if err := os.MkdirAll(ipDir, 0755); err != nil {
		log.Printf("[%s] upload part: failed to create directory %s: %v", apiName, ipDir, err)
		http.Error(w, "Failed to create directory", http.StatusInternalServerError)
		return
	}

	uploadsDir := filepath.Join(ipDir, ".uploads")
	partDir := filepath.Join(uploadsDir, uploadID)

	s.uploadsMu.Lock()
	state, exists := s.uploads[uploadID]
	if !exists {
		if err := os.MkdirAll(partDir, 0755); err != nil {
			s.uploadsMu.Unlock()
			log.Printf("[%s] upload part: failed to create upload dir %s: %v", apiName, partDir, err)
			http.Error(w, "Failed to create upload directory", http.StatusInternalServerError)
			return
		}
		state = &chunkedUploadState{
			clientIP:   clientIP,
			path:       pathMatch,
			filePath:   filePath,
			filename:   filename,
			totalParts: totalParts,
			dir:        partDir,
			received:   make([]bool, totalParts),
		}
		s.uploads[uploadID] = state
	} else {
		if state.clientIP != clientIP || state.path != pathMatch {
			s.uploadsMu.Unlock()
			log.Printf("[%s] upload part: upload ID mismatch (clientIP or path)", apiName)
			http.Error(w, "Upload ID mismatch", http.StatusBadRequest)
			return
		}
	}
	s.uploadsMu.Unlock()

	partPath := filepath.Join(partDir, fmt.Sprintf("part_%d", partIndex))
	partFile, err := os.Create(partPath)
	if err != nil {
		log.Printf("[%s] upload part: failed to create part file %s: %v", apiName, partPath, err)
		http.Error(w, "Failed to create part file", http.StatusInternalServerError)
		return
	}
	_, err = io.Copy(partFile, r.Body)
	partFile.Close()
	if err != nil {
		os.Remove(partPath)
		log.Printf("[%s] upload part: failed to write part %d: %v", apiName, partIndex, err)
		http.Error(w, "Failed to write part", http.StatusInternalServerError)
		return
	}

	s.uploadsMu.Lock()
	state.received[partIndex] = true
	allReceived := true
	for i := 0; i < state.totalParts; i++ {
		if !state.received[i] {
			allReceived = false
			break
		}
	}
	if !allReceived {
		s.uploadsMu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "part": partIndex})
		return
	}
	delete(s.uploads, uploadID)
	s.uploadsMu.Unlock()

	// Собираем части в один файл, считаем хеш
	destFile, err := os.CreateTemp(ipDir, ".tmp.upload.")
	if err != nil {
		os.RemoveAll(partDir)
		log.Printf("[%s] upload part: failed to create temp file in %s: %v", apiName, ipDir, err)
		http.Error(w, "Failed to create temp file", http.StatusInternalServerError)
		return
	}
	tmpPath := destFile.Name()
	defer destFile.Close()
	defer os.Remove(tmpPath)

	hasher := sha256.New()
	for i := 0; i < state.totalParts; i++ {
		p := filepath.Join(partDir, fmt.Sprintf("part_%d", i))
		f, err := os.Open(p)
		if err != nil {
			os.RemoveAll(partDir)
			log.Printf("[%s] upload part: failed to read part %d: %v", apiName, i, err)
			http.Error(w, "Failed to read part", http.StatusInternalServerError)
			return
		}
		_, err = io.Copy(io.MultiWriter(destFile, hasher), f)
		f.Close()
		if err != nil {
			os.RemoveAll(partDir)
			log.Printf("[%s] upload part: failed to assemble parts: %v", apiName, err)
			http.Error(w, "Failed to assemble parts", http.StatusInternalServerError)
			return
		}
	}
	if err := destFile.Sync(); err != nil {
		os.RemoveAll(partDir)
		log.Printf("[%s] upload part: failed to sync temp file: %v", apiName, err)
		http.Error(w, "Failed to sync file", http.StatusInternalServerError)
		return
	}
	destFile.Close()

	os.RemoveAll(partDir)

	fileHash := fmt.Sprintf("%x", hasher.Sum(nil))
	destPath := filepath.Join(ipDir, fileHash)
	if err := os.Rename(tmpPath, destPath); err != nil {
		log.Printf("[%s] upload part: failed to rename temp to %s: %v", apiName, destPath, err)
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	statInfo, err := os.Stat(destPath)
	if err != nil {
		log.Printf("[%s] upload part: failed to stat %s: %v", apiName, destPath, err)
		http.Error(w, "Failed to stat file", http.StatusInternalServerError)
		return
	}
	fileSize := statInfo.Size()

	fileInfo := FileInfo{
		IP:       clientIP,
		Filename: state.filename,
		FilePath: state.filePath,
		FileHash: fileHash,
		Size:     fileSize,
		Uploaded: time.Now(),
		MimeType: "",
	}
	if err := s.saveFileInfo(fileInfo); err != nil {
		log.Printf("[%s] upload part: failed to save file info: %v", apiName, err)
		http.Error(w, "Failed to save file info", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(UploadFolderResponse{
		OK:       true,
		Message:  "File uploaded successfully",
		FileHash: fileHash,
		FilePath: state.filePath,
		Filename: state.filename,
		Size:     fileSize,
	})
}

// ListHandler работает как статический файловый сервер для директории secrets
func (s *Store) ListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем пароль
	password := r.URL.Query().Get("password")
	if password != s.password {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Извлекаем путь к файлу из URL
	path := strings.TrimPrefix(r.URL.Path, "/api/secrets/list/")

	path = strings.TrimPrefix(path, "/api/secrets/list")

	// Если путь пустой, показываем список всех IP директорий
	if path == "" {
		// Получаем все уникальные IP из базы данных
		fileInfos, err := s.loadAllFileInfos()
		if err != nil {
			http.Error(w, "Failed to load file info", http.StatusInternalServerError)
			return
		}

		// Собираем уникальные IP
		ipSet := make(map[string]bool)
		for _, fileInfo := range fileInfos {
			ipSet[fileInfo.IP] = true
		}

		var ips []string
		for ip := range ipSet {
			ips = append(ips, ip)
		}

		// Формируем HTML страницу со списком IP
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := s.generateIPListHTML(ips, password)
		w.Write([]byte(html))
		return
	}

	// Разбираем путь: {ip}/{path}
	pathParts := strings.SplitN(path, "/", 2)
	if len(pathParts) < 1 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	ip := pathParts[0]
	var filePath string
	if len(pathParts) > 1 {
		filePath = "/" + pathParts[1]
	} else {
		filePath = "/"
	}

	// Получаем иерархию для текущего пути
	pathItems, err := s.getPathHierarchy(ip, filePath)
	if err != nil {
		http.Error(w, "Failed to load file info", http.StatusInternalServerError)
		return
	}

	// Проверяем, есть ли файл с точно таким же путем
	var exactFile *FileInfo
	for _, item := range pathItems {
		if item.IsFile && item.Path == filePath {
			exactFile = item.FileInfo
			break
		}
	}

	// Если найден точный файл, отдаём его
	if exactFile != nil {
		actualFilePath := filepath.Join(s.secretsDir, exactFile.IP, exactFile.FileHash)

		// Проверяем существование файла
		if _, err := os.Stat(actualFilePath); os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}

		// Устанавливаем заголовки
		w.Header().Set("Content-Type", exactFile.MimeType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", exactFile.Filename))

		// Отдаем файл
		http.ServeFile(w, r, actualFilePath)
		return
	}

	// Иначе показываем содержимое директории
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := s.generateHierarchyHTML(ip, filePath, pathItems, password)
	w.Write([]byte(html))
}

// FileHandler работает как статический файловый сервер для получения файлов по хешу
func (s *Store) FileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем пароль
	password := r.URL.Query().Get("password")
	if password != s.password {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Извлекаем хеш файла из URL
	path := strings.TrimPrefix(r.URL.Path, "/api/secrets/files/")
	if path == "" {
		http.Error(w, "File hash not specified", http.StatusBadRequest)
		return
	}

	// Ищем файл по хешу во всех IP директориях
	fileInfo, filePath, err := s.findFileByHash(path)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Проверяем существование файла
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Устанавливаем заголовки
	w.Header().Set("Content-Type", fileInfo.MimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileInfo.Filename))

	// Отдаем файл
	http.ServeFile(w, r, filePath)
}

// getClientIP извлекает IP адрес клиента
func (s *Store) getClientIP(r *http.Request) string {
	// Проверяем различные заголовки для получения реального IP
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// X-Forwarded-For может содержать несколько IP через запятую
		ips := strings.Split(ip, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Убираем порт из RemoteAddr если он есть
	remoteAddr := r.RemoteAddr
	if colonIndex := strings.LastIndex(remoteAddr, ":"); colonIndex != -1 {
		remoteAddr = remoteAddr[:colonIndex]
	}
	return remoteAddr
}

// saveFileInfo сохраняет информацию о файле в JSONL файл
func (s *Store) saveFileInfo(fileInfo FileInfo) error {
	data, err := json.Marshal(fileInfo)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(s.filesDB, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(append(data, '\n'))
	return err
}

// loadAllFileInfos загружает все записи из JSONL файла
func (s *Store) loadAllFileInfos() ([]FileInfo, error) {
	file, err := os.Open(s.filesDB)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileInfo{}, nil
		}
		return nil, err
	}
	defer file.Close()

	var fileInfos []FileInfo
	decoder := json.NewDecoder(file)
	for decoder.More() {
		var fileInfo FileInfo
		if err := decoder.Decode(&fileInfo); err != nil {
			return nil, err
		}
		fileInfos = append(fileInfos, fileInfo)
	}

	return fileInfos, nil
}

// findFileByHash ищет файл по хешу во всех IP директориях
func (s *Store) findFileByHash(fileHash string) (*FileInfo, string, error) {
	fileInfos, err := s.loadAllFileInfos()
	if err != nil {
		return nil, "", err
	}

	for _, fileInfo := range fileInfos {
		if fileInfo.FileHash == fileHash {
			filePath := filepath.Join(s.secretsDir, fileInfo.IP, fileInfo.FileHash)

			// Проверяем физическое существование файла
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				continue // Пропускаем файлы, которых физически нет
			}

			return &fileInfo, filePath, nil
		}
	}

	return nil, "", fmt.Errorf("file not found")
}

// loadFileInfosByIP загружает файлы для конкретного IP
func (s *Store) loadFileInfosByIP(ip string) ([]FileInfo, error) {
	fileInfos, err := s.loadAllFileInfos()
	if err != nil {
		return nil, err
	}

	var result []FileInfo
	for _, fileInfo := range fileInfos {
		if fileInfo.IP == ip {
			result = append(result, fileInfo)
		}
	}

	return result, nil
}

// getPathHierarchy возвращает иерархию папок и файлов для указанного IP и пути
func (s *Store) getPathHierarchy(ip, currentPath string) ([]PathItem, error) {
	fileInfos, err := s.loadFileInfosByIP(ip)
	if err != nil {
		return nil, err
	}

	// Нормализуем текущий путь
	if currentPath == "" {
		currentPath = "/"
	}
	// Не добавляем / в конец, чтобы корректно обрабатывать случаи когда currentPath == filePath

	// Собираем все уникальные пути
	pathMap := make(map[string]*PathItem)

	for _, fileInfo := range fileInfos {
		// Проверяем, что физический файл существует
		actualFilePath := filepath.Join(s.secretsDir, fileInfo.IP, fileInfo.FileHash)
		if _, err := os.Stat(actualFilePath); os.IsNotExist(err) {
			continue // Пропускаем файлы, которых физически нет
		}

		// Нормализуем путь к файлу из базы данных
		filePath := filepath.Clean(fileInfo.FilePath)
		if !strings.HasPrefix(filePath, "/") {
			filePath = "/" + filePath
		}

		// Получаем относительный путь от текущей директории
		relativePath := s.getRelativePath(currentPath, filePath)
		if relativePath == "" {
			continue
		}

		// Проверяем, является ли currentPath самим файлом
		if currentPath == filePath {
			// Если мы находимся в самом файле, добавляем его в список
			fileName := filepath.Base(filePath)
			if pathMap[filePath] == nil {
				pathMap[filePath] = &PathItem{
					Name:     fileName,
					Path:     filePath,
					IsFile:   true,
					FileInfo: &fileInfo,
				}
			}
			continue
		}

		// Разбираем относительный путь на компоненты
		parts := strings.Split(strings.Trim(relativePath, "/"), "/")
		if len(parts) == 0 {
			continue
		}

		// Первый компонент - это папка или файл в текущей директории
		firstPart := parts[0]

		// Формируем полный путь, учитывая что currentPath может заканчиваться на /
		var fullPath string
		if strings.HasSuffix(currentPath, "/") {
			fullPath = currentPath + firstPart
		} else {
			fullPath = currentPath + "/" + firstPart
		}

		if pathMap[fullPath] == nil {
			pathMap[fullPath] = &PathItem{
				Name:   firstPart,
				Path:   fullPath,
				IsFile: len(parts) == 1, // Если только один компонент, это файл
			}
		}

		// Если это файл, добавляем информацию о файле
		if len(parts) == 1 {
			pathMap[fullPath].FileInfo = &fileInfo
		}
	}

	// Преобразуем map в slice и сортируем
	var result []PathItem
	for _, item := range pathMap {
		result = append(result, *item)
	}

	// Сортируем: сначала папки, потом файлы, по алфавиту
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsFile != result[j].IsFile {
			return !result[i].IsFile // Папки сначала
		}
		return result[i].Name < result[j].Name
	})

	return result, nil
}

// getRelativePath возвращает относительный путь от basePath к targetPath
func (s *Store) getRelativePath(basePath, targetPath string) string {
	// Нормализуем пути
	basePath = filepath.Clean(basePath)
	targetPath = filepath.Clean(targetPath)

	// Убеждаемся, что пути начинаются с /
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	if !strings.HasPrefix(targetPath, "/") {
		targetPath = "/" + targetPath
	}

	// Если пути идентичны, возвращаем имя файла/папки
	if basePath == targetPath {
		parts := strings.Split(strings.Trim(targetPath, "/"), "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
		return ""
	}

	// Если targetPath не начинается с basePath, возвращаем пустую строку
	if !strings.HasPrefix(targetPath, basePath) {
		return ""
	}

	// Убираем basePath из targetPath
	relative := strings.TrimPrefix(targetPath, basePath)
	return strings.TrimPrefix(relative, "/")
}

// getBreadcrumbPath возвращает путь для хлебных крошек
func (s *Store) getBreadcrumbPath(path string) []PathItem {
	if path == "/" {
		return []PathItem{{Name: "Корень", Path: "/", IsFile: false}}
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	var breadcrumbs []PathItem

	// Добавляем корень
	breadcrumbs = append(breadcrumbs, PathItem{Name: "Корень", Path: "/", IsFile: false})

	// Строим путь по частям
	currentPath := ""
	for i, part := range parts {
		if i == 0 {
			currentPath = "/" + part
		} else {
			currentPath += "/" + part
		}
		breadcrumbs = append(breadcrumbs, PathItem{Name: part, Path: currentPath, IsFile: false})
	}

	return breadcrumbs
}

// generateIPListHTML генерирует HTML страницу со списком IP директорий
func (s *Store) generateIPListHTML(ips []string, password string) string {
	html := `<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>Файловый менеджер - IP директории</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; background-color: #f5f5f5; }
        .container { max-width: 800px; margin: 0 auto; background: white; padding: 20px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        h1 { color: #333; border-bottom: 2px solid #007bff; padding-bottom: 10px; }
        .ip-list { list-style: none; padding: 0; }
        .ip-item { padding: 12px; border-bottom: 1px solid #eee; display: flex; align-items: center; }
        .ip-item:hover { background-color: #f8f9fa; }
        .ip-icon { margin-right: 15px; font-size: 20px; color: #28a745; }
        .ip-link { font-weight: bold; color: #007bff; text-decoration: none; }
        .ip-link:hover { text-decoration: underline; }
        .empty { text-align: center; color: #6c757d; font-style: italic; padding: 40px; }
    </style>
</head>
<body>
    <div class="container">
        <h1>🌐 IP директории</h1>
        <ul class="ip-list">`

	if len(ips) == 0 {
		html += `<li class="empty">Нет загруженных файлов</li>`
	} else {
		for _, ip := range ips {
			html += fmt.Sprintf(`
            <li class="ip-item">
                <span class="ip-icon">🌐</span>
                <a href="/api/secrets/list/%s?password=%s" class="ip-link">%s</a>
            </li>`, ip, password, ip)
		}
	}

	html += `
        </ul>
    </div>
</body>
</html>`

	return html
}

// generateHierarchyHTML генерирует HTML страницу с иерархией папок и файлов
func (s *Store) generateHierarchyHTML(ip, currentPath string, pathItems []PathItem, password string) string {
	breadcrumbs := s.getBreadcrumbPath(currentPath)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>Файловый менеджер - %s</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; background-color: #f5f5f5; }
        .container { max-width: 800px; margin: 0 auto; background: white; padding: 20px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        h1 { color: #333; border-bottom: 2px solid #007bff; padding-bottom: 10px; }
        .breadcrumb { background: #f8f9fa; padding: 10px; border-radius: 5px; margin-bottom: 20px; }
        .breadcrumb a { color: #007bff; text-decoration: none; }
        .breadcrumb a:hover { text-decoration: underline; }
        .breadcrumb span { color: #6c757d; }
        .back-btn { display: inline-block; background: #6c757d; color: white; padding: 8px 16px; text-decoration: none; border-radius: 4px; margin-bottom: 15px; }
        .back-btn:hover { background: #5a6268; }
        .file-list { list-style: none; padding: 0; }
        .file-item { padding: 12px; border-bottom: 1px solid #eee; display: flex; align-items: center; }
        .file-item:hover { background-color: #f8f9fa; }
        .file-icon { margin-right: 15px; font-size: 20px; }
        .file-info { flex-grow: 1; }
        .file-name { font-weight: bold; color: #007bff; text-decoration: none; }
        .file-name:hover { text-decoration: underline; }
        .file-size { color: #6c757d; font-size: 0.9em; margin-left: 10px; }
        .file-date { color: #6c757d; font-size: 0.9em; }
        .folder { color: #28a745; }
        .file { color: #007bff; }
        .empty { text-align: center; color: #6c757d; font-style: italic; padding: 40px; }
    </style>
</head>
<body>
    <div class="container">
        <h1>📁 Файлы IP: %s</h1>
        
        <div class="breadcrumb">
            <strong>Путь:</strong> `, ip, ip)

	// Добавляем хлебные крошки
	for i, crumb := range breadcrumbs {
		if i > 0 {
			html += ` <span>/</span> `
		}
		if i == len(breadcrumbs)-1 {
			html += fmt.Sprintf(`<span>%s</span>`, crumb.Name)
		} else {
			html += fmt.Sprintf(`<a href="/api/secrets/list/%s%s?password=%s">%s</a>`, ip, crumb.Path, password, crumb.Name)
		}
	}

	html += fmt.Sprintf(`
        </div>
        
        <a href="/api/secrets/list/?password=%s" class="back-btn">⬅️ Назад к IP</a>
        
        <ul class="file-list">`, password)

	if len(pathItems) == 0 {
		html += `<li class="empty">Папка пуста</li>`
	} else {
		for _, item := range pathItems {
			if item.IsFile {
				// Это файл
				fileInfo := item.FileInfo
				html += fmt.Sprintf(`
            <li class="file-item">
                <span class="file-icon file">📄</span>
                <div class="file-info">
                    <a href="/api/secrets/list/%s%s?password=%s" class="file-name">%s</a>
                    <span class="file-size">%s</span>
                    <span class="file-date">%s</span>
                </div>
            </li>`, ip, item.Path, password, item.Name, s.formatFileSize(fileInfo.Size), fileInfo.Uploaded.Format("02.01.2006 15:04"))
			} else {
				// Это папка
				html += fmt.Sprintf(`
            <li class="file-item">
                <span class="file-icon folder">📁</span>
                <div class="file-info">
                    <a href="/api/secrets/list/%s%s?password=%s" class="file-name">%s/</a>
                </div>
            </li>`, ip, item.Path, password, item.Name)
			}
		}
	}

	html += `
        </ul>
    </div>
</body>
</html>`

	return html
}

// formatFileSize форматирует размер файла в читаемый вид
func (s *Store) formatFileSize(size int64) string {
	if size == 0 {
		return "0 B"
	}

	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	} else if size < unit*unit {
		return fmt.Sprintf("%.1f KB", float64(size)/unit)
	} else if size < unit*unit*unit {
		return fmt.Sprintf("%.1f MB", float64(size)/(unit*unit))
	} else {
		return fmt.Sprintf("%.1f GB", float64(size)/(unit*unit*unit))
	}
}
