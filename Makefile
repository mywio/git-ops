BINARY_NAME=git-ops
BUILD_DIR=bin
PLUGINS_DIR=$(BUILD_DIR)/plugins

.PHONY: all build plugins clean

all: build plugins

build:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) main.go

plugins:
	mkdir -p $(PLUGINS_DIR)
	rm -rf plugins/mcp/docs
	cp -R docs plugins/mcp/
	go build -buildmode=plugin -o $(PLUGINS_DIR)/env_forwarder.so plugins/env_forwarder/main.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/google_secret_manager.so plugins/google_secret_manager/main.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/mcp.so plugins/mcp/main.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/notifier_pushover.so plugins/notifier_pushover/pushover.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/notifier_webhook.so plugins/notifier_webhook/notifier_webhook.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/ui.so plugins/ui/main.go
	go build -buildmode=plugin -o $(PLUGINS_DIR)/webhook_trigger.so plugins/webhook_trigger/webhook_trigger.go

clean:
	rm -rf $(BUILD_DIR)
