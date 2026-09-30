# scaner just recipes — каждая cobra-команда имеет рецепт

path := "."
extra := ""

_run := "go run ./cmd"

default:
    @just --list

build:
    mkdir -p build
    go build -o build/scaner ./cmd/main.go

test:
    go test ./...

scan path="." extra="":
    {{ _run }} scan {{ path }} {{ extra }}

upload path="." extra="":
    {{ _run }} upload -p {{ path }} {{ extra }}

download extra="":
    {{ _run }} download {{ extra }}

ls extra="":
    {{ _run }} ls {{ extra }}

clean path="." force="" maxdepth="" extra="":
    #!/usr/bin/env bash
    set -euo pipefail
    args="clean {{ path }} {{ extra }}"
    if [ -n "{{ force }}" ]; then args="$args --force"; fi
    if [ -n "{{ maxdepth }}" ]; then args="$args --maxdepth {{ maxdepth }}"; fi
    go run ./cmd $args

dsstore path="/Users/tumarsal/" force="" maxdepth="" extra="":
    #!/usr/bin/env bash
    set -euo pipefail
    args="dsstore {{ path }} {{ extra }}"
    if [ -n "{{ force }}" ]; then args="$args --force"; fi
    if [ -n "{{ maxdepth }}" ]; then args="$args --maxdepth {{ maxdepth }}"; fi
    go run ./cmd $args
