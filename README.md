<p align="center">
  <h1 align="center">MaxKB 本地文件同步工具</h1>
</p>

<p align="center">将本地文件夹中的文件递归、增量地同步到指定的 MaxKB 知识库。</p>

<p align="center">
  <a href="https://github.com/fit2cloud-east-de/maxkb-local-file-sync/blob/main/LICENSE"><img src="https://img.shields.io/github/license/fit2cloud-east-de/maxkb-local-file-sync" alt="License"></a>
  <a href="https://github.com/fit2cloud-east-de/maxkb-local-file-sync/releases"><img src="https://img.shields.io/github/v/release/fit2cloud-east-de/maxkb-local-file-sync" alt="Release"></a>
  <a href="https://github.com/fit2cloud-east-de/maxkb-local-file-sync"><img src="https://img.shields.io/github/stars/fit2cloud-east-de/maxkb-local-file-sync?style=flat-square" alt="GitHub Stars"></a>
</p>

## 项目简介

MaxKB 本地文件同步工具是一款运行在用户电脑上的跨平台桌面应用，面向需要将本地资料持续同步到 MaxKB 知识库的场景。

应用不依赖中心服务端：同步任务、执行队列、文件映射、批次日志和恢复检查点均保存在本机 SQLite 中。客户端只管理自己数据库中记录的远端文档，不会按文件名删除文档，也不会影响其他用户或其他客户端创建的内容。

## 功能特性

- **增量同步**：递归扫描本地文件夹，使用相对路径和流式 MD5 识别新增、修改、删除、未变化和可识别的重命名。
- **灵活筛选**：支持 Include、Exclude 多条正则规则，统一使用相对根目录的 `/` 路径匹配，并提供文件匹配预览及排除原因。
- **MaxKB 集成**：支持连接校验、工作空间选择、知识库目录选择、知识库选择、Embedding 模型选择、文档上传和文档状态查询。
- **MinerU 集成**：支持关闭 MinerU、在线 MinerU 和内网 MinerU；通过 Adapter 隔离不同服务协议，并支持异步任务轮询、失败重试和结果 ZIP 下载。
- **ZIP 产物直传**：MinerU 生成的 ZIP 默认不在客户端解压，直接交由 MaxKB 处理；可按系统设置保存产物，并支持按批次或按时间清理。
- **任务调度**：支持手动立即同步和标准 5 段 Cron 定时同步，时区跟随操作系统。
- **可靠队列**：客户端内所有同步批次全局串行执行，单个任务内文件串行处理；队列和检查点持久化，支持异常退出恢复。
- **批次控制**：支持暂停、继续、停止和取消排队。暂停或停止在当前文件到达安全检查点后生效，不回滚已经完成的远端操作。
- **异常处理**：不确定的上传、批次创建或删除操作不会自动重试，必须由人工明确决策后处理；客户端保留必要的远端引用和本地状态用于后续处理。
- **凭据保护**：MaxKB API Key、在线 MinerU Token 和内网网关 Token 通过 macOS Keychain 或 Windows Credential Manager 保存，不写入 SQLite 明文、日志或导出文件。

## 支持的文件类型

MaxKB 端支持直接上传的文件类型为：

- TXT
- Markdown
- PDF
- DOCX
- HTML
- XLS / XLSX
- CSV
- ZIP

其他文件类型可以在启用并正确配置 MinerU 后，先通过 MinerU 转换，再将转换结果 ZIP 上传到 MaxKB。具体支持范围以实际 MaxKB、在线 MinerU 和内网 MinerU 服务的版本及配置为准。

## 快速开始

### 1. 获取应用

从 GitHub Releases 下载对应平台的安装包：

- Windows：`.exe` 安装包；安装时选择当前用户或所有用户，并可自定义安装目录。
- macOS：`.dmg` 安装包；支持 Apple Silicon 和 Intel 版本。

当前仓库主要托管源代码和发布文档，正式安装包应以 Releases 页面中的版本资产为准。

### 2. 配置 MaxKB

启动应用后进入“系统设置” → “MaxKB 配置”：

1. 填写 MaxKB Base URL。
2. 填写 User Key / API Key。
3. 点击“测试连接”，确认地址、凭据、License 和版本信息有效。
4. 点击“保存配置”。

Base URL 保存前会去除末尾 `/`，但保留用户配置的 HTTP 或 HTTPS，不会自动绕过 TLS 校验。

### 3. 配置 MinerU（可选）

如果同步范围包含 MaxKB 不直接支持的文件类型，可以在“系统设置” → “MinerU 配置”中启用 MinerU，然后：

1. 选择在线 MinerU 或内网 MinerU。
2. 配置服务地址和访问 Token（如服务需要）。
3. 配置产物保存目录。启用 MinerU 时该目录为必填项。
4. 选择清理策略：不自动清理、按批次清理或按时间清理。
5. 点击“测试连接”，确认服务可访问后保存配置。

