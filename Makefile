VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/lyovkindanil/Eva_Manager/internal/buildinfo.Version=$(VERSION)

.PHONY: all web build test xray run docker clean

all: build

# Интерфейс → internal/web/dist (встраивается в бинарник).
web:
	cd web && npm ci && npm run build

# Панель со встроенным интерфейсом → bin/eva-manager.
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/eva-manager ./cmd/eva-manager

# Тесты. С EVA_TEST_POSTGRES_URL — и на PostgreSQL, с EVA_TEST_XRAY_BIN —
# с настоящим Xray (make xray скачает его в bin/xray).
test:
	go vet ./...
	go test -race ./...

# Xray из XRAY_VERSION → bin/xray.
xray:
	scripts/fetch-xray.sh

# Панель с данными в ./bin/data и Xray из bin/xray.
run: build
	EVA_DATA_DIR=$(CURDIR)/bin/data EVA_XRAY_BIN=$(CURDIR)/bin/xray/xray ./bin/eva-manager serve

docker:
	docker build --build-arg VERSION=$(VERSION) -t eva-manager:$(VERSION) .

clean:
	rm -rf bin internal/web/dist/assets internal/web/dist/index.html
