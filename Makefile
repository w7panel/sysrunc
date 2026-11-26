.PHONY: build test chart package-chart build-image push-zpk full check-release ensure-zpk

BIN_DIR := dist/bin/generic
CHART_DIR := charts/sysrunc
CHART_NAME := $(shell sed -n 's/^name:[[:space:]]*//p' $(CHART_DIR)/Chart.yaml | head -n 1 | tr -d '"')
CHART_VERSION := $(shell sed -n 's/^version:[[:space:]]*//p' $(CHART_DIR)/Chart.yaml | head -n 1 | tr -d '"')
CHART_OUTPUT := dist/charts
CHART_PACKAGE := $(CHART_OUTPUT)/$(CHART_NAME)-$(CHART_VERSION).tgz
VERSION ?= $(CHART_VERSION)
IMAGE_REPO ?= ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc
IMAGE_TAG ?= v$(VERSION)
ALPINE_BASE_IMAGE ?= docker.cnb.cool/i0358/docker-images-chrom/alpine:3.20
ZPK_NAME ?= w7panel-sysrunc
ZPK_HOST ?= zpk.idc.w7.com
ZPK_BIN ?= $(shell command -v zpk 2>/dev/null || printf '%s/dist/tools/zpk' "$(CURDIR)")
ZPK_DOWNLOAD_URL ?= https://github.com/w7panel/w7panel-zpk/releases/download/latest/zpk_linux_amd64
ZPK_PROXY_URL ?= https://gh-proxy.org/$(ZPK_DOWNLOAD_URL)

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
	helm lint $(CHART_DIR) --set installMode=host
	helm template w7panel-sysrunc $(CHART_DIR) --set installMode=host >/dev/null
	helm template w7panel-sysrunc $(CHART_DIR) --set installMode=nested >/dev/null

check-release:
	@test -n "$(CHART_NAME)" && test -n "$(CHART_VERSION)" || { echo 'Chart.yaml needs name and version' >&2; exit 2; }
	@test "$(VERSION)" = "$(CHART_VERSION)" || { echo 'VERSION must match Chart.yaml version' >&2; exit 2; }
	@for tool in helm docker curl; do command -v "$$tool" >/dev/null || { echo "Missing $$tool" >&2; exit 2; }; done
	@test -n "$${ZPK_IDC_PASSWORD:-$${PASSWORD:-}}" || { echo 'Set ZPK_IDC_PASSWORD or PASSWORD' >&2; exit 2; }

ensure-zpk:
	@if ! test -x "$(ZPK_BIN)"; then \
		mkdir -p "$(dir $(ZPK_BIN))"; \
		curl -fsSL --connect-timeout 5 --max-time 30 -o "$(ZPK_BIN).tmp" "$(ZPK_DOWNLOAD_URL)" || \
		curl -fsSL --connect-timeout 10 --max-time 120 -o "$(ZPK_BIN).tmp" "$(ZPK_PROXY_URL)" || exit; \
		chmod +x "$(ZPK_BIN).tmp" && mv "$(ZPK_BIN).tmp" "$(ZPK_BIN)"; \
	fi

package-chart: chart
	mkdir -p $(CHART_OUTPUT)
	helm package $(CHART_DIR) --destination $(CHART_OUTPUT)

build-image: build
	docker build --platform linux/amd64 --build-arg sys_arch=amd64 --build-arg sysrunc_version=$(VERSION) --build-arg ALPINE_BASE_IMAGE=$(ALPINE_BASE_IMAGE) -f deploy/Dockerfile.sysrunc-k3s -t $(IMAGE_REPO):$(IMAGE_TAG) .
	docker push $(IMAGE_REPO):$(IMAGE_TAG)

push-zpk: ensure-zpk
	@test -f "$(CHART_PACKAGE)" || { echo 'Run make package-chart first' >&2; exit 2; }
	@test -n "$${ZPK_IDC_PASSWORD:-$${PASSWORD:-}}" || { echo 'Set ZPK_IDC_PASSWORD or PASSWORD' >&2; exit 2; }
	@zpk_config_dir=$$(mktemp -d); \
	trap 'rm -rf "$$zpk_config_dir"' EXIT; \
	export XDG_CONFIG_HOME="$$zpk_config_dir"; \
	"$(ZPK_BIN)" login --username=w7admin --password="$${ZPK_IDC_PASSWORD:-$${PASSWORD}}" --host=$(ZPK_HOST) && \
	"$(ZPK_BIN)" use $(ZPK_NAME) && \
	"$(ZPK_BIN)" attach add --path=$(CHART_PACKAGE) --type=helm && \
	"$(ZPK_BIN)" push

.NOTPARALLEL: full
full: check-release package-chart build-image push-zpk
