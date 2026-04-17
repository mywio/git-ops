BINARY_NAME=git-ops
BUILD_DIR=bin
PLUGINS_DIR=$(BUILD_DIR)/plugins
VERSION ?= dev
GO_LDFLAGS=-ldflags "-X github.com/mywio/git-ops/pkg/core.Version=$(VERSION)"

.PHONY: all build plugins build-plugins ui _check-npm clean

all: build plugins

build:
	mkdir -p $(BUILD_DIR)
	go build $(GO_LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) main.go

plugins: build-plugins ui

build-plugins:
	mkdir -p $(PLUGINS_DIR)
	rm -rf plugins/mcp/docs
	cp -R docs plugins/mcp/
	go build -buildmode=plugin -o $(PLUGINS_DIR)/env_forwarder.so plugins/env_forwarder/main.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/file_forwarder.so plugins/file_forwarder/main.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/google_secret_manager.so plugins/google_secret_manager/main.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/image_refresh.so plugins/image_refresh/*.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/mcp.so plugins/mcp/*.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/discord.so plugins/notifier_discord/discord.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/notifier_pushover.so plugins/notifier_pushover/pushover.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/notifier_webhook.so plugins/notifier_webhook/notifier_webhook.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/ui.so plugins/ui/*.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/webhook_trigger.so plugins/webhook_trigger/webhook_trigger.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/reconciler.so plugins/reconciler/*.go

ui: _check-npm
	cd plugins/ui/frontend && npm install && npm run build

_check-npm:
	@command -v npm >/dev/null 2>&1 || { echo "npm not found - install Node.js to build the UI plugin"; exit 1; }
	@command -v node >/dev/null 2>&1 || { echo "node not found - install Node.js 20.19+ to build the UI plugin"; exit 1; }
	@node -e "const [major, minor] = process.versions.node.split('.').map(Number); if (major < 20 || (major === 20 && minor < 19)) { console.error('Node.js 20.19+ is required to build the UI plugin'); process.exit(1); }"

clean:
	rm -rf $(BUILD_DIR)
