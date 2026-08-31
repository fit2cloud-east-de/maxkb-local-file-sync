# 需求与文档覆盖审计

审计对象：`MaxKB 本地文件同步客户端` 代码仓库及其 Markdown 文档。

审计日期：2026-08-26

审计范围：

- 完整产品需求提示词中的第一至第二十八节要求。
- 父目录 Markdown：`DESIGN.md`、`DESIGN_V2.md`、`DESIGN_V3_TODO.md`、`DESIGN_DECISIONS.md`、`PHASE0_START.md`、`PHASE2_COMPLETE.md`、`PHASE3_COMPLETE.md`、`REVISION_PLAN.md`。
- 项目目录 Markdown：`README.md`、`GAP_ANALYSIS.md`、`IMPLEMENTATION_ROADMAP.md`、`PHASE1_COMPLETE.md`、`PHASE4_COMPLETE.md`、`PHASE5_COMPLETE.md`、`PHASE_6_COMPLETION.md`、`PREVIEW_FEATURE_STATUS.md`、`FILE_ACCESS_PERMISSION.md`，以及 `frontend/README.md` 和 `build/README.md`。
- 当前 Go、Vue、迁移和测试目录，用于核对文档是否仍反映当前实现。

## 一、审计结论

### 1. README 状态

原 `maxkb-local-file-sync/README.md` 是 Wails Vue-TS 默认模板，只包含模板简介、`wails.json` 提示、`wails dev` 和 `wails build`。它没有说明产品目标、MaxKB/MinerU、同步规则、凭据安全、数据库迁移、测试结果、已知限制或真实契约验证状态。

本次已将其改写为项目真实 README，补充：

- 产品定位与功能概览。
- 技术栈和目录结构。
- 开发、构建、测试命令。
- SQLite 数据目录、迁移和恢复说明。
- MaxKB/MinerU 配置与凭据安全边界。
- 增量同步、任务控制和批次状态。
- 前端页面说明。
- 已通过测试、未执行测试和构建限制。
- MaxKB v2.10.4-lts、在线 MinerU、内网 MinerU 的待验证项。

### 2. 文档体系状态

文档数量较多，但此前缺少一份以当前代码为准的入口文档。历史阶段文档存在以下问题：

- `PHASE1_COMPLETE.md`、`PHASE2_COMPLETE.md`、`PHASE3_COMPLETE.md` 等记录的是阶段完成时的快照，部分状态名、表名和 TODO 已落后于当前实现。
- `GAP_ANALYSIS.md` 中仍保留早期“缺失”结论，例如文件匹配预览、默认忽略规则、暂停/继续和部分 MinerU 能力；这些项目后来已有不同程度的实现，但真实服务契约和 UI 完整性仍不能据此直接宣称完成。
- `DESIGN.md`、`DESIGN_V2.md`、`DESIGN_V3_TODO.md`、`REVISION_PLAN.md` 同时存在，设计演进关系没有在 README 中集中说明，容易让读者把历史 TODO 当成当前未实现代码。
- `frontend/README.md` 仍是 Vue/Vite 默认模板说明；它不影响项目 README，但不应作为产品使用说明。
- `build/README.md` 原主要描述 Wails 默认构建目录，本次已补充当前项目的 macOS/Windows 验证边界。

本次没有改写历史阶段文档，避免伪造历史记录；当前状态、缺口和测试结论集中写入本文件，并由项目 README 链接作为唯一审计入口。

## 二、需求覆盖矩阵

标记含义：

- **已实现/有测试**：代码中存在对应路径，且已有本地测试或静态检查支持。
- **部分实现**：有代码路径，但仍有需求差异、兼容路径或 UI/恢复语义缺口。
- **待真实验证**：模拟测试可覆盖结构，但必须连接指定版本服务确认。
- **未完成验证**：代码或文档不能证明已经满足。

