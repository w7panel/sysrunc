#
# Sysrunc library modules
#

.PHONY: lint listpackages

GO := go

lint:
	@for d in $(_allgodirs); do \
	   (cd $$d && $(GO) vet ./... && $(GO) fmt ./...) || exit; \
	done

listpackages:
	@echo $(allpackages)

# memoize allpackages, so that it's executed only once and only if used
_allgodirs = $(patsubst %/go.mod,%,$(wildcard */go.mod))
_allpackages = $(shell for d in $(_allgodirs); do cd $$d && $(GO) list ./... | grep -v vendor; cd ..; done)
allpackages = $(if $(__allpackages),,$(eval __allpackages := $$(_allpackages)))$(__allpackages)
