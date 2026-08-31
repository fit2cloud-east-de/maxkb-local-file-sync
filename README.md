# MaxKB 本地文件同步客户端

一个运行在用户电脑上的跨平台桌面客户端，用于将本地文件夹递归、增量地同步到指定的 MaxKB 知识库。客户端不依赖中心服务端，所有任务、队列、映射和日志均保存在本机 SQLite 中。

> 当前项目处于持续完善和契约验证阶段。本地单元测试、MinerU 模拟协议测试与本地构建通过，不等同于已经在真实 MaxKB v2.10.4-lts、在线 MinerU 或锁定版本的内网 MinerU 服务上完成端到端验证。真实环境验证项和构建边界以本文档及 [`UPDATE_PLAN.md`](./UPDATE_PLAN.md) 为准。

## 功能概览

- 本地文件夹递归扫描、隐藏/临时/系统文件过滤和符号链接跳过。
- Include、Exclude 多规则筛选，支持正则及兼容的 glob 写法，并提供匹配预览和排除原因。
- 流式 MD5 计算、快照校验和增量差异识别。
- 支持新增、修改、本地删除和基于唯一 MD5 的重命名识别。
- 支持手动同步和标准 5 段 Cron 定时同步。
- 一个客户端内所有同步批次全局串行执行；单批次内文件串行处理。
- 支持批次暂停、继续、停止和取消排队，并将队列、检查点和恢复信息持久化到 SQLite。
- 支持 MaxKB 连接校验、工作空间、知识库目录、知识库和 Embedding 模型选择。
- 支持 MaxKB 智能分段、批量创建文档、文档状态查询、文档删除和文档链接生成。
- 支持关闭 MinerU、在线 MinerU 和内网 MinerU 两种 Adapter 模式。
- 支持 MinerU 异步任务轮询与结果 ZIP 流式下载；ZIP 不在客户端解压，直接交由 MaxKB 智能分段。
- 支持失败重试、错误分类、异常退出恢复以及 `RECONCILE_REQUIRED` 人工处理入口。
- MaxKB API Key 和 MinerU Token 使用系统凭据库保存，不写入 SQLite 明文。

## 技术栈

- **后端**：Go、Wails v2
- **前端**：Vue 3、TypeScript、Vite
- **UI**：Element Plus、`lucide-vue-next`
- **状态管理与路由**：Pinia、Vue Router
- **数据库**：SQLite，优先使用不依赖 CGO 的 `modernc.org/sqlite`
- **调度**：`robfig/cron` 标准 5 段 Cron
- **凭据存储**：macOS Keychain、Windows Credential Manager（通过 `go-keyring`）

## 项目结构

```text
maxkb-local-file-sync/
├── app.go                         # Wails 对外绑定入口
├── main.go                        # Wails 应用启动入口
├── internal/
│   ├── adapter/                   # MaxKB、在线 MinerU、内网 MinerU Adapter
│   ├── api/                       # 面向前端的 DTO 与 API 门面
│   ├── app/                       # 应用组装、配置和生命周期
│   ├── infra/
│   │   ├── credential/            # 系统凭据库与日志脱敏
│   │   ├── db/                    # SQLite、迁移和嵌入迁移资源
│   │   ├── file/                  # 路径、扫描、快照和 MD5 工具
│   │   └── logger/                # 结构化日志
│   ├── pkg/types/                 # 状态、阶段和领域类型
│   ├── repository/                # 任务、批次、文件、队列和恢复持久化
│   └── service/                   # 扫描、差异、流水线、调度和编排
├── migrations/                    # 可审阅的 SQLite 版本化迁移
├── frontend/
│   └── src/
│       ├── components/             # 状态、任务和日志组件
│       ├── stores/                 # Pinia stores
│       ├── views/                  # 同步任务、队列、对账和设置页面
│       └── router/                 # Vue Router 配置
├── build/                         # Wails 平台构建资源
└── wails.json                     # Wails 项目配置
```

## 开发环境要求

建议在 macOS 或 Windows 上使用以下工具：

