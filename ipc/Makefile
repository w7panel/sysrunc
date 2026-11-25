#
# sysrunc-ipc Makefile
#

.PHONY: clean sysipc-grpc-fs-proto lint list-packages

GO := go

SYSIPC_GRPC_FS_DIR  := sysruncFsGrpc
sysrunc-ipc: sysipc-grpc-fs-proto

sysipc-grpc-fs-proto:
	$(MAKE) -C $(SYSIPC_GRPC_FS_DIR)/protocol

# Note: we must build the protobuf before go mod tidy
gomod-tidy: sysipc-grpc-fs-proto
	$(GO) mod tidy

lint:
	$(GO) vet $(allpackages)
	$(GO) fmt $(allpackages)

listpackages:
	@echo $(allpackages)

clean:
	$(MAKE) -C $(SYSIPC_GRPC_FS_DIR)/protocol clean

distclean: clean

# memoize allpackages, so that it's executed only once and only if used
_allpackages = $(shell $(GO) list ./... | grep -v vendor)
allpackages = $(if $(__allpackages),,$(eval __allpackages := $$(_allpackages)))$(__allpackages)
