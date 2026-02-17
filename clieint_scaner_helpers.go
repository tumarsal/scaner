package scaner

import (
	"os"
	"path/filepath"
	"strings"
)

// shouldSkipDirectory проверяет, нужно ли пропускать указанную директорию
// Возвращает true для папок, которые являются результатом компиляции, кэша или зависимостей
func shouldSkipDirectory(dirName string) bool {
	// Список папок, которые нужно пропускать
	skipDirs := map[string]bool{
		// Node.js
		"node_modules":  true,
		"npm-debug.log": true,
		".npm":          true,
		".docker":       true,

		// Python
		"__pycache__":   true,
		".pytest_cache": true,
		".venv":         true,
		"venv":          true,
		"env":           true,
		"site-packages": true,

		// Java
		"target":  true,
		"build":   true,
		".gradle": true,
		"bin":     true,
		"out":     true,
		"classes": true,

		// Go
		"vendor": true,
		"pkg":    true,

		// C/C++
		"obj":                 true,
		"Debug":               true,
		"Release":             true,
		"cmake-build-debug":   true,
		"cmake-build-release": true,

		// Rust
		"Cargo.lock": true,

		// .NET
		"packages": true,

		// Ruby
		"tmp": true,
		"log": true,

		// PHP
		"composer.lock": true,

		// Системные папки
		".git":      true,
		".svn":      true,
		".hg":       true,
		".DS_Store": true,
		"Thumbs.db": true,

		// macOS системные папки
		"Library":      true,
		"System":       true,
		"Applications": true,
		"Users":        true,
		"Volumes":      true,
		"private":      true,
		"var":          true,
		"etc":          true,
		"usr":          true,
		"opt":          true,
		"dev":          true,
		"home":         true,
		"root":         true,

		// Кэш и временные файлы
		".cache": true,
		"cache":  true,
		"temp":   true,
		".tmp":   true,
		".temp":  true,

		// IDE и редакторы
		".cargo":    true,
		".idea":     true,
		".vscode":   true,
		".vs":       true,
		".eclipse":  true,
		".metadata": true,

		// Логи и отладочная информация
		"logs":   true,
		"debug":  true,
		".debug": true,

		// Другие
		"src":         true,
		".konan":      true,
		"dist":        true,
		"coverage":    true,
		".nyc_output": true,
		".coverage":   true,
	}

	return skipDirs[dirName]
}

// isSystemPath проверяет, является ли путь системным (требует особых разрешений)
func isSystemPath(fullPath string) bool {
	// Системные пути macOS, которые часто требуют особых разрешений
	systemPaths := []string{
		"/System",
		"/Library",
		"/Applications",
		"/Volumes",
		"/private",
		"/var",
		"/etc",
		"/usr",
		"/bin",
		"/sbin",
		"/opt",
		"/dev",
		"/home",
		"/root",
		"/tmp",
		"/tmp/",
		"/Desktop",
		"/Desktop/",
		"/Pictures",
		"/Pictures/",
		"/Music",
		"/Music/",
		"/Movies",
		"/Movies/",
	}

	// Нормализуем путь для сравнения
	normalizedPath := strings.ToLower(strings.ReplaceAll(fullPath, "\\", "/"))

	// Проверяем каждый системный путь
	for _, systemPath := range systemPaths {
		if strings.HasPrefix(normalizedPath, strings.ToLower(systemPath)) {
			return true
		}
	}

	return false
}