| 需求区域 | 当前覆盖判断 | 主要依据 | 需要补充的文档内容 |
|---|---|---|---|
| 产品目标、非目标 | 已覆盖 | README、设计文档 | 增加版本范围和发布边界 |
| MaxKB 配置与 Base URL | 部分实现 | `maxkb` Adapter、配置 API | 记录保存草稿、配置不可用和重新校验行为 |
| MaxKB profile 校验 | 待真实验证 | `MaxKBAdapter`、模拟测试 | 记录真实 `code`、license、version 响应契约 |
| 凭据安全 | 已实现/需实机验证 | credential store、sanitizer | 增加 Keychain/Credential Manager 实机验收步骤 |
| MinerU 公共配置 | 部分实现 | MinerU Adapter、配置 API | 明确重试/超时/结果保存等设置是否已进入 UI |
| 在线 MinerU | 部分实现/待真实验证 | online Adapter、测试 | 记录真实 batch、URL、状态和限制字段 |
| 内网 MinerU | 部分实现/待真实验证 | internal Adapter、测试 | 记录锁定版本 `/health`、`/tasks` 和 ZIP 契约 |
| 任务模型与唯一约束 | 部分实现 | SQLite 迁移、repository | 补充“文件夹/知识库单任务绑定”的可验证 SQL 约束和错误提示 |
| Include/Exclude 筛选 | 已实现/有测试 | pattern、scanner、preview | 补充路径示例、正则与 glob 的兼容边界 |
| 默认忽略和符号链接 | 已实现/有测试 | file scanner | 补充 macOS/Windows 系统文件样例和权限异常行为 |
| MaxKB 工作空间/知识库 | 部分实现/待真实验证 | MaxKB Adapter | 记录分页、`type=0`、权限和模型可用状态的真实样例 |
| MaxKB 文档上传流程 | 部分实现/待真实验证 | sync executor、MaxKB Adapter、`maxkb_test.go` httptest 契约测试 | 真实环境确认 source file ID、document ID、状态字段和 batch_create 返回结构 |
| MinerU→ZIP→MaxKB | 已实现/待真实验证 | MinerU 测试、执行器 | 验证 MaxKB v2.10.4-lts 对 MinerU ZIP 的智能分段响应和失败恢复 |
| 增量同步 | 已实现/有测试 | scanner、snapshot、executor、repository | 补充重命名唯一 MD5 判定和源文件变化对账规则 |
| 任务启用/关闭 | 部分实现 | task control、cron、UI | 补充关闭时队列、运行批次、暂停批次的验收步骤 |
| 暂停/继续/停止/取消排队 | 已实现核心/需加强验证 | state machine、reliability store、UI | 补充安全检查点、重启后恢复和禁止重复远端操作的时序图 |
| 异常退出恢复 | 部分实现 | checkpoint、recovery、attempts | 补充每个阶段的恢复策略和人工处理入口 |
| 全局串行与 Cron | 部分实现/需验证 | orchestrator、cron | 明确 5 段 Cron、时区、去重和 next execution 更新行为 |
| 错误分类与重试 | 已实现核心 | MaxKB/MinerU error types | 补充错误分类表、重试上限和用户提示映射 |
| 日志与脱敏 | 已实现核心/需审计 | logger、sanitizer | 补充日志字段白名单和禁止记录字段清单 |
| 数据库与迁移 | 已实现/有测试 | 000001—000006 migrations、嵌入迁移和迁移链测试 | 补充升级、失败回滚、备份和版本兼容操作手册 |
| UI 信息架构 | 部分实现 | Vue views/components | 补充任务编辑字段、设置字段、分辨率截图和无障碍验收记录 |
| 自动化/契约测试 | 部分实现/待真实验证 | Go tests、MaxKB/MinerU httptest 契约测试 | 补充真实服务测试记录和边界矩阵 |
| macOS/Windows 构建 | 部分验证 | Go build、交叉编译 | 记录 Wails `.app`、Windows GUI 和安装包启动验收结果 |
| 安全与非目标 | 已覆盖原则 | README、设计文档 | 补充威胁模型、备份策略和发布前安全检查 |

## 三、已实现且文档应保持一致的内容

以下内容已经在当前代码或本地测试中有依据，项目 README 已按此描述：

