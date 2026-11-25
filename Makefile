.PHONY: build test chart

BIN_DIR := dist/bin/generic

build:
	mkdir -p $(BIN_DIR)
	cd runc && go build -tags seccomp -ldflags '-linkmode external -extldflags -static' -o ../$(BIN_DIR)/sysrunc .
	cd fs && go build -ldflags '-linkmode external -extldflags -static' -o ../$(BIN_DIR)/sysrunc-fs ./cmd/sysrunc-fs
	cd admission && go build -o ../$(BIN_DIR)/sysrunc-admission ./cmd/sysrunc-admission
	cd snapshotter && CGO_ENABLED=0 go build -o ../$(BIN_DIR)/sysrunc-snapshotter ./cmd/sysrunc-snapshotter
	ln -sfn sysrunc $(BIN_DIR)/sysbox-runc
	ln -sfn sysrunc $(BIN_DIR)/sysbox-runc-lite

test:
	cd ipc && go test ./...
	cd admission && go test ./...
	cd snapshotter && go test ./...
	cd fs && go test ./...
	cd runc && go test ./...

chart:
	helm lint charts/sysrunc --set installMode=host
	helm template sysrunc charts/sysrunc --set installMode=host >/dev/null
	helm template sysrunc charts/sysrunc --set installMode=nested >/dev/null
