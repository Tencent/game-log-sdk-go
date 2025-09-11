# 开发环境初始化指南

[English](./DEV_SETUP.md) | 中文

## 环境要求

### 必需工具
- **Go**: 1.21 或更高版本
- **Git**: 用于版本控制

### 开发工具
基于 Makefile 配置，需要以下工具：
- `gofmt`: Go 代码格式化工具
- `goimports`: 自动管理 import 语句
- `golint`: Go 代码风格检查
- `go vet`: Go 代码静态分析

## 初始化步骤

### 1. 安装 Go
从 [Go 官网](https://golang.org/dl/) 下载并安装 Go 1.21 或更高版本。

验证安装：
```bash
go version
# 输出应显示: go version go1.21.x ...
```

### 2. 克隆项目
```bash
git clone <repository-url>
cd game-log-sdk-go
```

### 3. 安装开发工具
```bash
# 安装 goimports
go install golang.org/x/tools/cmd/goimports@latest

# 安装 golint
go install golang.org/x/lint/golint@latest

# 确保工具在 PATH 中
export PATH=$PATH:$(go env GOPATH)/bin
```

### 4. 下载依赖
```bash
# 下载所有模块依赖
go mod download

# 验证依赖完整性
go mod verify
```

### 5. 验证开发环境
运行 Makefile 中的 all 目标来验证环境配置：
```bash
make all
```

这将执行：
- 代码格式化 (`gofmt -w .`)
- import 语句整理 (`goimports -w .`)
- 静态代码分析 (`go vet ./...`)
- 代码风格检查 (`golint -set_exit_status ./...`)
- 构建测试二进制文件

## 项目结构

```
game-log-sdk-go/
├── bufferpool/     # 缓冲池实现
├── bytecloser/     # 字节流关闭器
├── connpool/       # 连接池管理
├── crypto/         # 加密工具
├── discoverer/     # 服务发现
├── framer/         # 协议帧处理
├── logger/         # 日志工具
├── syncx/          # 同步原语扩展
├── tglog/          # 游戏日志核心实现
├── util/           # 通用工具函数
└── test/           # 测试文件
```

## 开发工作流

### 日常开发
1. **编写代码前**：确保代码库是最新的
   ```bash
   git pull origin main
   ```

2. **开发过程中**：定期运行质量检查
   ```bash
   make all
   ```

3. **提交代码前**：确保所有检查通过
   ```bash
   # 格式化代码
   gofmt -w .
   goimports -w .
   
   # 运行所有检查
   make all
   ```

### 测试
```bash
# 运行单元测试
go test ./...

# 运行带覆盖率的测试
go test -cover ./...

# 运行特定包的测试
go test ./tglog/...
```

### 构建
```bash
# 构建测试程序（Linux 平台）
GOOS=linux go build -o test/test test/test.go

# 清理构建产物
make clean
```

## IDE 配置

### VS Code
推荐安装 Go 扩展，并配置：
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
1. 启用 `goimports` 作为格式化工具
2. 配置保存时自动格式化
3. 启用 `golint` 和 `go vet` 检查

## 常见问题

### Q: golint 命令找不到
A: 确保 `$(go env GOPATH)/bin` 在 PATH 环境变量中：
```bash
echo 'export PATH=$PATH:$(go env GOPATH)/bin' >> ~/.bashrc
source ~/.bashrc
```

### Q: 依赖下载失败
A: 配置 Go 代理：
```bash
go env -w GOPROXY=https://goproxy.cn,direct
go env -w GOSUMDB=off
```

### Q: make all 失败
A: 逐个检查工具是否正确安装：
```bash
which gofmt
which goimports
which golint
go version
```

## 提交规范

提交前确保：
- ✅ 代码已格式化（`gofmt -w .`）
- ✅ Import 语句已整理（`goimports -w .`）
- ✅ 通过静态检查（`go vet ./...`）
- ✅ 通过风格检查（`golint ./...`）
- ✅ 单元测试通过（`go test ./...`）

## 获取帮助

- 查看 [README.md](README.md) 了解项目详情
- 查看 [CHANGELOG.md](CHANGELOG.md) 了解版本历史
- 提交 Issue 报告问题