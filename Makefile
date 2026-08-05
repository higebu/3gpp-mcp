.PHONY: build install import import-dir build-db download-specs download-latest-specs update-specs db-info test clean web

SPECS_DIR ?= specs
DB_PATH ?= data/3gpp.db
BIN_DIR ?= bin
# RELEASE selects which versions to build/download. The default "latest" builds
# the latest version of every spec across all releases (full coverage, including
# specs that have no Release-19 version such as TS 34.108). Set RELEASE to a
# number (e.g. RELEASE=19) to restrict to a single release.
RELEASE ?= latest
release_flag = $(if $(filter latest,$(RELEASE)),--latest,--release $(RELEASE))

# Build the MCP server
build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/3gpp-mcp ./cmd/3gpp-mcp

# Install the MCP server
install:
	go install ./cmd/3gpp-mcp

# Import a single .docx file (usage: make import FILE=path/to/spec.docx)
import: build
	@test -n "$(FILE)" || { echo "usage: make import FILE=path/to/spec.docx"; exit 1; }
	./$(BIN_DIR)/3gpp-mcp import --db $(DB_PATH) $(FILE)

# Import all .docx files in SPECS_DIR
import-dir: build
	./$(BIN_DIR)/3gpp-mcp import-dir --db $(DB_PATH) $(SPECS_DIR)

# Download + import in one step (recommended). Builds the latest version of
# every spec by default; pass RELEASE=19 to restrict to a single release.
build-db: build
	./$(BIN_DIR)/3gpp-mcp build $(release_flag) --db $(DB_PATH) --convert-doc

# Download specs (no database import; legacy .doc files are converted to
# .docx). Latest of every spec by default; pass RELEASE=19 to restrict to a
# single release.
download-specs: build
	./$(BIN_DIR)/3gpp-mcp download $(release_flag) --output-dir $(SPECS_DIR) --convert-doc

# Download the single latest version of each spec across all releases
download-latest-specs: build
	./$(BIN_DIR)/3gpp-mcp download --latest --output-dir $(SPECS_DIR) --convert-doc

# Update specs in DB to latest versions
update-specs: build
	./$(BIN_DIR)/3gpp-mcp update --db $(DB_PATH) --convert-doc

# Show database info
db-info:
	@command -v sqlite3 >/dev/null || { echo "sqlite3 CLI not found"; exit 1; }
	@test -f $(DB_PATH) || { echo "Database not found: $(DB_PATH)"; exit 1; }
	@sqlite3 $(DB_PATH) "SELECT COUNT(*) || ' specs' FROM specs; SELECT COUNT(*) || ' sections' FROM sections;"

# Start HTTP server with web viewer
web: build
	./$(BIN_DIR)/3gpp-mcp serve --db $(DB_PATH) --transport http --addr :8080 --web

# Run Go tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf $(BIN_DIR)
	rm -f $(DB_PATH)