1. SQLite 生产启动使用嵌入迁移，测试保留快速初始化路径。
2. 数据库启用外键、WAL、busy timeout，并持久化队列和 checkpoint。
3. 文件扫描不跟随符号链接；路径、扩展名和 MD5 处理有独立测试。
4. Include/Exclude 规则、扩展名规范化、匹配预览和排除列表已有实现。
5. MaxKB 和 MinerU 通过 Adapter 隔离，错误分类和请求超时/重试逻辑集中在 Adapter。
6. 批次状态具有显式迁移校验，暂停和停止使用请求态并在安全检查点生效。
7. 本地删除只使用本任务持有的远端文档 ID；删除任务本身不执行远端删除。
8. 不确定的远端操作可进入 `RECONCILE_REQUIRED`，前端提供异常处理。
9. API Key/Token 不以明文写入 SQLite；测试使用虚构凭据。
10. `robfig/cron` 已改为标准 5 段解析，不使用 `WithSeconds()`。

## 四、必须补充或修订的文档内容

### P0：当前交付必须有

1. **唯一的当前状态入口**
   - README 指向本审计文档。
   - 所有历史阶段文档增加“历史快照，结论可能落后”的说明，或在文档索引中明确优先级。

2. **真实环境契约验证清单**
   - MaxKB v2.10.4-lts：profile、分页、folder、knowledge、model、OSS、split、batch_create、document status、delete、错误响应。
   - 在线 MinerU：`/file-urls/batch`、预签名 PUT、`extract-results/batch`、`full_zip_url`、状态枚举和限制。
   - 锁定版本内网 MinerU：`/health`、`/tasks`、status/result URL、multipart 参数、ZIP 产物和鉴权。
   - 每项必须记录请求摘要、脱敏响应摘要、验证日期、服务版本和结论；禁止保存真实凭据和业务文件内容。

3. **测试结果摘要**
   - 区分本地单元/模拟契约测试、静态检查、前端构建、交叉编译和真实服务测试。
   - 明确“未连接真实服务”时不得写“已验证可用”。

4. **构建和发布说明**
   - macOS `.app` 和 Windows GUI 产物必须单独记录；Go 交叉编译不等同于 Wails 应用启动成功。
   - 补充应用签名、打包、升级和回滚策略（当前尚未定义）。

### P1：进入发布候选前必须有

1. **配置字段清单**：把数据库字段、后端 DTO、前端控件和默认值做成一张表，特别是 MinerU 高级参数、删除开关、Cron 下次执行时间和配置草稿。
2. **状态机时序图**：批次状态、文件处理阶段、控制请求、SQLite checkpoint 和远端操作之间的关系必须统一说明。
3. **恢复矩阵**：按“开始前、MinerU 任务创建后、下载后、MaxKB 删除后、document ID 保存后、batch_create 后、文档状态轮询中”列出重启、暂停、停止和对账策略。
4. **MaxKB ID 语义说明**：明确 OSS `source_file_id`、MaxKB `document_id`、MinerU `batch_id/task_id` 不可互换。
5. **日志字段白名单**：规定普通日志可以记录哪些 ID、路径和统计信息；禁止记录 Token、请求头、预签名 URL、原始响应和业务内容。
6. **UI 验收记录**：至少 1440×900、1280×800、1024×700 三种尺寸，记录无截断、无重叠、无按钮溢出和状态变化布局稳定性。
7. **迁移运维手册**：包括备份、升级前检查、迁移失败保留数据库、恢复失败处理和旧版本兼容策略。

### P2：长期完善

1. `frontend/README.md` 已改为项目开发者前端说明，移除默认 Vue/Vite 模板措辞。
2. `build/README.md` 已增加本项目的 Wails 构建、产物命名和平台验收说明。
3. 增加文档目录/index，说明设计文档、阶段记录、审计文档和实现文档的权威顺序。
4. 删除或标记前端遗留模板资源和未使用组件，避免文档与资源目录产生误导。
5. 补充许可证、第三方依赖归属、版本策略、发布渠道和隐私声明。