// shouldSkipPath проверяет, нужно ли пропускать путь на основе его содержимого
// Возвращает true для путей, которые содержат паттерны, указывающие на отсутствие интересного контента
func shouldSkipPath(fullPath string) bool {
	// Паттерны путей, которые указывают на отсутствие интересного контента
	skipPathPatterns := []string{
		// Android
		"app/src/main",
		"app/test",
		"app/src/test",
		"app/build",
		"apk",
		".cursor",
		".dotnet",
		".rustup",
		".m2",
		".gradle",
		".idea",
		"LocalPackages",
		"Modules",
		"app/lib",
		".xcframework/Headers",

		// just skip
		"patches",
		"templates",
		"tests",

		// iOS
		"ios/build",
		"ios/DerivedData",
		"ios/Pods",
		".xcassets/",

		// Java/Maven
		"src/main/java",
		"src/main/resources",
		"src/test/java",
		"src/test/resources",

		// Gradle
		"src/main/kotlin",
		"src/test/kotlin",

		// Python
		"src/main",
		"src/tests",
		"tests/unit",
		"tests/integration",

		// Node.js
		"src/components",
		"src/pages",
		"src/utils",
		"src/services",
		"public/assets",
		"public/images",

		// React/Vue/Angular
		"src/app",
		"src/views",
		"src/components",
		"assets/images",
		"assets/fonts",
		"static/css",
		"static/js",
		"static/img",
		"static/fonts",
		"static/videos",
		"static/audio",
		"static/docs",
		"static/other",
		"js/vendor",
		"js/plugins",
		"js/libs",
		"js/utils",
		"js/services",
		"js/components",
		"js/pages",

		// Go
		"cmd",
		"internal",
		"pkg",
		"api",
		"web",
		"sdk/go1",

		// C/C++
		"src/main",
		"src/lib",
		"include",
		"lib",

		// Rust
		"src/bin",
		"src/lib",
		"examples",

		// .NET
		"src/Controllers",
		"src/Models",
		"src/Views",
		"wwwroot",

		// Ruby/Rails
		"app/controllers",
		"app/models",
		"app/views",
		"app/assets",
		"config",
		"db",

		// PHP
		"src/Controller",
		"src/Model",
		"src/View",
		"public/assets",
		"config",
		"database",

		// Системные и служебные
		"system",
		"runtime",
		"temp",
		"tmp",
		"cache",
		"logs",
		"data",
		"storage",
		"uploads",
		"downloads",
		"backup",
		"archive",
		// "go/src/github.com",

		// next
		".next",
	}

	// Нормализуем путь для сравнения
	normalizedPath := strings.ToLower(strings.ReplaceAll(fullPath, "\\", "/"))

	// Проверяем каждый паттерн
	for _, pattern := range skipPathPatterns {
		if strings.Contains(normalizedPath, strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

// isSshKeyFile проверяет, является ли файл SSH ключом
func isSshKeyFile(fileName string) bool {
	// Список возможных имен SSH ключей
	sshKeyNames := map[string]bool{
		"id_rsa":          true,
		"id_rsa.pub":      true,
		"id_dsa":          true,
		"id_dsa.pub":      true,
		"id_ecdsa":        true,
		"id_ecdsa.pub":    true,
		"id_ed25519":      true,
		"id_ed25519.pub":  true,
		"id_xmss":         true,
		"id_xmss.pub":     true,
		"authorized_keys": true,
		"known_hosts":     true,
	}

	return sshKeyNames[fileName]
}

// isEnvFile проверяет, является ли файл .env файлом
func isEnvFile(fileName string) bool {
	// Список возможных имен .env файлов
	envFileNames := map[string]bool{
		".env":             true,
		".env.local":       true,
		".env.development": true,
		".env.production":  true,
		".env.test":        true,
		".env.staging":     true,
		"env":              true,
		"environment":      true,
	}

	return envFileNames[fileName]
}

// isConfigFile проверяет, является ли файл конфигурационным файлом с потенциально чувствительными данными
func isConfigFile(fileName string) bool {
	// Список файлов, которые могут содержать токены, ключи и другие чувствительные данные
	configFileNames := map[string]bool{
		// .NET - файлы с настройками приложений
		"appsettings.json":             true,
		"appsettings.Development.json": true,
		"appsettings.Production.json":  true,
		"appsettings.Test.json":        true,
		"web.config":                   true,
		"app.config":                   true,

		// Java - файлы с настройками приложений
		"application.properties": true,
		"application.yml":        true,
		"application.yaml":       true,

		// Python - файлы с настройками
		"config.py":   true,
		"settings.py": true,

		// Ruby - файлы с настройками
		"config.ru": true,

		// PHP - файлы с настройками
		"config.php": true,

		// Docker - файлы с переменными окружения
		"docker-compose.yaml": true,

		// Terraform - файлы с переменными
		"variables.tf":     true,
		"terraform.tfvars": true,

		// Ansible - файлы с переменными
		"inventory":    true,
		"playbook.yml": true,

		// Общие конфигурационные файлы
		"config.json":        true,
		"config.yml":         true,
		"config.yaml":        true,
		"config.toml":        true,
		"config.ini":         true,
		"settings.json":      true,
		"settings.yml":       true,
		"settings.yaml":      true,
		"configuration.json": true,
		"configuration.yml":  true,
		"configuration.yaml": true,
	}

	return configFileNames[fileName]
}

// isWalletFile проверяет, является ли файл файлом электронного кошелька
func isWalletFile(fileName string) bool {
	// Список файлов, которые могут быть файлами электронных кошельков
	walletFileNames := map[string]bool{
		// Bitcoin Core
		"wallet.dat":           true,
		"wallet.dat.bak":       true,
		"wallet.dat.old":       true,
		"wallet.dat.reserve.1": true,
		"wallet.dat.reserve.2": true,

		// Ethereum и другие EVM кошельки
		"keystore":      true,
		"keystore.json": true,
		"UTC--":         true, // Ethereum keystore files
		"geth":          true,
		"geth.ipc":      true,

		// MetaMask и другие браузерные кошельки
		"Local Extension Settings": true,
		"Secure Preferences":       true,
		"Preferences":              true,

		// Electrum
		"electrum":            true,
		"electrum.dat":        true,
		"electrum.log":        true,
		"electrum.conf":       true,
		"electrum_wallet":     true,
		"electrum_wallet.dat": true,

		// Exodus
		"exodus":      true,
		"exodus.conf": true,
		"exodus.log":  true,

		// Trust Wallet
		"trust":      true,
		"trust.conf": true,
		"trust.log":  true,

		// Atomic Wallet
		"atomic":      true,
		"atomic.conf": true,
		"atomic.log":  true,

		// Binance Wallet
		"binance":      true,
		"binance.conf": true,
		"binance.log":  true,

		// Coinbase Wallet
		"coinbase":      true,
		"coinbase.conf": true,
		"coinbase.log":  true,

		// Ledger Live
		"ledger":      true,
		"ledger.conf": true,
		"ledger.log":  true,

		// Trezor
		"trezor":      true,
		"trezor.conf": true,
		"trezor.log":  true,

		// Phantom Wallet
		"phantom":      true,
		"phantom.conf": true,
		"phantom.log":  true,

		// Solflare
		"solflare":      true,
		"solflare.conf": true,
		"solflare.log":  true,

		// MyEtherWallet
		"myetherwallet": true,
		"mew":           true,
		"mew.conf":      true,
		"mew.log":       true,

		// Jaxx Liberty
		"jaxx":      true,
		"jaxx.conf": true,
		"jaxx.log":  true,

		// Guarda Wallet
		"guarda":      true,
		"guarda.conf": true,
		"guarda.log":  true,

		// Freewallet
		"freewallet":      true,
		"freewallet.conf": true,
		"freewallet.log":  true,

		// Edge Wallet
		"edge":      true,
		"edge.conf": true,
		"edge.log":  true,

		// Samourai Wallet
		"samourai":      true,
		"samourai.conf": true,
		"samourai.log":  true,

		// Wasabi Wallet
		"wasabi":      true,
		"wasabi.conf": true,
		"wasabi.log":  true,

		// BlueWallet
		"bluewallet":      true,
		"bluewallet.conf": true,
		"bluewallet.log":  true,

		// Sparrow Wallet
		"sparrow":      true,
		"sparrow.conf": true,
		"sparrow.log":  true,

		// ElectrumSV
		"electrumsv":      true,
		"electrumsv.conf": true,
		"electrumsv.log":  true,

		// Bitcoin Core RPC
		"bitcoin.conf": true,
		"bitcoin.log":  true,

		// Litecoin Core
		"litecoin.conf": true,
		"litecoin.log":  true,

		// Dogecoin Core
		"dogecoin.conf": true,
		"dogecoin.log":  true,

		// Monero
		"monero":             true,
		"monero.conf":        true,
		"monero.log":         true,
		"monero-wallet":      true,
		"monero-wallet.keys": true,

		// Zcash
		"zcash":      true,
		"zcash.conf": true,
		"zcash.log":  true,

		// Dash
		"dash":      true,
		"dash.conf": true,
		"dash.log":  true,

		// Общие файлы кошельков
		"wallet":                          true,
		"wallet.json":                     true,
		"wallet.txt":                      true,
		"wallet.backup":                   true,
		"wallet.encrypted":                true,
		"wallet.key":                      true,
		"wallet.key.backup":               true,
		"wallet.seed":                     true,
		"wallet.mnemonic":                 true,
		"wallet.phrase":                   true,
		"wallet.passphrase":               true,
		"wallet.password":                 true,
		"wallet.pin":                      true,
		"wallet.pass":                     true,
		"wallet.pwd":                      true,
		"wallet.passwd":                   true,
		"wallet.passwords":                true,
		"wallet.passwords.txt":            true,
		"wallet.passwords.json":           true,
		"wallet.passwords.csv":            true,
		"wallet.passwords.xlsx":           true,
		"wallet.passwords.xls":            true,
		"wallet.passwords.doc":            true,
		"wallet.passwords.docx":           true,
		"wallet.passwords.pdf":            true,
		"wallet.passwords.rtf":            true,
		"wallet.passwords.txt.bak":        true,
		"wallet.passwords.json.bak":       true,
		"wallet.passwords.csv.bak":        true,
		"wallet.passwords.xlsx.bak":       true,
		"wallet.passwords.xls.bak":        true,
		"wallet.passwords.doc.bak":        true,
		"wallet.passwords.docx.bak":       true,
		"wallet.passwords.pdf.bak":        true,
		"wallet.passwords.rtf.bak":        true,
		"wallet.passwords.txt.old":        true,
		"wallet.passwords.json.old":       true,
		"wallet.passwords.csv.old":        true,
		"wallet.passwords.xlsx.old":       true,
		"wallet.passwords.xls.old":        true,
		"wallet.passwords.doc.old":        true,
		"wallet.passwords.docx.old":       true,
		"wallet.passwords.pdf.old":        true,
		"wallet.passwords.rtf.old":        true,
		"wallet.passwords.txt.reserve.1":  true,
		"wallet.passwords.json.reserve.1": true,
		"wallet.passwords.csv.reserve.1":  true,
		"wallet.passwords.xlsx.reserve.1": true,
		"wallet.passwords.xls.reserve.1":  true,
		"wallet.passwords.doc.reserve.1":  true,
		"wallet.passwords.docx.reserve.1": true,
		"wallet.passwords.pdf.reserve.1":  true,
		"wallet.passwords.rtf.reserve.1":  true,
		"wallet.passwords.txt.reserve.2":  true,
		"wallet.passwords.json.reserve.2": true,
		"wallet.passwords.csv.reserve.2":  true,
		"wallet.passwords.xlsx.reserve.2": true,
		"wallet.passwords.xls.reserve.2":  true,
		"wallet.passwords.doc.reserve.2":  true,
		"wallet.passwords.docx.reserve.2": true,
		"wallet.passwords.pdf.reserve.2":  true,
		"wallet.passwords.rtf.reserve.2":  true,
	}

	return walletFileNames[fileName]
}

// getStandardConfigDirectoriesOrFiles возвращает список стандартных директорий для конфигурационных файлов
func getStandardConfigDirectoriesOrFiles() []string {
	// Получаем домашнюю директорию пользователя
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Если не удалось получить домашнюю директорию, возвращаем только системные пути
		return []string{
			"/etc",
			"/etc/ssh",
			"/root/.ssh",
		}
	}

	// Стандартные директории для конфигурационных файлов
	configDirs := []string{
		filepath.Join(homeDir, ".ssh"),    // ~/.ssh - основная директория пользователя
		filepath.Join(homeDir, ".config"), // ~/.config - современная конфигурационная директория
		filepath.Join(homeDir, ".aws"),    // AWS конфигурация
		filepath.Join(homeDir, ".docker"), // Docker конфигурация
		filepath.Join(homeDir, ".kube"),   // Kubernetes конфигурация
		filepath.Join(homeDir, ".ssh"),    // .ssh
		filepath.Join(homeDir, ".config"), // .ssh
		"/etc",                            // Системная конфигурация
		"/etc/ssh",                        // Системная SSH конфигурация
		"/root/.ssh",                      // SSH директория root пользователя
		"/root/.config",                   // Конфигурация root пользователя
	}

	return configDirs
}