已经保存的 MinerU Token 会由系统凭据库复用；关闭 MinerU 后再次开启，不需要重复输入 Token。

### 4. 创建同步任务

在“同步任务”页面点击“新建”：

1. 选择一个本地文件夹。
2. 选择目标工作空间和知识库目录。
3. 选择一个知识库。
4. 配置 Cron、删除策略、Include / Exclude 规则以及 MinerU 转换范围。
5. 保存任务并点击“立即同步”，或等待定时调度执行。

一个任务只绑定一个本地文件夹和一个 MaxKB 知识库。同一个客户端中，同一本地文件夹或同一个知识库不能被多个任务重复绑定。

## 应用界面

应用包含以下主要页面：

- **同步任务**：查看任务、启用或关闭任务、立即同步、编辑配置和查看执行记录。
- **执行队列**：以任务为单元查看排队和执行状态，进入后查看不同批次及文件明细。
- **同步记录**：查看批次概览、单文件阶段、成功与失败信息、MaxKB 文档链接和 MinerU 处理信息。
- **异常处理**：处理状态不明确的上传、批次创建、删除或远端状态核对事项。
- **系统设置**：配置 MaxKB、MinerU、产物保存目录和自动清理策略。

## 技术栈

- **桌面框架**：[Wails v2](https://wails.io/)
- **后端**：[Go](https://go.dev/)
- **前端**：[Vue 3](https://vuejs.org/)、TypeScript、Vite
- **UI 组件**：[Element Plus](https://element-plus.org/)、[`lucide-vue-next`](https://github.com/lucide-icons/lucide)
- **状态管理与路由**：[Pinia](https://pinia.vuejs.org/)、[Vue Router](https://router.vuejs.org/)
- **数据库**：SQLite，使用 `modernc.org/sqlite`，避免依赖 CGO
- **调度**：[`robfig/cron`](https://github.com/robfig/cron)
- **凭据存储**：[`go-keyring`](https://github.com/zalando/go-keyring)，对接 macOS Keychain 和 Windows Credential Manager

## 系统要求

### 运行应用

- macOS：Apple Silicon 或 Intel；正式分发建议使用签名并完成公证的 DMG。
- Windows：x64 或 ARM64；需要系统支持 WebView2 运行环境。

### 开发和构建

- Go 版本以 [`go.mod`](./go.mod) 为准。
- Node.js 与 npm。
- Wails CLI v2。
- macOS 构建需要 Xcode Command Line Tools。
- Windows 构建需要 Go、Node.js、Wails CLI、NSIS 及对应的 Windows 编译工具链。

## 本地开发

克隆仓库后进入项目目录：

```bash
git clone git@github.com:fit2cloud-east-de/maxkb-local-file-sync.git
cd maxkb-local-file-sync
```

安装前端依赖：

```bash
cd frontend
npm install
```

启动 Wails 开发模式：

```bash
cd ..
wails dev
```

前端生产构建：

```bash
cd frontend
npm run build
```

Go 静态检查、测试和编译：

```bash
cd ..
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
go build ./...
```

## 构建安装包

构建脚本位于 [`scripts/`](./scripts/)，构建产物位于 `dist/` 或 `build/bin/`，不会提交到 Git 仓库。

### macOS DMG

在 macOS 主机执行：

```bash
# Apple Silicon
./scripts/build-macos-dmg.sh

# Intel
MACOS_ARCH=x64 ./scripts/build-macos-dmg.sh
```

产物：

```text
dist/macos/MaxKB-Local-File-Sync-v<版本>-macos-arm64.dmg
dist/macos/MaxKB-Local-File-Sync-v<版本>-macos-x64.dmg
```

DMG 采用 macOS 常见的 Finder 拖拽安装方式：双击 DMG，将应用拖入 `Applications`，复制完成后推出镜像，再从“应用程序”打开应用。

正式发布时应配置 Developer ID 签名和 Apple 公证。具体环境变量和验证命令见 [`SIGNING_GUIDE.md`](./SIGNING_GUIDE.md)。

### Windows EXE

在 Windows 主机执行：

```powershell
# x64
.\scripts\build-windows.ps1 -Architecture x64

# ARM64
.\scripts\build-windows.ps1 -Architecture arm64
```

每个平台生成一个 NSIS `.exe` 安装包。安装向导中可以选择：

- 仅当前用户安装；
- 所有用户安装；
- 自定义安装目录。

安装目录与用户数据目录分离。升级或卸载应用时，不会默认删除任务、映射、日志、SQLite 数据和系统凭据。

Windows 安装包签名示例：

```powershell
.\scripts\build-windows.ps1 `
  -Architecture x64 `
  -Sign `
  -CertificateFile C:\secure\maxkb-signing.pfx `
  -CertificatePassword $env:WINDOWS_CERT_PASSWORD
```

证书和密码只能通过受保护的发布环境提供，禁止提交到仓库。更多签名说明见 [`SIGNING_GUIDE.md`](./SIGNING_GUIDE.md)。

### 发布前校验

```bash
./scripts/verify-release.sh
```

该脚本用于检查发布目录和 SHA-256 校验文件。发布包不得包含真实 API Key、Token、Cookie、用户资料、业务文件、SQLite 数据库或日志。

## 数据目录

应用程序文件和用户数据分开保存。默认数据目录为：

```text
Windows: %LOCALAPPDATA%\MaxKB\MaxKB 本地文件同步工具
macOS:   ~/Library/Application Support/MaxKB/MaxKB 本地文件同步工具
```

数据目录通常包含：

```text
data/       SQLite 数据库
snapshots/  文件扫描快照
logs/       应用日志
temp/       临时文件
backups/    数据库备份
```

应用启动时会执行版本化 SQLite 迁移。迁移失败时应停止启动并保留原数据库，不应通过删除数据库或跳过迁移来恢复。

## 安全说明

- MaxKB API Key、在线 MinerU Token、内网网关 Token 不写入 SQLite 明文、日志或导出文件。
- macOS 使用 Keychain，Windows 使用 Credential Manager；系统凭据库不可用时不会降级为明文保存。
- 凭据输入框默认隐藏，已保存凭据不会完整回显。
- HTTP 请求只发送必要请求头，不复制浏览器 Cookie、Referer、Origin、User-Agent 或 `Sec-Fetch-*` 请求头。
- 不自动绕过 TLS 校验。
- 预签名上传 URL、Authorization、Token 和服务端错误中的敏感信息不会进入普通日志。
- ZIP 处理使用安全路径校验，避免路径穿越。
- 删除任务、修改知识库和切换本地文件夹只修改客户端自身映射，不会删除不属于本客户端管理的远端文档。

## 在线更新策略

项目后续以 GitHub Releases 作为正式发布源，更新方案遵循以下原则：

- PATCH 和 MINOR 版本可以在应用内检查、下载并引导升级。
- MAJOR 版本涉及运行时、数据库或权限不兼容时，提示用户重新安装。
- 更新包下载到应用私有临时目录后，先校验平台、架构、版本、SHA-256 和代码签名，再启动外部安装器或升级器。
- 更新过程不删除用户数据、SQLite、日志、任务映射和系统凭据。
- 同步批次执行期间不强制退出应用；升级失败时保留当前可运行版本。

当前仓库的详细发布与升级设计见 [`UPDATE_PLAN.md`](./UPDATE_PLAN.md)。

## 真实服务验证

本地单元测试、模拟契约测试和构建验证不能替代真实服务端到端验证。接入生产或测试环境前，请使用不包含敏感业务内容的测试文件、专用测试知识库和虚构或专用凭据。

仍需在真实环境重点验证：

- MaxKB v2.10.4-lts 的工作空间、知识库、文档列表分页和文档状态字段。
- MaxKB `split`、`batch_create`、文档删除及异步处理结果。
- 在线 MinerU 的上传地址、批次状态、结果 ZIP 地址和状态枚举。
- 项目锁定版本内网 MinerU 的 `/health`、`/tasks`、`status_url`、`result_url` 和 ZIP 响应。
- Windows x64 / ARM64 的安装、权限、WebView2、升级和卸载行为。
- macOS x64 / arm64 的安装、签名、公证、首次启动和升级行为。

遇到无法从需求、真实响应或指定源码确认的接口行为时，客户端会将协议解析隔离在 Adapter 中，不根据名称或未确认字段猜测成功结果。

## 项目结构

```text
maxkb-local-file-sync/
├── app.go                         # Wails 对外绑定入口
├── main.go                        # 应用启动入口
├── internal/
│   ├── adapter/                   # MaxKB、在线 MinerU、内网 MinerU Adapter
│   ├── api/                       # 面向前端的 DTO 与 API 门面
│   ├── app/                       # 应用组装、配置和生命周期
│   ├── core/                      # 扫描、差异、流水线、队列、调度和状态机
│   ├── infra/                     # 数据库、凭据、文件、HTTP、日志和平台能力
│   └── pkg/types/                 # 状态、阶段和领域类型
├── migrations/                    # SQLite 版本化迁移
├── frontend/src/                  # Vue 3 页面、组件、Store 和路由
├── build/                         # Wails 平台构建资源
├── scripts/                       # macOS DMG、Windows EXE 和发布校验脚本
└── wails.json                     # Wails 应用配置
```

## 相关文档

- [`SIGNING_GUIDE.md`](./SIGNING_GUIDE.md)：Windows 和 macOS 签名、公证及发布凭据说明。
- [`UPDATE_PLAN.md`](./UPDATE_PLAN.md)：GitHub Releases 在线更新和版本升级方案。
- [`build/README.md`](./build/README.md)：Wails 构建资源及平台构建边界。
- [`frontend/README.md`](./frontend/README.md)：前端开发说明。

## 许可证

本项目采用 [Apache License 2.0](./LICENSE) 授权。