## 五、真实环境契约验证清单

### A. MaxKB v2.10.4-lts

必须使用专用测试工作空间、测试知识库和虚构/短期凭据；不得把完整响应、业务文件内容或凭据提交到仓库。

- [ ] `GET /admin/api/profile`：确认 HTTP 状态、`code=200`、`data.license_is_valid=true`、`data.version` 非空；记录真实版本原文和展示格式化规则。
- [ ] `GET /admin/api/user/profile`：确认 `data.workspace_list[]` 的字段名和权限含义。
- [ ] workspace knowledge folder：确认 folder ID 字段、空目录响应和错误响应；本地代码已支持目录树选择和任务快照保存。
- [ ] knowledge 分页：确认 page 起始值、`total/current/size/data` 字段和 `type=0` 语义。
- [ ] embedding model：确认 `shared_model` 与 `model` 的实际结构、可用状态字段和合并去重规则。
- [ ] OSS：确认 multipart 字段、返回的 source file ID 字段名和临时文件生命周期。
- [ ] smart split：确认响应是数组还是对象、source file ID 字段和错误结构。
- [ ] `batch_create`：确认返回结构、document ID 字段、重复调用是否幂等，以及通过 source file ID 查询文档的可靠性。
- [ ] document list/status：确认状态字段、完整状态枚举、分页和最终成功条件；未知状态不得当作成功。
- [ ] delete：确认 404 的业务语义、超时后的重试和删除完成条件。
- [ ] HTTP 401/403/404/429/5xx：记录脱敏消息和是否可重试。
- [ ] 文档/文件/向量模型限制：确认文件大小单位、文件数、文档数和向量模型限制字段。
- [ ] 知识库与文档链接：确认动态 ID 组成和系统默认浏览器打开行为。
- [ ] legacy `QueryBatchStatus` endpoint：需求未提供该契约，必须实测；在验证完成前不得视为正式接口。

### B. 在线 MinerU

- [ ] `POST /api/v4/file-urls/batch`：确认 `code=0`、`batch_id`、`file_urls` 实际结构和单文件 data ID 语义。
- [ ] 普通文件与 HTML：确认 `model_version=vlm` 与 `MinerU-HTML` 的真实接受范围。
- [ ] 预签名 URL：确认必须使用 PUT、请求头要求、状态码成功范围和 URL 有效期。
- [ ] `GET /api/v4/extract-results/batch/{batchId}`：确认响应层级、状态枚举、错误字段和 `full_zip_url` 位置。
- [ ] 任务重试与恢复：确认原 batch ID 查询、重复 data ID 和重复提交行为。
- [ ] 文件大小/页数限制：确认本地前置判断可验证的字段和服务端错误。
- [ ] ZIP 结果：确认 MaxKB v2.10.4-lts 可直接接收 MinerU 原始 ZIP，并记录智能分段响应结构。

### C. 锁定版本内网 MinerU

参考版本来源和具体 commit/tag 必须在验证记录中写明；当前需求只给出项目源码链接，未提供可复现的锁定 commit。

- [ ] `/health`：确认 HTTP 成功、`status=healthy`、`protocol_version` 以及可选并发字段。
- [ ] `/tasks`：确认 HTTP 202、multipart 字段、默认参数、文件名返回和 `queued_ahead`。
- [ ] status URL：确认 `pending`、`processing`、`completed` 及失败状态的真实枚举和错误消息。
- [ ] result URL：确认 ZIP 下载状态、URL 生命周期和失败后的重试行为。
- [ ] `backend` 以 `-http-client` 结尾时 `server_url` 的实际要求。
- [ ] 原生服务和企业网关 Bearer Token 的鉴权差异。
- [ ] 并发创建限制、轮询建议间隔和服务端处理窗口。
- [ ] ZIP 下载地址、文件名、内容类型和结果归档结构。

## 五点五、流式处理审计结论

本轮补齐了大文件主链路中的全量读取问题：

