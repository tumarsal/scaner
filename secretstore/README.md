# Хранилище секретов (Secret Store)

Хранилище секретов предоставляет безопасный способ загрузки и хранения файлов с метаинформацией.

## Возможности

- Загрузка файлов через HTTP POST
- Загрузка текста как файл с произвольным именем
- Автоматическое определение IP клиента
- Хранение файлов по хешу SHA256
- Организация файлов по IP адресам
- Метаинформация в формате JSONL
- Защищенный доступ к списку файлов по паролю
- Статический файловый сервер для скачивания файлов
- Очередь загрузки с автоматическими повторами
- Проверка интернет-соединения перед загрузкой
- Многопоточная обработка загрузок

## Структура хранения

```
secrets/
├── files.jsonl                    # Метаинформация о файлах
├── 192.168.1.100/                 # Директория для IP
│   ├── a1b2c3d4...               # Файл по хешу
│   └── e5f6g7h8...               # Другой файл
└── 192.168.1.101/                 # Другой IP
    └── i9j0k1l2...               # Файл
```

## API Endpoints

### POST /api/secrets/upload
Загружает файл в хранилище.

**Параметры:**
- `file` - файл для загрузки (multipart/form-data)

**Ответ:**
```
File uploaded successfully. Hash: a1b2c3d4...
```

### GET /api/secrets/list?password=secret123
Получает список всех файлов.

**Параметры:**
- `password` - пароль для доступа

**Ответ:**
```json
{
  "files": [
    {
      "ip": "192.168.1.100",
      "filename": "document.txt",
      "filehash": "a1b2c3d4...",
      "size": 1024,
      "uploaded": "2024-01-01T12:00:00Z",
      "mime_type": "text/plain"
    }
  ],
  "count": 1
}
```

### GET /api/secrets/files/{hash}?password=secret123
Работает как статический файловый сервер - скачивает файл по хешу.

**Параметры:**
- `hash` - хеш файла в URL пути
- `password` - пароль для доступа

**Ответ:**
Возвращает файл с правильными заголовками Content-Type и Content-Disposition.

## Использование

### Сервер
Хранилище автоматически инициализируется в RTTY сервере:

```go
// В cmd/rttyserver.go
server.secretStore = secretstore.NewStore("secrets", "secret123")
server.secretStore.Init()
```

### Клиент
```go
client := secretstore.NewClient("http://localhost:5912")
defer client.Close() // Важно закрыть клиент

// Загрузка файла (добавляется в очередь)
err := client.UploadFile("/path/to/file.txt")

// Загрузка текста как файл с указанным именем
err := client.UploadText("Это текстовое содержимое", "my_document.txt")

// Получение списка файлов
files, err := client.ListFiles("secret123")

// Скачивание файла по хешу
err := client.DownloadFile("a1b2c3d4...", "secret123", "/path/to/output.txt")

// Проверка размера очереди
queueSize := client.GetQueueSize()

// Ожидание завершения всех загрузок
client.WaitForQueueEmpty()
```

## Безопасность

- Файлы сохраняются по хешу SHA256
- Доступ к списку файлов защищен паролем
- IP адреса извлекаются из заголовков X-Real-IP, X-Forwarded-For
- Файлы организуются по IP адресам для изоляции

## Примеры использования curl

### Загрузка файла:
```bash
curl -X POST -F "file=@/path/to/file.txt" http://localhost:5912/api/secrets/upload
```

### Получение списка файлов:
```bash
curl "http://localhost:5912/api/secrets/list?password=secret123"
```

### Скачивание файла по хешу:
```bash
curl "http://localhost:5912/api/secrets/files/a1b2c3d4...?password=secret123" -o downloaded_file.txt
```

## Тестирование

Запуск тестов:
```bash
go test ./secretstore -v
```
