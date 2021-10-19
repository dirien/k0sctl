GO_SRCS := $(shell find . -type f -name '*.go' -a ! -name 'zz_generated*')

k0sctl: $(GO_SRCS)
	go build $(BUILD_FLAGS) -o k0sctl main.go

.PHONY: clean
clean:
	rm -rf bin/

smoketests := smoke-basic smoke-upgrade smoke-reset smoke-os-override smoke-init smoke-backup-restore
.PHONY: $(smoketests)
$(smoketests): k0sctl
	$(MAKE) -C smoke-test $@

golint := $(shell which golangci-lint)
ifeq ($(golint),)
golint := $(shell go env GOPATH)/bin/golangci-lint
endif

$(golint):
	go get github.com/golangci/golangci-lint/cmd/golangci-lint@v1.31.0

goreleaser := $(shell which goreleaser)
ifeq ($(goreleaser),)
goreleaser := $(shell go env GOPATH)/bin/goreleaser
endif

$(goreleaser):
	go install github.com/goreleaser/goreleaser@latest

.PHONY: lint
lint: $(golint)
	$(golint) run ./...


.PHONY: build-all
build-all: $(goreleaser)
	$(goreleaser) build --snapshot --rm-dist