- `SnapshotService` 继续以流式复制并计算 MD5。
- MinerU 提交优先使用持久化快照路径，Adapter 在请求内打开文件并通过 multipart/PUT 流式发送。
- MaxKB OSS 与智能分段使用 `io.Pipe` + `multipart.Writer`，不先把文件拼成 `bytes.Buffer`。
- MinerU 结果优先通过 `DownloadResultTo`/`DownloadResultToAt` 流式写入私有临时 ZIP；客户端不解压，随后从文件流直接送入 MaxKB。
- legacy `FileContent`、`DownloadResult() []byte` 仍为兼容 API；生产 Adapter 优先使用流式下载，MaxKB `batch_create` 的段落 JSON 仍可能按文本大小占用内存。

因此，当前结论是“主要文件传输链路已流式化，尚未达到所有文本处理环节零内存复制”，而不是笼统宣称全链路完全流式。

## 六、已执行测试与结果

### 已执行并通过

```text
GOCACHE=/tmp/maxkb-go-cache go vet ./...
GOCACHE=/tmp/maxkb-go-cache go test ./... -count=1
GOCACHE=/tmp/maxkb-go-cache go test -race ./... -count=1
GOCACHE=/tmp/maxkb-go-cache go test ./internal/adapter -race -count=1
GOCACHE=/tmp/maxkb-go-cache go vet ./...
frontend: npm run build
GOOS=windows GOARCH=amd64 GOCACHE=/tmp/maxkb-go-cache go build ./...
GOCACHE=/tmp/maxkb-go-cache go build -v .
```

历史上曾尝试过不经过 Wails packaging 的手工生产链接命令：

```text
GOCACHE=/tmp/maxkb-go-cache go build -buildvcs=false -tags desktop,wv2runtime.download,production -ldflags "-w -s" -o /tmp/maxkb-local-file-sync-wails
```

该历史命令曾执行但未通过；本轮 Wails packaging 已通过。当前结果：

- `go vet ./...`：通过。
- `go test ./... -count=1`：通过；包含 credential、db、file、logger、repository、service 测试包，以及 MaxKB/MinerU Adapter 模拟协议测试；新增同步任务 MaxKB 快照、目录绑定和路径规范化仓储测试。
- `go test -race ./... -count=1`：通过。
- `go test ./internal/adapter -race -count=1`：通过。
- `npm run build`：通过 `vue-tsc --noEmit` 和 Vite production build；存在约 500 KB 以上 chunk 的性能警告，但非失败。
- Windows Go 交叉编译：通过；仅代表 Go 包可编译。
- Go 本地构建：通过。
- Wails 生产编译和 packaging：本轮已通过 `wails build -clean -platform darwin/arm64`，并生成 macOS `.app`；本机没有 Windows 主机和 `makensis`，Windows 安装包仍待目标环境构建。

### 未执行、未完成或当前环境无法证明的测试

- 未连接 MaxKB v2.10.4-lts 实例，未完成真实接口端到端验证。
- 未连接在线 MinerU 服务，未完成真实上传、轮询和下载验证。
- 未部署或连接项目锁定版本的内网 MinerU，且需求没有给出可复现 commit/tag。
- 未在 Windows 主机启动 Wails GUI、验证 WebView2、目录选择和 Credential Manager。
- 已在当前 macOS 环境完成 Wails `.app` 构建，但未在 Finder 中执行安装后首次启动验收。
- 未完成 Keychain/Credential Manager 的实机读写验收。
- 未完成 1440×900、1280×800、1024×700 的截图级视觉回归。
- 未完成大规模目录、200 MB/200 页边界、权限受限目录和异常断电测试。

## 七、文档优先级与维护规则

1. **当前实现和风险**：以本文件和 `README.md` 为准。
2. **设计基线**：以 `DESIGN_V2.md` 和后续已明确的实现决策为准；如与代码冲突，必须新增决策记录，不要静默修改历史文档。
3. **历史阶段记录**：`PHASE*.md`、`IMPLEMENTATION_ROADMAP.md`、`GAP_ANALYSIS.md` 保留其历史价值，但标题或开头应注明快照日期和“可能落后于当前实现”。
4. **真实服务契约**：任何未经实际响应、指定源码或契约测试支持的字段都必须标记“待验证”，禁止使用“应该可用”。
5. **敏感信息**：文档、测试输出、截图和提交记录不得包含真实 API Key、Token、Cookie、用户资料或业务文件内容。