- Go 1.25 或与 [`go.mod`](./go.mod) 一致的 Go 版本
- Node.js 与 npm
- Wails CLI v2；当前开发机验证版本为 v2.14.0
- macOS 构建需要 Xcode Command Line Tools；Windows 构建需要对应的 WebView2/编译工具链

本项目不应依赖开发机的绝对路径。`go.mod` 中如存在本地 `replace` 配置，应在交付或 CI 环境中移除或替换为可复现的依赖来源。

## 本地开发

进入项目目录：

```bash
cd /Users/maekblack/VSCodeProjects/maxkb-local-file-sync/maxkb-local-file-sync
```

安装前端依赖：

```bash
cd frontend
npm install
```

启动 Wails 热重载开发模式：

```bash
cd ..
wails dev
```

仅验证前端类型检查和生产构建：

```bash
cd frontend
npm run build
```

仅编译 Go 后端：

```bash
cd ..
go build ./...
```

## 发布安装包

发布脚本位于 `scripts/`，安装目录与应用数据目录分离，安装和卸载不会默认删除任务、映射、日志或系统凭据。

### Windows x64 / ARM64

请在对应 Windows 主机上执行 PowerShell 脚本，并确保已安装 Go、Node.js、Wails CLI 和 NSIS：

```powershell
.\scripts\build-windows.ps1 -Version 1.0.0
```

脚本每次生成一个安装包，安装向导中选择安装范围：

```powershell
# Windows x64
.\scripts\build-windows.ps1 -Version 1.0.0 -Architecture x64
# 输出：dist\windows\MaxKB-Local-File-Sync-v1.0.0-windows-x64-setup.exe

# Windows ARM64
.\scripts\build-windows.ps1 -Version 1.0.0 -Architecture arm64
# 输出：dist\windows\MaxKB-Local-File-Sync-v1.0.0-windows-arm64-setup.exe
```

安装向导支持：

- 仅当前用户安装：默认 `%LOCALAPPDATA%\Programs\MaxKB 本地文件同步工具`，不需要管理员权限；
- 所有用户安装：默认 `%PROGRAMFILES%\MaxKB\MaxKB 本地文件同步工具`，需要管理员权限并触发 UAC；
- 两种范围都支持自定义安装目录、开始菜单快捷方式和桌面快捷方式。

应用本身不会以管理员权限运行，也不会修改系统 PATH 或自动创建防火墙规则。正式发布时可通过脚本的 `-Sign` 参数使用 Windows 代码签名证书。

### macOS Intel / Apple Silicon

在 macOS 主机上执行：

```bash
./scripts/build-macos-dmg.sh
```

生成：

```bash
# Apple Silicon
./scripts/build-macos-dmg.sh
# 输出：dist/macos/MaxKB-Local-File-Sync-v1.0.0-macos-arm64.dmg

# Intel Mac
MACOS_ARCH=x64 ./scripts/build-macos-dmg.sh
# 输出：dist/macos/MaxKB-Local-File-Sync-v1.0.0-macos-x64.dmg
```

DMG 采用 Finder 标准拖拽安装：双击 DMG 挂载镜像，将应用拖到 `Applications`，等待复制完成后推出镜像，再从“应用程序”打开“MaxKB 本地文件同步工具”。脚本会在 DMG 中附带中文安装说明，不使用 `.pkg` 安装器。

正式签名、公证和 GitHub Actions 凭据准备说明见 [`SIGNING_GUIDE.md`](./SIGNING_GUIDE.md)。签名和公证必须在受保护的发布环境中执行。

正式签名和公证可通过环境变量启用：

```bash
CODESIGN_IDENTITY="Developer ID Application: ..." \
NOTARY_PROFILE="你的 notarytool keychain profile" \
./scripts/build-macos-dmg.sh
```

未配置签名时只能作为开发或内部分发包，Gatekeeper 可能提示来源未知。签名、公证和真实安装启动必须在目标 Mac 上完成。

### 应用数据目录

安装目录只保存程序文件，用户数据使用操作系统标准目录：

```text
Windows: %LOCALAPPDATA%\MaxKB\MaxKB 本地文件同步工具
macOS:   ~/Library/Application Support/MaxKB/MaxKB 本地文件同步工具
```

