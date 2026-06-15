.PHONY: build frontend run cross-arm64 cross-amd64 deploy clean

# -s -w strips the symbol table and DWARF debug info, shrinking the binary.
LDFLAGS := -s -w
BIN     := homelab-dashboard

build:
	@echo "Building Go binary..."
	go build -ldflags="$(LDFLAGS)" -o $(BIN) .

frontend:
	cd frontend && npm install
	@echo "Building Svelte frontend into static/ ..."
	cd frontend && npm run build

run:
	go run .

cross-arm64:
	@echo "Cross-compiling for Raspberry Pi (linux/arm64)..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BIN)-linux-arm64 .

cross-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BIN)-linux-amd64 .

# Optional development convenience: push a fresh build to a running Pi over SSH.
# Override PI on the command line, e.g.  make deploy PI=pi@raspberrypi.local
PI         ?= pi@raspberrypi.local
REMOTE_DIR ?= /opt/homelab-dashboard

deploy: frontend cross-arm64
	@echo "Deploying to $(PI):$(REMOTE_DIR) ..."
	scp $(BIN)-linux-arm64 "$(PI):/tmp/$(BIN).new"
	rsync -az --delete static/ "$(PI):/tmp/homelab-dashboard-static/"
	ssh "$(PI)" 'sudo install -m 0755 /tmp/$(BIN).new $(REMOTE_DIR)/$(BIN) && sudo rsync -a --delete /tmp/homelab-dashboard-static/ $(REMOTE_DIR)/static/ && sudo systemctl restart homelab-dashboard'
	@echo "Deployed and restarted."

clean:
	rm -f $(BIN) $(BIN)-linux-arm64 $(BIN)-linux-amd64
	cd frontend && rm -rf node_modules dist
