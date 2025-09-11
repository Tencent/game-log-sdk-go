# Development Environment Setup Guide

English | [中文](./DEV_SETUP_CN.md)

## Prerequisites

### Required Tools
- **Go**: Version 1.21 or higher
- **Git**: For version control

### Development Tools
Based on Makefile configuration, the following tools are required:
- `gofmt`: Go code formatter
- `goimports`: Automatic import statement management
- `golint`: Go code style checker
- `go vet`: Go static analysis tool

## Setup Steps

### 1. Install Go
Download and install Go 1.21 or higher from the [official Go website](https://golang.org/dl/).

Verify installation:
```bash
go version
# Output should show: go version go1.21.x ...
```

### 2. Clone the Project
```bash
git clone <repository-url>
cd game-log-sdk-go
```

### 3. Install Development Tools
```bash
# Install goimports
go install golang.org/x/tools/cmd/goimports@latest

# Install golint
go install golang.org/x/lint/golint@latest

# Ensure tools are in PATH
export PATH=$PATH:$(go env GOPATH)/bin
```

### 4. Download Dependencies
```bash
# Download all module dependencies
go mod download

# Verify dependency integrity
go mod verify
```

### 5. Verify Development Environment
Run the `all` target in Makefile to verify environment configuration:
```bash
make all
```

This will execute:
- Code formatting (`gofmt -w .`)
- Import statement organization (`goimports -w .`)
- Static code analysis (`go vet ./...`)
- Code style check (`golint -set_exit_status ./...`)
- Build test binary

## Project Structure

```
game-log-sdk-go/
├── bufferpool/     # Buffer pool implementation
├── bytecloser/     # Byte stream closer
├── connpool/       # Connection pool management
├── crypto/         # Cryptographic utilities
├── discoverer/     # Service discovery
├── framer/         # Protocol frame handling
├── logger/         # Logging utilities
├── syncx/          # Extended synchronization primitives
├── tglog/          # Core game log implementation
├── util/           # Common utility functions
└── test/           # Test files
```

## Development Workflow

### Daily Development
1. **Before coding**: Ensure the codebase is up to date
   ```bash
   git pull origin main
   ```

2. **During development**: Run quality checks regularly
   ```bash
   make all
   ```

3. **Before committing**: Ensure all checks pass
   ```bash
   # Format code
   gofmt -w .
   goimports -w .
   
   # Run all checks
   make all
   ```

### Testing
```bash
# Run unit tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests for specific package
go test ./tglog/...
```

### Building
```bash
# Build test program (Linux platform)
GOOS=linux go build -o test/test test/test.go

# Clean build artifacts
make clean
```

## IDE Configuration

### VS Code
Install the Go extension and configure:
```json
{
    "go.lintTool": "golint",
    "go.lintOnSave": "workspace",
    "go.formatTool": "goimports",
    "go.formatOnSave": true,
    "go.vetOnSave": "workspace"
}
```

### GoLand/IntelliJ IDEA
1. Enable `goimports` as the formatting tool
2. Configure format on save
3. Enable `golint` and `go vet` inspections

## Troubleshooting

### Q: golint command not found
A: Ensure `$(go env GOPATH)/bin` is in your PATH environment variable:
```bash
echo 'export PATH=$PATH:$(go env GOPATH)/bin' >> ~/.bashrc
source ~/.bashrc
```

### Q: Dependency download fails
A: Configure Go proxy:
```bash
go env -w GOPROXY=https://goproxy.cn,direct
go env -w GOSUMDB=off
```

### Q: make all fails
A: Check that each tool is properly installed:
```bash
which gofmt
which goimports
which golint
go version
```

## Commit Guidelines

Before committing, ensure:
- ✅ Code is formatted (`gofmt -w .`)
- ✅ Import statements are organized (`goimports -w .`)
- ✅ Static analysis passes (`go vet ./...`)
- ✅ Style check passes (`golint ./...`)
- ✅ Unit tests pass (`go test ./...`)

## Getting Help

- See [README.md](README.md) for project details
- Check [CHANGELOG.md](CHANGELOG.md) for version history
- Submit an Issue to report problems