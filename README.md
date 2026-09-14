<p align="center">
  <img src="build/appicon.png" width="112" alt="MaxKB 本地文件同步工具图标">
</p>

<h1 align="center">MaxKB 本地文件同步工具</h1>

<p align="center">将本地文件夹持续、增量地同步到指定的 MaxKB 知识库。</p>

<p align="center">
  <a href="https://github.com/fit2cloud-east-de/maxkb-local-file-sync/blob/main/LICENSE"><img src="https://img.shields.io/github/license/fit2cloud-east-de/maxkb-local-file-sync" alt="License"></a>
  <a href="https://github.com/fit2cloud-east-de/maxkb-local-file-sync/releases"><img src="https://img.shields.io/github/v/release/fit2cloud-east-de/maxkb-local-file-sync" alt="Release"></a>
  <a href="https://github.com/fit2cloud-east-de/maxkb-local-file-sync"><img src="https://img.shields.io/github/stars/fit2cloud-east-de/maxkb-local-file-sync?style=flat-square" alt="GitHub Stars"></a>
</p>

## 项目介绍

MaxKB 本地文件同步工具是一款运行在 Windows 和 macOS 上的桌面应用。它递归扫描指定文件夹，将新增或变化的文件同步到 MaxKB，并在需要时调用 MinerU 完成文档转换。

应用不依赖额外的中心服务。任务配置、执行队列、文件映射和恢复检查点保存在本机；API Key 和 Token 由操作系统凭据库管理。

![同步任务页面](docs/images/sync-tasks.png)

## 核心能力

- **增量同步**：通过相对路径和流式 MD5 识别新增、修改、删除、未变化和可确认的重命名。
- **灵活筛选**：支持 Include、Exclude 正则规则，并提供限制展示数量的文件匹配预览。
- **MaxKB 集成**：支持工作区、知识库目录、知识库选择，以及文件上传、智能分段和文档创建。
- **MinerU 转换**：支持在线 MinerU 和内网 MinerU，异步提交、状态轮询、ZIP 下载及结果整理。
- **产物管理**：MinerU ZIP 仅保留 `full.md` 和 `images/` 后重新压缩，可立即清理、按批次清理、按时间清理或不自动清理。
- **可靠执行**：同步批次持久化、全局串行执行，支持暂停、继续、停止、异常退出恢复和失败文件重试。
- **人工确认**：无法确定远端是否已执行的上传、创建或删除操作进入“异常处理”，避免盲目重试造成重复文档或误删。
- **后台运行**：Windows 支持最小化到系统托盘；macOS 关闭主窗口后保留顶部菜单栏入口。

## 工作流程

```mermaid
flowchart LR
    A[扫描本地文件夹] --> B[应用 Include / Exclude]
    B --> C{是否需要 MinerU?}
    C -- 否 --> D[直接上传 MaxKB]
    C -- 是 --> E[MinerU 转换]
    E --> F[整理 full.md 和 images]
    F --> G[重新压缩 ZIP]
    G --> D
    D --> H[MaxKB 智能分段]
    H --> I[创建知识库文档]
    I --> J[记录文档 ID 和文件摘要]
```

MaxKB 返回有效文档 ID 后，本地同步即判定成功，不等待 MaxKB 后续的索引、向量化或问题生成完成。

## 文件格式

### MaxKB 直接上传

`.txt`、`.md`、`.markdown`、`.pdf`、`.docx`、`.html`、`.xls`、`.xlsx`、`.csv`、`.zip`

### MinerU 转换

`.pdf`、`.png`、`.jpg`、`.jpeg`、`.bmp`、`.tiff`、`.gif`、`.webp`、`.jp2`、`.docx`、`.pptx`、`.xlsx`

实际支持能力还取决于所连接的 MaxKB、在线 MinerU 或内网 MinerU 版本。任务中的“MinerU 转换范围”决定具体文件走直传还是转换路线：

| 转换范围 | 处理方式 |
| --- | --- |
| MinerU 关闭 | 仅同步 MaxKB 可直接上传的格式 |
| 留空 | MaxKB 支持的格式直接上传，其他格式尝试 MinerU |
| `*` | MinerU 明确支持的格式全部转换，其余 MaxKB 格式直接上传 |
| `.pdf,.pptx` 等 | 命中的格式使用 MinerU，未命中的格式按 MaxKB 直传能力处理 |

