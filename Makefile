PYTHON ?= python3
PREFIX ?= $(HOME)/.local

.PHONY: build install test test-scripts manual-test vet check fmt clean

build:
	go build -trimpath -o .bin/vault-bridge ./cmd/vault-bridge

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 .bin/vault-bridge "$(DESTDIR)$(PREFIX)/bin/vault-bridge"

test:
	go test ./...

test-scripts:
	$(PYTHON) -m unittest discover -s scripts -p 'test_*.py' -v

manual-test:
	$(PYTHON) scripts/manual-test.py

vet:
	go vet ./...

check: test test-scripts vet build

fmt:
	gofmt -w cmd internal templates

clean:
	rm -f .bin/vault-bridge coverage.out
