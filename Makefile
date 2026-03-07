.PHONY: vet install-tools run build install clean

BIN_NAME ?= wiki
BUILD_OUT ?= /tmp/$(BIN_NAME)
INSTALL_BIN_DIR ?= /usr/bin
SERVICE_NAME ?= wiki
SYSTEMD_UNIT_DIR ?= /etc/systemd/system
SERVICE_SRC ?= deploy/systemd/$(SERVICE_NAME).service
SERVICE_DST ?= $(SYSTEMD_UNIT_DIR)/$(SERVICE_NAME).service
ENV_TEMPLATE ?= deploy/systemd/$(SERVICE_NAME).env
ENV_FILE ?= /etc/env/$(SERVICE_NAME).env
SUDO ?= sudo

# Run formatting and linting tools.
vet:
	go fmt ./...
	go vet ./...
	staticcheck ./...

# Install staticcheck.
install-tools:
	go install honnef.co/go/tools/cmd/staticcheck@latest

# Run the development server.
run:
	go run . -port=${PORT:-8080}

# Build production binary.
build:
	go build -o $(BUILD_OUT) .

# Install binary and systemd service, then restart service.
install: build
	$(SUDO) install -d -m 0755 $(INSTALL_BIN_DIR)
	$(SUDO) install -m 0755 $(BUILD_OUT) $(INSTALL_BIN_DIR)/$(BIN_NAME)
	$(SUDO) install -d -m 0755 $(SYSTEMD_UNIT_DIR)
	$(SUDO) install -m 0644 $(SERVICE_SRC) $(SERVICE_DST)
	$(SUDO) install -d -m 0755 $(dir $(ENV_FILE))
	@if [ ! -f "$(ENV_FILE)" ]; then \
		$(SUDO) install -m 0644 $(ENV_TEMPLATE) $(ENV_FILE); \
		echo "Installed $(ENV_FILE) from template."; \
	else \
		echo "Keeping existing $(ENV_FILE)."; \
	fi
	$(SUDO) systemctl daemon-reload
	$(SUDO) systemctl enable $(SERVICE_NAME).service
	$(SUDO) systemctl restart $(SERVICE_NAME).service

clean:
	rm -f $(BUILD_OUT)
