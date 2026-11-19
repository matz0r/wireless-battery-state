# Build output goes to target/, like cargo: target/debug for development builds,
# target/release for the static, stripped binary that gets installed and released.
VERSION ?= $(shell sed -n 's/^var version = "\(.*\)"/\1/p' src/main.go)
BIN     := battery-state

.PHONY: build release run test vet fmt install uninstall icon clean

build:
	go build -o target/debug/$(BIN) ./src

release:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o target/release/$(BIN) ./src

run: build
	target/debug/$(BIN) $(ARGS)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w src assets

install: release
	target/release/$(BIN) --install

uninstall: release
	target/release/$(BIN) --uninstall

# Regenerates assets/icon.svg and icon-{256,512}.png (needs Pillow).
icon:
	python3 assets/make_icon.py

clean:
	rm -rf target