数据目录包含 `data/`（SQLite）、`snapshots/`、`logs/`、`temp/` 和 `backups/`。旧版本的 `~/.maxkb-sync` 会在首次启动时迁移到新目录；如果新目录已存在则不会覆盖旧数据。卸载默认保留这些用户数据，删除数据和系统凭据需要用户单独确认。

### 发布校验

构建后可执行：

```bash
./scripts/verify-release.sh
```

发布目录会生成 SHA-256 校验文件。安装包不得携带真实 API Key、Token、Cookie、用户资料、SQLite 数据库或业务文件。

## 构建

### macOS

```bash
cd /Users/maekblack/VSCodeProjects/maxkb-local-file-sync/maxkb-local-file-sync
wails build -clean
```

产物通常位于 `build/bin/`。当前环境已成功完成 macOS Apple Silicon 的 Wails `.app` 构建，并通过 DMG 生成和元数据校验。代码签名、公证和首次启动仍需在具有对应 Apple Developer 身份的目标环境中执行。构建限制和签名说明见 [`build/README.md`](./build/README.md) 与 [`SIGNING_GUIDE.md`](./SIGNING_GUIDE.md)。

### Windows

推荐使用上面的 `scripts\build-windows.ps1`，它会生成一个可在安装过程中选择“当前用户”或“所有用户”的 NSIS 安装包。当前开发机已验证 Go Windows amd64 交叉编译，但没有 Windows 主机和 NSIS，因此 Windows Wails GUI、WebView2、NSIS 安装向导、UAC、Credential Manager 和卸载升级尚未在本机完成实机验证。

## 数据目录、日志和快照

应用启动时默认使用当前用户主目录下的私有目录：

```text
Windows: %LOCALAPPDATA%\MaxKB\MaxKB 本地文件同步工具\data\       SQLite 数据库
macOS:   ~/Library/Application Support/MaxKB/MaxKB 本地文件同步工具/data/       SQLite 数据库
各平台同一应用数据根目录下还包含 snapshots/、logs/、temp/ 和 backups/
```

应用会在启动时执行嵌入的版本化迁移。迁移失败时应停止启动并保留原数据库，不应通过删除数据库或跳过迁移来恢复。测试代码可使用快速初始化路径，但生产启动使用迁移链。

### SQLite 迁移

迁移文件按版本递增：

- `000001_init`
- `000002_reliability`
- `000003_recovery_audit`
- `000004_task_control`
- `000005_database_hardening`
- `000006_foreign_key_hardening`
- `000007_system_mineru_artifacts`
- `000008_mineru_cleanup_policy`

升级时不要手动删除 SQLite 文件，也不要直接修改已发布迁移。新增数据库结构必须添加新的 `NNNNNN_*.up.sql` 和对应的回滚说明，并同时更新嵌入迁移资源与迁移测试。

## 配置和凭据安全

### MaxKB

在“系统设置”中配置：

- MaxKB Base URL（保存前校验 URL，并去除末尾 `/`）
- MaxKB User Key/API Key
- 连接测试和最近一次校验结果

请求只使用必要的 `Authorization`、`Accept` 以及 JSON 请求所需的 `Content-Type`。不会复制浏览器 Cookie、Referer、Origin、User-Agent 或 `Sec-Fetch-*` 请求头，也不会自动绕过 TLS 校验。

### MinerU

支持关闭、在线 MinerU 和内网 MinerU。Adapter 已隔离以下协议差异：

- 在线 MinerU：申请预签名上传地址、PUT 上传、查询批次结果、下载 ZIP。
- 内网 MinerU：`/health`、`/tasks`、`status_url` 和 `result_url` 异步协议。

在线 MinerU Token 和内网网关 Bearer Token 只允许进入系统凭据库。系统凭据库不可用时不降级为明文保存。日志、错误、数据库、导出内容和测试数据不得包含真实 Token、Cookie、用户资料或业务文件内容。

#### MinerU 产物保存与清理

MinerU 返回的结果 ZIP 由“系统设置”统一管理，不在同步任务中重复配置：

