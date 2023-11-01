BINARY:=invenv
PWD:=$(shell pwd)
VERSION=0.0.0
MONOVA:=$(shell which monova dot 2> /dev/null)

version:
ifdef MONOVA
override VERSION=$(shell monova)
else
	$(info "Install monova (https://github.com/jsnjack/monova) to calculate version")
endif

bin/${BINARY}: bin/${BINARY}_linux_amd64
	cp bin/${BINARY}_linux_amd64 bin/${BINARY}

bin/${BINARY}_linux_amd64: version main.go cmd/*.go
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-X github.com/jsnjack/${BINARY}/cmd_root.Version=${VERSION}" -o bin/${BINARY}_linux_amd64

bin/${BINARY}_darwin_amd64: version main.go cmd/*.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-X github.com/jsnjack/${BINARY}/cmd_root.Version=${VERSION}" -o bin/${BINARY}_darwin_amd64

bin/${BINARY}_darwin_arm64: version main.go cmd/*.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-X github.com/jsnjack/${BINARY}/cmd_root.Version=${VERSION}" -o bin/${BINARY}_darwin_arm64

build: test bin/${BINARY} bin/${BINARY}_linux_amd64 bin/${BINARY}_darwin_amd64 bin/${BINARY}_darwin_arm64

test:
	cd cmd && go test

release: build
	tar --transform='s,_.*,,' --transform='s,bin/,,' -cz -f bin/${BINARY}_linux_amd64.tar.gz bin/${BINARY}_linux_amd64
	tar --transform='s,_.*,,' --transform='s,bin/,,' -cz -f bin/${BINARY}_darwin_amd64.tar.gz bin/${BINARY}_darwin_amd64
	tar --transform='s,_.*,,' --transform='s,bin/,,' -cz -f bin/${BINARY}_darwin_arm64.tar.gz bin/${BINARY}_darwin_arm64
	grm release jsnjack/${BINARY} -f bin/${BINARY} -f bin/${BINARY}_linux_amd64.tar.gz -f bin/${BINARY}_darwin_amd64.tar.gz -f bin/${BINARY}_darwin_arm64.tar.gz -t "v`monova`"

.PHONY: version release build test