完整处理规则和异常场景见[用户手册](USER_GUIDE.md#六文件格式与处理路线)。

## 快速开始

### 1. 安装应用

从 [GitHub Releases](https://github.com/fit2cloud-east-de/maxkb-local-file-sync/releases) 下载对应安装包：

- Windows x64 / ARM64：下载 `.exe` 安装包；安装时可选择当前用户、所有用户和安装目录。
- macOS Apple Silicon / Intel：下载 `.dmg`，将应用拖入 `Applications（应用程序）`。

### 2. 配置服务

进入“系统设置”完成 MaxKB 配置：

1. 填写 MaxKB Base URL，支持 HTTP 或 HTTPS。
2. 填写 User Key / API Key。
3. 测试连接并保存配置。
4. 如需文档转换，再启用并配置 MinerU 服务、产物目录和清理策略。

### 3. 创建任务

进入“同步任务”，点击“新建”，填写任务名称并选择：

- 本地文件夹；
- 目标工作区；
- 目标知识库；
- 定时同步、删除策略和文件筛选规则；
- 是否启用 MinerU，以及转换范围。

任务名称必须唯一；同一个本地文件夹允许创建多个任务。

### 4. 执行与排障

保存任务后点击“立即同步”，或等待 Cron 定时触发。在“执行队列”查看批次和文件处理结果；远端结果不确定的操作在“异常处理”中由人工确认。

![执行队列页面](docs/images/execution-queue.png)

## 页面说明

| 页面 | 用途 |
| --- | --- |
| 同步任务 | 新建、编辑、启用、关闭、删除和立即执行同步任务 |
| 执行队列 | 查看任务分组、批次记录、文件明细、处理阶段和失败原因 |
| 异常处理 | 处理远端结果不确定的上传、文档创建和删除操作 |
| 系统设置 | 配置 MaxKB、MinerU、请求超时、产物目录和清理策略 |

## 可靠性边界

- 普通失败可以在问题修复后重新同步，重试批次只包含上次普通失败的文件。
- 上传、智能分段、文档创建或删除发生超时、断网、TLS 错误、HTTP 429 或服务端错误时，远端结果可能已经生效，因此不会自动重试。
- 无法确认的操作进入“异常处理”，可选择“确认远端成功”“确认不存在并重试”或“标记失败”。
- 客户端只删除自身数据库中保存了文档 ID 的远端文档，不按文件名猜测或删除其他来源的文档。
- 文件处理期间如果本地内容发生变化，远端引用会保留并进入人工确认，避免将错误版本标记为成功。

## 技术栈

- 桌面框架：[Wails v2](https://wails.io/)
- 后端：[Go](https://go.dev/)
- 前端：[Vue 3](https://vuejs.org/)、TypeScript、Vite、Element Plus
- 本地数据库：SQLite（`modernc.org/sqlite`）
- 调度：`robfig/cron`
- 凭据存储：macOS Keychain / Windows Credential Manager

## 本地开发

### 环境要求

- Go 版本以 [`go.mod`](go.mod) 为准
- Node.js 与 npm
- Wails CLI v2
- macOS：Xcode Command Line Tools
- Windows：WebView2、NSIS 及对应架构的编译环境

### 启动开发环境

```bash
git clone git@github.com:fit2cloud-east-de/maxkb-local-file-sync.git
cd maxkb-local-file-sync

cd frontend
npm ci --include=dev
cd ..

wails dev
```

### 代码检查

```bash
cd frontend && npm run build && cd ..
go vet ./...
go test ./... -count=1
```

## 构建安装包

构建产物位于 `dist/` 或 `build/bin/`，默认不提交到 Git 仓库。

### macOS

```bash
# Apple Silicon
./scripts/build-macos-dmg.sh

# Intel
MACOS_ARCH=x64 ./scripts/build-macos-dmg.sh
```

### Windows

```powershell
# x64
.\scripts\build-windows.ps1 -Architecture x64

# ARM64
.\scripts\build-windows.ps1 -Architecture arm64
```

Windows 完整环境准备和排障说明见 [`WINDOWS_BUILD_GUIDE.md`](WINDOWS_BUILD_GUIDE.md)，签名与 macOS 公证说明见 [`SIGNING_GUIDE.md`](SIGNING_GUIDE.md)。

## 数据与安全

默认数据位置：

```text
Windows 安装版: <安装目录>\data
Windows 开发版: %LOCALAPPDATA%\MaxKB\MaxKB 本地文件同步工具\data
macOS:          ~/Library/Application Support/MaxKB/MaxKB 本地文件同步工具/data
```

- MaxKB API Key、在线 MinerU Token 和内网网关 Token 不写入 SQLite、日志或导出文件。
- 系统凭据库不可用时不会降级为明文保存。
- 日志、错误详情和响应摘要会过滤凭据、Cookie、预签名 URL 等敏感内容。
- ZIP 解压会校验路径、符号链接和重复条目，避免路径穿越。
- 升级和卸载默认不删除任务、SQLite、日志及系统凭据。

## 文档

- [用户操作手册](USER_GUIDE.md)
- [Windows 打包手册](WINDOWS_BUILD_GUIDE.md)
- [签名与公证](SIGNING_GUIDE.md)
- [在线更新方案](UPDATE_PLAN.md)
- [构建资源说明](build/README.md)

## License

Copyright (c) FIT2CLOUD 飞致云

本项目遵循 [Apache License 2.0](LICENSE) 开源协议。