## 八、本次文档变更

- `README.md`：从 Wails 默认模板改为项目真实说明。
- `FINAL_AUDIT.md`：新增本需求覆盖审计、文档缺口、测试摘要、文档优先级和真实环境契约验证清单。

本次同时更新了 Adapter、迁移、扫描预览和前端类型/页面，详见上方变更清单。

## 九、本轮 MaxKB 异步结果处理补充

### 已实现

- 新增 `internal/service/maxkb_reconciler.go`：应用启动立即对账，之后默认每 2 秒执行一次。
- 仅处理 `MAXKB_SPLITTING`、`MAXKB_CREATING`、`MAXKB_PROCESSING` 阶段的 `RECONCILE_REQUIRED` 文件。
- 仅按本地持久化的 `document_id` 或唯一 `source_file_id` 匹配远端文档，禁止按文件名认领。
- 匹配成功且 Adapter 判定远端状态成功时，调用可靠性仓储的 `REMOTE_SUCCEEDED` 事务，自动更新本地映射和最终状态。
- 远端未找到、状态处理中、状态未知、状态失败、身份冲突或多条 source ID 匹配时不盲目重传，保留人工处理。
- MaxKB 文档列表兼容顶层和 `meta.source_file_id`，顶层字段优先。
- 前端将 `RECONCILE_REQUIRED` 计数视为后台工作，继续轮询队列与任务列表，避免后端异步完成后页面停留在旧状态。
- MaxKB Adapter 已根据 v2.10.4-lts 锁定源码验证 `2/n` 聚合状态规则；例如 `nnnn` 映射为成功，其他未验证状态保持未知。

### 本轮新增测试

- `internal/service/maxkb_reconciler_test.go`：
  - document ID 优先匹配；
  - source_file_id 唯一匹配与多匹配冲突；
  - document ID/source_file_id 冲突；
  - `nnnn` 成功状态触发自动对账；
  - 未找到、处理中、未知和失败状态不自动确认；
  - 无安全身份、错误阶段和依赖缺失不发起远端请求；
  - 并发 `RunNow` 不重入。
- `internal/adapter/maxkb_test.go`：覆盖 `meta.source_file_id` 和已验证聚合状态映射。

### 本轮真实环境验证边界

当前环境未连接用户的 MaxKB 实例，未执行真实端到端请求。因此本轮结论是：本地 Adapter 契约测试、服务层测试和前端构建覆盖了实现逻辑；不能据此声称真实 MaxKB 已验证。仍需在 MaxKB v2.10.4-lts 测试实例核对：

- 文档列表分页的实际字段和完整性；
- `status` 聚合值在智能分段、向量化和失败阶段的真实变化；
- `nnnn`、`2nnn`、`2n2n` 等组合在目标部署中的最终语义；
- `source_file_id` 是否始终位于 `meta.source_file_id`，以及文档 ID与 source ID 的生命周期；
- `batch_create` 返回文档记录后，文档列表可见性的延迟和权限过滤行为。

## 十、本轮验证记录

本轮已执行并通过：

- `gofmt`：通过；
- `go test ./... -count=1`：通过，包含新增异步对账服务测试；
- `go test -race ./...`：通过；
- `go vet ./...`：通过；
- `go build ./...`：通过；
- `cd frontend && npm run build`：通过 `vue-tsc --noEmit` 和 Vite 构建（仅有 chunk 体积提示）。

以上均为本地模拟/静态/构建验证，不等同于真实 MaxKB 服务端到端验证。


## 十一、本轮 MinerU 产物配置调整（2026-08-28）

### 已实现

