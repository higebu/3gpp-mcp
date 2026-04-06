.PHONY: build install import import-dir build-db download-specs download-latest-specs download-all-specs update-specs db-info test clean web

SPECS_DIR ?= specs
DB_PATH ?= data/3gpp.db
BIN_DIR ?= bin
RELEASE ?= 19

# Build the MCP server
build:
	go build -o $(BIN_DIR)/3gpp-mcp ./cmd/3gpp-mcp

# Install the MCP server
install:
	go install ./cmd/3gpp-mcp

# Import a single .docx file (usage: make import FILE=path/to/spec.docx)
import: build
	./$(BIN_DIR)/3gpp-mcp import --db $(DB_PATH) $(FILE)

# Import all .docx files in SPECS_DIR
import-dir: build
	./$(BIN_DIR)/3gpp-mcp import-dir --db $(DB_PATH) $(SPECS_DIR)

# Download + import in one step (recommended)
build-db: build
	./$(BIN_DIR)/3gpp-mcp build --release $(RELEASE) --db $(DB_PATH) --convert-doc

# Download specs for a specific release (download only, no conversion)
download-specs: build
	./$(BIN_DIR)/3gpp-mcp download --release $(RELEASE) --output-dir $(SPECS_DIR) --convert-doc

# Download the single latest version of each spec across all releases
download-latest-specs: build
	./$(BIN_DIR)/3gpp-mcp download --latest --output-dir $(SPECS_DIR) --convert-doc

# Download all versions of all specs across all releases
download-all-specs: build
	./$(BIN_DIR)/3gpp-mcp download --all --output-dir $(SPECS_DIR) --convert-doc

# Update specs in DB to latest versions
update-specs: build
	./$(BIN_DIR)/3gpp-mcp update --db $(DB_PATH) --convert-doc

# Show database info
db-info:
	@sqlite3 $(DB_PATH) "SELECT COUNT(*) || ' specs' FROM specs; SELECT COUNT(*) || ' sections' FROM sections;" 2>/dev/null || echo "Database not found: $(DB_PATH)"

# Start HTTP server with web viewer
web: build
	./$(BIN_DIR)/3gpp-mcp serve --db $(DB_PATH) --transport http --addr :8080 --web

# Run Go tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf $(BIN_DIR)
	rm -f data/3gpp.db
