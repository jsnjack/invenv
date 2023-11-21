BINARY:=invenv
PWD:=$(shell pwd)
VERSION=0.0.0
MONOVA:=$(shell which monova dot 2> /dev/null)
SPEC_FILE:=${BINARY}.spec
COPR_PROJECT:=surfly/${BINARY}
RPMBUILD_DIR:=${PWD}/rpmbuild
TARBALL_DIR:=${RPMBUILD_DIR}/SOURCES
TARBALL:=${TARBALL_DIR}/${BINARY}-${VERSION}.tar.gz

# For COPR:
# $ sudo dnf install rpm-build copr-cli
# Refresh token every 180 days: https://copr.fedorainfracloud.org/api/

version:
ifdef MONOVA
override VERSION=$(shell monova)
override TARBALL=${TARBALL_DIR}/${BINARY}-${VERSION}.tar.gz
else
	$(info "Install monova (https://github.com/jsnjack/monova) to calculate version")
endif

bin/${BINARY}: bin/${BINARY}_linux_amd64
	cp bin/${BINARY}_linux_amd64 bin/${BINARY}

bin/${BINARY}_linux_amd64: version main.go cmd/*.go
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-X ${BINARY}/cmd.Version=${VERSION}" -o bin/${BINARY}_linux_amd64

bin/${BINARY}_darwin_amd64: version main.go cmd/*.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-X ${BINARY}/cmd.Version=${VERSION}" -o bin/${BINARY}_darwin_amd64

bin/${BINARY}_darwin_arm64: version main.go cmd/*.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-X ${BINARY}/cmd.Version=${VERSION}" -o bin/${BINARY}_darwin_arm64

build: test bin/${BINARY} bin/${BINARY}_linux_amd64 bin/${BINARY}_darwin_amd64 bin/${BINARY}_darwin_arm64

test:
	cd cmd && go test

${SPEC_FILE}: ${SPEC_FILE}.tpl
	VERSION=${VERSION} envsubst < $< > $@

${TARBALL}:
	mkdir -p ${TARBALL_DIR}
	mkdir -p ${TARBALL_DIR}/${BINARY}-${VERSION}
	find . -type f \( -name "*.go" -o -name "go.mod" -o -name "go.sum" \) -exec cp --parents \{} ${TARBALL_DIR}/${BINARY}-${VERSION} \;
	tar -C ${TARBALL_DIR} -czf ${TARBALL} ${BINARY}-${VERSION}
	rm -rf ${TARBALL_DIR}/${BINARY}-${VERSION}

# build the RPM package
rpm: clean ${SPEC_FILE} ${TARBALL}
	rpmbuild -bs ${SPEC_FILE} --define "_topdir ${RPMBUILD_DIR}"

# upload the RPM package to Fedora COPR
copr: $(SRPM_FILE)
	ls ${RPMBUILD_DIR}/SRPMS/${BINARY}-*.src.rpm | xargs -t -I % copr-cli --config ~/.config/copr_surfly build --nowait $(COPR_PROJECT) %

release: build rpm copr
	tar --transform='s,_.*,,' --transform='s,bin/,,' -cz -f bin/${BINARY}_linux_amd64.tar.gz bin/${BINARY}_linux_amd64
	tar --transform='s,_.*,,' --transform='s,bin/,,' -cz -f bin/${BINARY}_darwin_amd64.tar.gz bin/${BINARY}_darwin_amd64
	tar --transform='s,_.*,,' --transform='s,bin/,,' -cz -f bin/${BINARY}_darwin_arm64.tar.gz bin/${BINARY}_darwin_arm64
	grm release jsnjack/${BINARY} -f bin/${BINARY} -f bin/${BINARY}_linux_amd64.tar.gz -f bin/${BINARY}_darwin_amd64.tar.gz -f bin/${BINARY}_darwin_arm64.tar.gz -t "v`monova`"

clean:
	rm -rf ${RPMBUILD_DIR}

.PHONY: version release build test
