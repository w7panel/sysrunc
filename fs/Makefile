#
# sysrunc-fs Makefile
#
# Note: targets must execute from the $SYSFS_DIR

.PHONY: clean sysrunc-fs-debug sysrunc-fs-static lint list-packages

GO := go

SYSFS_BUILDROOT := build
SYSFS_BUILDDIR := $(SYSFS_BUILDROOT)/$(TARGET_ARCH)
SYSFS_TARGET := sysrunc-fs
SYSFS_DEBUG_TARGET := sysrunc-fs-debug
SYSFS_STATIC_TARGET := sysrunc-fs-static
SYSFS_DIR := $(CURDIR)
SYSFS_SRC := $(shell find . 2>&1 | grep -E '.*\.(c|h|go|o)$$')

SYSFS_GRPC_DIR := ../ipc/sysruncFsGrpc
SYSFS_GRPC_SRC := $(shell find $(SYSFS_GRPC_DIR) -type f | grep -E '.*\.(c|h|go|proto)$$')

SYSLIB_DIR := ../libs
SYSLIB_SRC := $(shell find $(SYSLIB_DIR) -type f | grep -E '.*\.(c|h|go|proto)$$')

NSENTER_DIR := ../runc/libcontainer/nsenter
NSENTER_SRC := $(shell find $(NSENTER_DIR) -type f | grep -E '.*\.(c|h|go)$$')

COMMIT_NO := $(shell git rev-parse HEAD 2> /dev/null || true)
COMMIT ?= $(if $(shell git status --porcelain --untracked-files=no),$(COMMIT_NO)-dirty,$(COMMIT_NO))
BUILT_AT := $(shell date)
BUILT_BY := $(shell git config user.name)

LDFLAGS := -X 'main.edition=${EDITION}' -X main.version=${VERSION} \
		-X main.commitId=$(COMMIT) -X 'main.builtAt=$(BUILT_AT)' \
		-X 'main.builtBy=$(BUILT_BY)'

# Set cross-compilation flags if applicable.
ifneq ($(SYS_ARCH),$(TARGET_ARCH))
	ifeq ($(TARGET_ARCH),armel)
		GO_XCOMPILE := CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=6 CC=arm-linux-gnueabi-gcc
	else ifeq ($(TARGET_ARCH),armhf)
		GO_XCOMPILE := CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 CC=arm-linux-gnueabihf-gcc
	else ifeq ($(TARGET_ARCH),arm64)
		GO_XCOMPILE = CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc
	else ifeq ($(TARGET_ARCH),amd64)
		GO_XCOMPILE = CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC=x86_64-linux-gnu-gcc
	endif
endif

.DEFAULT: sysrunc-fs

sysrunc-fs: $(SYSFS_BUILDDIR)/$(SYSFS_TARGET)

$(SYSFS_BUILDDIR)/$(SYSFS_TARGET): $(SYSFS_SRC) $(SYSFS_GRPC_SRC) $(SYSLIB_SRC) $(NSENTER_SRC)
	$(GO_XCOMPILE) $(GO) build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o $(SYSFS_BUILDDIR)/sysrunc-fs ./cmd/sysrunc-fs

sysrunc-fs-debug: $(SYSFS_BUILDDIR)/$(SYSFS_DEBUG_TARGET)

$(SYSFS_BUILDDIR)/$(SYSFS_DEBUG_TARGET): $(SYSFS_SRC) $(SYSFS_GRPC_SRC) $(SYSLIB_SRC) $(NSENTER_SRC)
	$(GO_XCOMPILE) $(GO) build -buildvcs=false -trimpath -gcflags="all=-N -l" -ldflags "${LDFLAGS}" \
		-o $(SYSFS_BUILDDIR)/sysrunc-fs-debug ./cmd/sysrunc-fs

sysrunc-fs-static: $(SYSFS_BUILDDIR)/$(SYSFS_STATIC_TARGET)

$(SYSFS_BUILDDIR)/$(SYSFS_STATIC_TARGET): $(SYSFS_SRC) $(SYSFS_GRPC_SRC) $(SYSLIB_SRC) $(NSENTER_SRC)
	CGO_ENABLED=1 $(GO_XCOMPILE) $(GO) build -buildvcs=false -trimpath -tags "netgo osusergo" \
		-installsuffix netgo -ldflags "-extldflags -static ${LDFLAGS}" \
		-o $(SYSFS_BUILDDIR)/sysrunc-fs-static ./cmd/sysrunc-fs

gomod-tidy:
	$(GO) mod tidy

lint:
	$(GO) vet $(allpackages)
	$(GO) fmt $(allpackages)

listpackages:
	@echo $(allpackages)

clean:
	rm -f $(SYSFS_BUILDDIR)/sysrunc-fs $(SYSFS_BUILDDIR)/sysrunc-fs-debug $(SYSFS_BUILDDIR)/sysrunc-fs-static

distclean: clean
	rm -rf $(SYSFS_BUILDROOT)

# memoize allpackages, so that it's executed only once and only if used
_allpackages = $(shell $(GO) list ./... | grep -v vendor)
allpackages = $(if $(__allpackages),,$(eval __allpackages := $$(_allpackages)))$(__allpackages)