- MinerU 开启时必须选择产物保存根目录；原始结果 ZIP 默认保留，客户端不会解压或改写 ZIP，保存结构为 `{根目录}/{安全任务名}/{批次 ID}/{源文件相对目录}/{源文件名}/`。
- “立即清理”表示本批次只在应用私有临时目录中使用 ZIP，完成当前文件安全检查点后删除，不发布到产物目录。
- 自动清理支持“按批次清理”（每个任务保留最近 N 个批次）、“按时间清理”（保留最近 N 小时或 N 天）和“不自动清理”（默认保留，仍可手动清理）。自动策略使用标准 5 段 Cron，按操作系统时区执行。
- 手动“立即清理”会按当前策略删除可清理的已完成批次；活动、暂停、异常恢复中的批次和未知目录均受保护。
- 清理只删除能通过本地 `sync_runs.id` 关联到的批次目录，不按名称猜测，也不会调用 MaxKB 删除接口。
- 保存过程使用应用私有临时目录、流式复制和原子发布；目录名经过跨平台安全处理，并拒绝路径穿越和符号链接。

## 同步规则

- 文件身份为 `task_id + normalized_relative_path`；MD5 仅用于判断内容是否变化。
- Include 为空时默认包含全部候选文件；Include 非空时匹配任意规则才进入候选集；Exclude 最后执行并拥有最高优先级。
- 文件内容变化时先删除本任务记录的旧 MaxKB 文档，删除成功或 404 后才上传新内容。
- 开启“同步本地删除”时，只删除本任务数据库中保存的远端文档 ID，不按文件名删除，也不触碰其他客户端的文档。
- 关闭该开关时，本地缺失文件标记为 `LOCAL_MISSING_REMOTE_KEPT`；相同路径和 MD5 的文件重新出现时不重复上传。
- 暂停和停止只在安全检查点生效，不强行中断当前文件的原子远端操作。
- 不明确的远端结果进入 `RECONCILE_REQUIRED`，继续操作前先进行远端核对。

## 批次状态

持久化批次状态包括：

```text
QUEUED -> RUNNING
RUNNING -> PAUSE_REQUESTED -> PAUSED
PAUSED -> QUEUED -> RUNNING
RUNNING -> STOP_REQUESTED -> STOPPED
PAUSED -> STOPPED
RUNNING -> SUCCESS / PARTIAL_SUCCESS / FAILED / INTERRUPTED
QUEUED -> CANCELLED
```

文件级处理阶段和最终状态独立保存。应用异常退出后，遗留的 `RUNNING`、`PAUSE_REQUESTED`、`STOP_REQUESTED` 批次进入 `INTERRUPTED`，已暂停批次保持 `PAUSED`；恢复流程会使用检查点、文件尝试记录和远端引用进行恢复或人工处理，而不是无条件全量重传。

项目仍保留 `COMPLETED` 作为旧数据兼容状态；新文档和新流程应以 `SUCCESS` 等需求定义状态为准。

## 前端页面

当前界面包含：

- **同步任务**：任务列表、启用/关闭、立即同步、编辑、文件匹配预览和文件状态入口。
- **执行队列**：队列统计、批次详情、按状态查看文件、暂停/继续/停止。
- **异常处理**：处理 `RECONCILE_REQUIRED` 文件，人工确认远端成功、远端不存在后重试或标记失败。
- **系统设置**：MaxKB 和 MinerU 配置、连接测试、凭据掩码显示，以及 MinerU 产物保存、定时清理和手动清理。

界面采用白色/浅灰背景、`#5A55FA` 主色、紧凑表格和克制的阴影，不复制 MaxKB 营销页面布局或未授权品牌素材。

## 测试与审计

本地已执行并通过：

```bash
GOCACHE=/tmp/maxkb-go-cache go vet ./...
GOCACHE=/tmp/maxkb-go-cache go test ./... -count=1
GOCACHE=/tmp/maxkb-go-cache go test -race ./... -count=1
cd frontend && npm run build
GOOS=windows GOARCH=amd64 GOCACHE=/tmp/maxkb-go-cache go build ./...
GOCACHE=/tmp/maxkb-go-cache go build -v .
```

