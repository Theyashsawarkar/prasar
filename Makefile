# Build:                             make
# System-wide or packaged install:   make install [PREFIX=/usr/local] [DESTDIR=]
# Per-user, without make:            ./install.sh
PREFIX ?= /usr/local
BINDIR  = $(DESTDIR)$(PREFIX)/bin
SHARE   = $(DESTDIR)$(PREFIX)/share

.PHONY: build install uninstall check test lint dist

build:
	go build -trimpath -o bin/prasar ./cmd/prasar

install: build
	install -Dm755 bin/prasar $(BINDIR)/prasar
	install -Dm644 README.md $(SHARE)/doc/prasar/README.md
	install -Dm644 CHANGELOG.md $(SHARE)/doc/prasar/CHANGELOG.md
	install -Dm644 LICENSE $(SHARE)/licenses/prasar/LICENSE

uninstall:
	rm -f $(BINDIR)/prasar
	rm -rf $(SHARE)/doc/prasar $(SHARE)/licenses/prasar

test:
	go test ./...

lint:
	go vet ./...
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	shellcheck -S warning prasar.tmux install.sh scripts/prasar-run

# What CI runs.
check: lint test build
	./bin/prasar --version
	./bin/prasar --help >/dev/null

# Release binaries (CI does this on a tag).
dist:
	mkdir -p dist
	for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags='-s -w' -o dist/prasar-linux-$$arch ./cmd/prasar; \
	done
