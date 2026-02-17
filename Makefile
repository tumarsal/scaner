# Переменные из .env (опционально)
-include .env
export

# Имена по умолчанию
BINARY_NAME ?= scaner
BUILD_DIR   ?= build
DEPLOY_PATH ?= /tmp

# Кросс-компиляция: OS и архитектуры
GOOS_LIST   := linux darwin windows
GOARCH_LIST := amd64 arm64

.PHONY: build build-all deploy clean clean-all

# Сборка только для текущей платформы
build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/main.go

# Сборка для всех OS/arch (суффикс .exe для Windows)
build-all:
	@mkdir -p $(BUILD_DIR)
	@for os in $(GOOS_LIST); do \
	  for arch in $(GOARCH_LIST); do \
	    suffix=; [ "$$os" = "windows" ] && suffix=".exe"; \
	    echo "Building $$os/$$arch..."; \
	    GOOS=$$os GOARCH=$$arch go build -o $(BUILD_DIR)/$(BINARY_NAME)-$$os-$$arch$$suffix ./cmd/main.go; \
	  done; \
	done

deploy: build-all
	@if [ -z "$$SSH_MACHINE" ]; then \
		echo "Ошибка: задайте SSH_MACHINE в .env (например user@host)"; \
		exit 1; \
	fi
	rsync -avz -e "ssh $(SSH_OPTS)" $(BUILD_DIR)/ $(SSH_MACHINE):$(DEPLOY_PATH)/

# SSH_OPTS для rsync/scp, например: SSH_OPTS=-p 2222
SSH_OPTS ?= $(if $(DEPLOY_PORT),-p $(DEPLOY_PORT),)

clean:
	rm -f $(BUILD_DIR)/$(BINARY_NAME)

# Удалить все бинарники кросс-компиляции
clean-all: clean
	@for os in $(GOOS_LIST); do \
	  for arch in $(GOARCH_LIST); do \
	    suffix=; [ "$$os" = "windows" ] && suffix=".exe"; \
	    rm -f $(BUILD_DIR)/$(BINARY_NAME)-$$os-$$arch$$suffix; \
	  done; \
	done


update: 
	cd ../rtty && make update_remote