- MinerU 系统设置不再提供“保存启用状态”或“保存策略”按钮；启用开关单独控制服务是否启用，服务关闭时其余 MinerU 配置只读禁用，页面不收起。
- 服务连接与产物保存/清理合并为 MinerU Tab 底部唯一的“保存配置”操作，服务连接区仅保留“测试连接”。MinerU 开启时，产物保存目录在前端和后端均强制必填。
- MinerU 结果固定按原始 ZIP 保存，不解压；ZIP 直接进入 MaxKB 的上传/智能分段流程。
- 清理策略包括：立即清理、按批次保留最近 N 个批次、按时间保留最近 N 小时/天、不自动清理。默认策略为“不自动清理”，即默认保存 ZIP，同时支持手动清理。
- 清理服务按任务隔离批次保留额度，只清理能通过本地 `sync_runs.id` 关联的批次目录，保护运行中、排队、暂停和异常恢复批次。
- 新增 `mineru_cleanup_after_value` 与 `mineru_cleanup_after_unit` 迁移字段，并保留旧字段用于数据库升级兼容。

### 本轮验证

- `go test ./...`：通过。
- `cd frontend && npm run build`：通过 `vue-tsc --noEmit` 和 Vite 构建。
- 清理策略测试覆盖任务隔离、按小时清理、活动批次保护、手动清理和暂停批次保护。

### 仍需真实环境验证

- 需要在真实 MaxKB v2.10.4-lts 环境确认 ZIP 文件经过 `/document/split` 后的文档状态、批次可见性和异步处理延迟。
- 需要在目标在线 MinerU 和锁定版本内网 MinerU 环境确认结果 ZIP 下载内容、文件名、生命周期及失败状态。
- 需要在 macOS Keychain、Windows Credential Manager 和 Windows Wails GUI 上进行实机验收。

## 本轮安装包交付记录（2026-08-31）

本轮已实施并验证：

- `wails.json` 增加统一产品版本 `1.0.0`、厂商、版权和说明元数据。
- 增加 `scripts/build-windows.ps1`：Windows x64 单一 NSIS 安装包构建入口，支持安装向导内选择当前用户/所有用户范围、自定义目录、可选代码签名和 SHA-256。
- 完善 `build/windows/installer/project.nsi`：中文安装界面、运行时安装范围选择、安装目录选择、按范围写入卸载注册表项、快捷方式和卸载入口。
- 增加 `scripts/build-macos-dmg.sh`：Apple Silicon arm64 Wails `.app` 构建、标准 Finder 拖拽式 DMG、中文安装说明、可选签名/公证和 SHA-256。
- 增加 `scripts/verify-release.sh`：macOS 应用元数据校验入口。
- 应用数据目录从旧的 `~/.maxkb-sync` 迁移为平台标准用户目录；首次启动仅在新目录不存在时迁移旧目录，避免覆盖新数据。
- macOS entitlements 收敛为用户选择文件读写和网络客户端权限，不申请网络服务端或全盘文件权限。

本机已执行并通过：

```text
GOCACHE=/tmp/maxkb-go-cache go vet ./...
GOCACHE=/tmp/maxkb-go-cache go test ./... -count=1
frontend: npm run build
wails build -clean -platform darwin/arm64 -o "MaxKB 本地文件同步工具"
./scripts/build-macos-dmg.sh
./scripts/verify-release.sh
```

已生成产物：

```text
dist/macos/MaxKB-Local-File-Sync-v1.0.0-macos-arm64.dmg
dist/checksums/MaxKB-Local-File-Sync-v1.0.0-macos-arm64.dmg.sha256
```

未在本机执行：

- Windows NSIS `.exe` 实际构建：当前 macOS 没有 `makensis`，且没有 Windows 构建主机。
- Windows 安装、UAC、WebView2、Credential Manager、升级和卸载实机验收。
- macOS Apple Developer ID 签名、公证和 Gatekeeper 验收：当前环境未配置签名身份。
- macOS DMG 在 Finder 中的拖拽安装和首次启动实机验收；已完成 DMG 镜像生成、应用元数据和 SHA-256 校验。

安装包发布前仍必须在目标 Windows 主机和目标 Mac 上完成上述实机验收，不得以交叉编译或本机无签名 DMG 代替平台验收。