历史上曾尝试过不经过 Wails packaging 的手工生产链接命令；该命令曾因本机 SDK 符号问题失败，不代表当前 Wails 构建结果。当前应以 `wails build -clean -platform darwin/arm64` 及 `./scripts/build-macos-dmg.sh` 的结果为准。

这些结果覆盖 Go 单元/集成测试、静态检查、前端类型检查和构建、Windows Go 交叉编译，以及本轮新增的同步任务快照仓储测试。尚未完成的验证包括：真实 MaxKB v2.10.4-lts 端到端调用、真实在线/内网 MinerU 调用、Windows Wails GUI 启动、macOS 完整 `.app` 启动、Keychain/Credential Manager 实机读写、指定分辨率的浏览器视觉回归。

## 已知限制与待验证项

1. 已根据 MaxKB v2.10.4-lts 锁定源码确认 `batch_create` 的 `data` 为 `[document_records, knowledge_id, workspace_id]`，并从 `document_records[].id` 提取文档 ID；仍需在真实实例核对文档状态字段、`source_file_id` 及部分错误响应。
2. `QueryBatchStatus` 和 `UploadDocument` 保留 legacy 兼容路径；其中批次状态 endpoint 不在给定需求契约中，不能视为真实接口已确认。
3. 主要源文件上传、MaxKB multipart、MinerU 结果下载和 MaxKB 智能分段路径已改为基于文件/`io.Reader` 的流式处理；legacy `[]byte` 适配器方法仍保留兼容路径，Markdown 图片解析和 `batch_create` 段落 JSON 仍会按文档结构占用内存，因此不能宣称所有场景完全零拷贝。
4. 在线 MinerU 和锁定版本内网 MinerU 的真实响应字段、状态枚举、ZIP 目录结构、URL 生命周期和限制尚未实测。
5. 当前 Wails UI 尚未完成目标三种分辨率的截图级回归验证。
6. 复杂目录、超大文件、权限受限目录和企业代理环境需要进一步现场验证。

## 真实环境验证

在连接真实服务前，请准备不包含敏感业务内容的测试知识库、虚构或专用测试凭据以及可删除的测试目录。真实环境验证需要记录 MaxKB/MinerU 的实际响应，不能用本地模拟契约测试替代。

## 相关文档

- [`../DESIGN.md`](../DESIGN.md)、[`../DESIGN_V2.md`](../DESIGN_V2.md)：设计演进记录。

## 许可证

当前仓库未声明正式开源许可证。发布前请补充许可证、第三方依赖归属和发行版本策略。

## MaxKB 异步结果处理

`batch_create` 成功返回文档记录后，MaxKB 仍可能在后台继续完成智能分段、向量化等处理。客户端不会把“已收到文档 ID”误认为最终完成，而是按以下方式异步收敛本地状态：

- 对文件处理阶段为 `MAXKB_SPLITTING`、`MAXKB_CREATING` 或 `MAXKB_PROCESSING` 且最终状态为 `RECONCILE_REQUIRED` 的记录，后台异常处理服务默认每 2 秒查询一次对应知识库的文档列表。
- 只使用本地持久化的 `document_id` 或 `source_file_id` 认领远端文档；不会按文件名接管文档，也不会在远端暂时查不到时盲目重复 `batch_create`。
- MaxKB v2.10.4-lts 的聚合状态由 Adapter 解析。已根据锁定源码验证，四字符且仅由 `2`（成功）和 `n/N`（忽略）组成的状态（例如 `nnnn`）视为成功；其他未知状态继续等待或进入人工处理，不猜测为成功。
- 查询到远端成功后，客户端自动更新本地文档 ID、成功 MD5、文件状态、文件尝试和批次状态，并由前端继续轮询队列统计以刷新页面。
- 远端未找到、状态处理中、状态未知、身份冲突或多条 `source_file_id` 匹配时，不自动重传，保留“异常处理”人工处理入口。

该机制只在 MaxKB 配置已完成连接校验且凭据可从系统凭据库恢复时运行。真实 MaxKB 实例的状态字段、分页和 `meta.source_file_id` 形态仍需在目标环境验证；本地模拟契约测试不等同于真实服务端到端验证。
