BINARY       := invenv
PKG          := ./...
PWD          := $(shell pwd)
VERSION      := 0.0.0
MONOVA       := $(shell which monova 2> /dev/null)
LDFLAGS       = -ldflags="-X $(BINARY)/cmd.Version=$(VERSION)"
SPEC_FILE    := $(BINARY).spec
COPR_PROJECT := surfly/$(BINARY)
RPMBUILD_DIR := $(PWD)/rpmbuild
TARBALL_DIR  := $(RPMBUILD_DIR)/SOURCES
TARBALL       = $(TARBALL_DIR)/$(BINARY)-$(VERSION).tar.gz

export PATH := $(PATH):$(shell go env GOPATH)/bin

# For COPR:
# $ sudo dnf install rpm-build copr-cli
# Refresh token every 180 days: https://copr.fedorainfracloud.org/api/

version:
ifdef MONOVA
override VERSION = $(shell monova)
override LDFLAGS = -ldflags="-X $(BINARY)/cmd.Version=$(VERSION)"
override TARBALL = $(TARBALL_DIR)/$(BINARY)-$(VERSION).tar.gz
else
	$(info "Install monova with: grm install jsnjack/monova")
endif

test:
	go test $(PKG)

vet:
	go vet $(PKG)

fmt:
	@command -v goimports >/dev/null 2>&1 || { \
	  echo "goimports is not installed. Install it with:"; \
	  echo "  go install golang.org/x/tools/cmd/goimports@latest"; \
	  exit 1; \
	}
	goimports -w .

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint is not installed. Install it with:"; \
	  echo "  grm install golangci/golangci-lint"; \
	  exit 1; \
	}
	golangci-lint run

check: fmt vet build test lint
	@echo "==> make check: all green"

standards:
	curl -sL https://raw.githubusercontent.com/jsnjack/standards/master/AGENTS.universal.md \
	    -o AGENTS.universal.md
	curl -sL https://raw.githubusercontent.com/jsnjack/standards/master/AGENTS.go.md \
	    -o AGENTS.go.md

bin/$(BINARY): bin/$(BINARY)_linux_amd64
	cp $< $@
	ln -sf bin/$(BINARY) $(BINARY)
bin/$(BINARY)_linux_amd64: version
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $@
bin/$(BINARY)_linux_arm64: version
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $@
bin/$(BINARY)_darwin_amd64: version
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $@
bin/$(BINARY)_darwin_arm64: version
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $@

build: bin/$(BINARY) bin/$(BINARY)_linux_amd64 bin/$(BINARY)_linux_arm64 bin/$(BINARY)_darwin_amd64 bin/$(BINARY)_darwin_arm64

$(SPEC_FILE): $(SPEC_FILE).tpl version
	VERSION=$(VERSION) envsubst < $< > $@

$(TARBALL): version
	mkdir -p $(TARBALL_DIR)
	mkdir -p $(TARBALL_DIR)/$(BINARY)-$(VERSION)
	find . -type f \( -name "*.go" -o -name "go.mod" -o -name "go.sum" \) -exec cp --parents \{} $(TARBALL_DIR)/$(BINARY)-$(VERSION) \;
	tar -C $(TARBALL_DIR) -czf $(TARBALL) $(BINARY)-$(VERSION)
	rm -rf $(TARBALL_DIR)/$(BINARY)-$(VERSION)

# build the source RPM package
rpm: clean $(SPEC_FILE) $(TARBALL)
	rpmbuild -bs $(SPEC_FILE) --define "_topdir $(RPMBUILD_DIR)"

# upload the source RPM package to Fedora COPR
copr: rpm
	ls $(RPMBUILD_DIR)/SRPMS/$(BINARY)-*.src.rpm | xargs -t -I % copr-cli --config ~/.config/copr_surfly build --nowait $(COPR_PROJECT) %

github: build
	tar -czf bin/$(BINARY)_linux_amd64.tar.gz  --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_linux_amd64
	tar -czf bin/$(BINARY)_linux_arm64.tar.gz  --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_linux_arm64
	tar -czf bin/$(BINARY)_darwin_amd64.tar.gz --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_darwin_amd64
	tar -czf bin/$(BINARY)_darwin_arm64.tar.gz --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_darwin_arm64
	grm release jsnjack/$(BINARY) \
		-f bin/$(BINARY)_linux_amd64.tar.gz \
		-f bin/$(BINARY)_linux_arm64.tar.gz \
		-f bin/$(BINARY)_darwin_amd64.tar.gz \
		-f bin/$(BINARY)_darwin_arm64.tar.gz \
		-t "v`monova`"

release: github rpm copr

clean:
	rm -f $(SPEC_FILE)
	rm -rf $(RPMBUILD_DIR)
	rm -rf bin/ $(BINARY)

.PHONY: version build release test vet fmt lint check standards clean rpm copr github
