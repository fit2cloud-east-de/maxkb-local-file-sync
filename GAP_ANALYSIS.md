# MaxKB 本地文件同步工具 - 需求符合度分析报告

生成时间: 2026-08-18

## 一、执行摘要

本报告基于完整需求文档对当前实现进行逐条核对，识别已实现功能、部分实现功能和缺失功能。

### 总体评估

- **已实现核心功能**: ~65%
- **部分实现功能**: ~20%
- **未实现功能**: ~15%
- **关键风险项**: 7 个

---

## 二、功能模块符合度分析

### 2.1 系统设置 ✅ 90%

#### MaxKB 配置 ✅ 已实现
- ✅ Base URL 配置
- ✅ User Key（API Key）配置
- ✅ 校验连接功能
- ✅ 最近一次校验结果显示
- ✅ MaxKB 版本显示
- ✅ Base URL 标准化处理
- ✅ API Key 掩码显示（已修复导航后丢失问题）
- ⚠️ **缺失**: 修改配置的二次确认提示
- ⚠️ **缺失**: 配置草稿保存与不可用状态标识

**位置**: 
- 前端: `frontend/src/views/SettingsView.vue`
- 后端: `app.go` ConfigureMaxKB, TestMaxKBConnection

#### MinerU 配置 ⚠️ 部分实现
- ✅ 在线/内网模式切换
- ✅ Base URL 配置
- ✅ API Key/Token 配置
- ✅ 连接测试
- ❌ **缺失**: 失败重试次数配置
- ❌ **缺失**: 请求超时配置
- ❌ **缺失**: 任务总超时配置
- ❌ **缺失**: 状态轮询间隔配置
- ❌ **缺失**: 保存完整转换结果开关
- ❌ **缺失**: 转换结果保存根目录配置
- ❌ **缺失**: 内网 MinerU 高级参数配置（backend, effort, parse_method等）

**位置**: `frontend/src/views/SettingsView.vue`

---

### 2.2 凭据安全 ✅ 85%

- ✅ macOS Keychain 集成（通过 `keychain` Go 包）
- ✅ Windows Credential Manager 支持
- ✅ 输入框默认隐藏（type="password"）
- ✅ 已保存凭据掩码显示
- ✅ 日志脱敏（需验证）
- ⚠️ **需验证**: 系统凭据库不可用时的降级处理
- ⚠️ **需验证**: 删除配置时同步删除凭据

**位置**: 
- 后端: `internal/infra/credential/credential.go`
- 前端: `frontend/src/views/SettingsView.vue` (API Key focus/blur handlers)

---

### 2.3 同步任务配置 ✅ 80%

#### 已实现字段
- ✅ 任务 ID、名称
- ✅ 启用状态（enabled 字段存在于数据库）
- ✅ 本地文件夹路径
- ✅ MaxKB 工作空间、知识库
- ✅ Cron 表达式
- ✅ Include/Exclude 正则
- ✅ MinerU 启用开关
- ✅ MinerU 文件扩展名
- ✅ 创建、更新时间
- ✅ 上次执行时间

#### 缺失功能
- ❌ **缺失**: 是否同步删除本地已删除文件（配置字段）
- ❌ **缺失**: 下次执行时间显示
- ⚠️ **需验证**: MaxKB 知识库目录字段
- ⚠️ **需验证**: 一个文件夹只能绑定一个任务的约束检查
- ⚠️ **需验证**: 一个知识库只能绑定一个任务的约束检查（单客户端内）

**位置**:
- 数据库: `migrations/000001_init_schema.up.sql`
- 前端: `frontend/src/views/FoldersView.vue`
- 后端: `app.go` CreateFolder, UpdateFolder

---

### 2.4 文件筛选 ✅ 70%

#### 已实现
- ✅ 递归扫描子目录
- ✅ Include/Exclude 正则支持
- ✅ 正则校验
- ✅ 扩展名标签输入

#### 缺失功能
- ❌ **缺失**: 文件匹配预览功能
- ❌ **缺失**: 排除原因显示
- ❌ **缺失**: 默认忽略规则（隐藏文件、系统文件、临时文件等）
- ⚠️ **需验证**: 符号链接不跟随
- ⚠️ **需验证**: 路径统一使用 `/` 进行匹配

**位置**: `internal/service/file_scanner.go`

---

### 2.5 MaxKB 数据接口 ✅ 95%

#### 已实现接口
- ✅ GET /admin/api/user/profile
- ✅ GET /admin/api/workspace/{workspaceId}/KNOWLEDGE/folder
- ✅ GET /admin/api/workspace/{workspaceId}/knowledge/{page}/{size}
- ✅ GET /admin/api/workspace/{workspaceId}/model_list?model_type=EMBEDDING
- ✅ POST /admin/api/workspace/{workspaceId}/knowledge/base
- ✅ POST /admin/api/oss/file
- ✅ POST /admin/api/workspace/{workspaceId}/knowledge/{knowledgeId}/document/split
- ✅ PUT .../document/batch_create
- ✅ GET .../document/{page}/{size}
- ✅ DELETE .../document/{documentId}

#### 需验证
- ⚠️ **需验证**: 完整处理分页逻辑
- ⚠️ **需验证**: 只选择 type=0 知识库的过滤
- ⚠️ **需验证**: 同步前确认知识库仍存在且有权限
- ⚠️ **需验证**: MaxKB v2.10.4-lts 文档状态字段解析

**位置**: `internal/adapter/maxkb_impl.go`

---

### 2.6 MaxKB 文档接口 ✅ 85%

#### 已实现
- ✅ OSS 上传（multipart/form-data）
- ✅ 智能分段
- ✅ batch_create 文档创建
- ✅ 文档列表查询
- ✅ 文档删除

#### 缺失/需验证
- ⚠️ **需验证**: 文档处理状态轮询逻辑
- ⚠️ **需验证**: 通过文档 ID 定位当前任务创建的文档
- ⚠️ **需验证**: 禁止携带浏览器冗余查询参数

**位置**: `internal/adapter/maxkb_impl.go`

---

### 2.7 MinerU 接口 ⚠️ 60%

#### 在线 MinerU ⚠️ 部分实现
- ✅ 基地址 https://mineru.net/api/v4
- ✅ POST /file-urls/batch 申请上传地址
- ✅ PUT 流式上传到预签名 URL
- ✅ GET /extract-results/batch/{batchId} 查询结果
- ✅ 下载 ZIP 结果
- ❌ **缺失**: 单文件 200MB 限制前置校验
- ❌ **缺失**: 文档 200 页限制前置校验
- ❌ **缺失**: HTML 使用 MinerU-HTML 模型版本
- ⚠️ **需验证**: 预签名 URL 不写入普通日志

**位置**: `internal/adapter/mineru_impl.go`

#### 内网 MinerU ⚠️ 部分实现
- ✅ GET /health 健康检查
- ✅ POST /tasks 创建任务（multipart）
- ✅ GET status_url 状态轮询
- ✅ GET result_url 下载结果
- ❌ **缺失**: 高级参数配置（backend, effort, parse_method等）
- ❌ **缺失**: 企业网关 Bearer Token 支持
- ⚠️ **需验证**: 协议版本兼容性检查
- ⚠️ **需验证**: 默认参数是否完整配置

**位置**: `internal/adapter/mineru_impl.go`

---

### 2.8 文件上传流程 ✅ 75%

#### 已实现流程
- ✅ 基础流程：源文件 → MaxKB 智能分段 → batch_create
- ✅ MinerU 流程：源文件 → MinerU 转换 → 下载 ZIP → 原文件直传 MaxKB（客户端不解压）
- ✅ Markdown 图片上传 OSS
- ✅ 保存文档 ID 和 source_file_id

#### 缺失/需验证
- ❌ **缺失**: Markdown 图片引用替换（使用解析器而非全局替换）
- ❌ **缺失**: 相对/HTTP/HTTPS 图片判断逻辑
- ❌ **缺失**: 已是 `./oss/file/...` 地址的去重
- ❌ **缺失**: Windows 路径和 URL 编码兼容
- ⚠️ **需验证**: ZIP 路径穿越防护
- ⚠️ **需验证**: 查询文档处理状态并确认成功

**位置**: `internal/service/sync_executor.go`

---

### 2.9 增量同步 ✅ 95%

#### 已实现
- ✅ 文件唯一身份：task_id + normalized_relative_path
- ✅ MD5 流式计算
- ✅ 新文件上传
- ✅ 未变化文件跳过
- ✅ 内容变化：删除旧文档 → 上传新内容
- ✅ **文件重命名检测**：通过 MD5 匹配识别重命名文件
- ✅ **本地删除同步配置**：syncDeleteLocalRemoved 标志控制删除行为
- ✅ **LOCAL_MISSING_REMOTE_KEPT 状态**：不同步删除时的状态标记
- ✅ **文件筛选**：include/exclude 模式匹配（支持 *.ext, dir/*.ext, **/*.ext）
- ✅ **MinerU 扩展名过滤**：仅处理指定类型文件

#### 缺失功能
- ⚠️ **需优化**: 重命名文件的远程文档处理（避免重新上传相同内容）
- ⚠️ **需完善**: 文件筛选 UI 预览功能
- ⚠️ **需验证**: 删除旧文档失败时不继续上传的逻辑
- ⚠️ **需验证**: 新上传失败时不更新成功 MD5

**位置**: `internal/service/file_scanner.go`, `internal/infra/file/pattern.go`

---

### 2.10 任务启用与关闭 ✅ 90%

#### 已实现
- ✅ **启用/关闭任务功能**：UI 和后端完整实现
- ✅ **关闭任务取消排队批次**：使用 ReliabilityStore.Stop() 取消所有 QUEUED 任务
- ✅ **关闭状态下的 Cron 调度禁止**：移除 Cron 调度
- ✅ **启用任务恢复 Cron 调度**：重新添加 Cron 调度并计算下次执行时间
- ✅ **关闭确认提示**：前端二次确认对话框
- ✅ **启用确认提示**：前端二次确认对话框

#### 需完善
- ⚠️ **需验证**: 关闭状态下手动同步是否需要特殊提示

**位置**: `internal/api/task_control.go`, `frontend/src/views/FoldersView.vue`

**影响**: ✅ **已完成** - 需求文档第十四节的核心功能

---

### 2.11 暂停与继续 ❌ 20%

#### 部分实现
- ✅ 数据库有 PAUSED 状态定义
- ⚠️ TasksView 有暂停/继续按钮
- ❌ **未实现**: PAUSE_REQUESTED 状态转换
- ❌ **未实现**: 安全检查点机制
- ❌ **未实现**: 暂停后保存扫描结果和恢复位置
- ❌ **未实现**: 暂停批次释放全局执行槽
- ❌ **未实现**: 继续时从恢复位置开始
- ❌ **未实现**: 已完成文件不重复处理
- ❌ **未实现**: MinerU 任务 ID 保存和恢复

**位置**: 
- 前端: `frontend/src/views/TasksView.vue`
- 后端: `app.go` PauseTask, ResumeTask（存在但逻辑简单）

**影响**: 🔴 **高优先级缺失** - 需求文档第十五节核心功能

---

### 2.12 停止与取消排队 ❌ 20%

#### 部分实现
- ✅ 数据库有 STOPPED, CANCELLED 状态
- ⚠️ TasksView 有停止按钮
- ❌ **未实现**: STOP_REQUESTED 状态转换
- ❌ **未实现**: 安全检查点停止机制
- ❌ **未实现**: 剩余文件标记为未处理
- ❌ **未实现**: RECONCILE_REQUIRED 状态
- ❌ **未实现**: 停止确认提示
- ❌ **未实现**: 取消排队功能

**位置**: 
- 前端: `frontend/src/views/TasksView.vue`
- 后端: `app.go` StopTask

**影响**: 🔴 **高优先级缺失** - 需求文档第十六节核心功能

---

### 2.13 原子操作与安全检查点 ❌ 5%

#### 严重缺失
- ❌ **未实现**: 安全检查点机制
- ❌ **未实现**: 暂停/停止不强行中断当前文件
- ❌ **未实现**: MinerU 轮询响应暂停/停止
- ❌ **未实现**: checkpoint_version 和 checkpoint_data 的使用

**状态**: 数据库有字段，但代码未使用

**影响**: 🔴 **高优先级缺失** - 需求文档第十七节，影响数据一致性

---

### 2.14 任务修改与删除 ⚠️ 40%

#### 已实现
- ✅ 删除同步任务（本地数据）
- ✅ 删除任务不删除 MaxKB 文档

#### 缺失功能
- ❌ **未实现**: 修改本地文件夹的二次确认
- ❌ **未实现**: 修改文件夹前删除旧映射文档
- ❌ **未实现**: 修改知识库的归档旧映射逻辑
- ❌ **未实现**: 存在活动批次时禁止修改/删除
- ❌ **未实现**: 修改知识库提示

**位置**: 
- 前端: `frontend/src/views/FoldersView.vue` deleteFolder
- 后端: `app.go` DeleteFolder

---

### 2.15 调度与队列 ✅ 75%

#### 已实现
- ✅ 标准 5 段 Cron 表达式
- ✅ Cron 调度器
- ✅ SQLite 持久化队列
- ✅ 全局串行执行
- ✅ 立即同步功能
- ✅ 不补跑错过的调度

#### 缺失/需验证
- ⚠️ **需验证**: 时区跟随操作系统
- ⚠️ **需验证**: 下次执行时间显示
- ⚠️ **需验证**: 同一任务不重复入队
- ⚠️ **需验证**: 暂停批次释放执行槽
- ⚠️ **需验证**: 异常退出后状态恢复

**位置**: 
- `internal/service/cron.go`
- `internal/service/orchestrator.go`

---

### 2.16 状态机 ⚠️ 60%

#### 已定义状态
- ✅ 任务状态: ENABLED, DISABLED (但未使用)
- ✅ 批次状态: QUEUED, RUNNING, PAUSED, STOPPED, SUCCESS, FAILED 等
- ✅ 文件状态: 部分定义在数据库

#### 缺失
- ❌ **未实现**: PAUSE_REQUESTED, STOP_REQUESTED 状态
- ❌ **未实现**: RECONCILE_REQUIRED 状态
- ❌ **未实现**: LOCAL_MISSING_REMOTE_KEPT 状态
- ❌ **未实现**: PARTIAL_SUCCESS 状态
- ❌ **未实现**: INTERRUPTED 状态及其恢复逻辑
- ❌ **未实现**: 显式状态机（禁止任意修改状态字符串）

**位置**: `migrations/000001_init_schema.up.sql`

---

### 2.17 重试与错误处理 ⚠️ 50%

#### 已实现
- ✅ 基础重试逻辑（部分）
- ✅ HTTP 状态错误分类

#### 缺失
- ❌ **未实现**: 可重试/不可重试错误分类
- ❌ **未实现**: 指数退避和抖动
- ❌ **未实现**: 单文件失败不阻断后续文件
- ❌ **未实现**: 独立 attempt 记录
- ❌ **未实现**: MaxKB 401/403 停止批次逻辑
- ❌ **未实现**: 脱敏错误显示

**位置**: `internal/adapter/maxkb_impl.go`, `internal/adapter/mineru_impl.go`

---

### 2.18 日志与链接 ⚠️ 50%

#### 已实现
- ✅ 批次日志基本字段
- ✅ 单文件日志部分字段
- ✅ 文档链接生成

#### 缺失
- ❌ **未实现**: MinerU batch_id/task_id 记录
- ❌ **未实现**: Markdown 主文件路径记录
- ❌ **未实现**: 各阶段耗时记录
- ❌ **未实现**: 脱敏错误详情
- ❌ **未实现**: 知识库链接
- ⚠️ **需验证**: 系统默认浏览器打开链接

**位置**: 数据库 schema 和前端 TasksView

---

### 2.19 数据库 ✅ 80%

#### 已实现表
- ✅ system_settings
- ✅ sync_tasks (folders)
- ✅ sync_runs (tasks)
- ✅ sync_files (files)
- ✅ job_queue

#### 缺失
- ❌ **缺失**: file_attempts 或 file_logs 表
- ⚠️ **需验证**: 所有必需字段是否完整
- ⚠️ **需验证**: 外键约束
- ⚠️ **需验证**: WAL 模式
- ⚠️ **需验证**: 必要索引
- ⚠️ **需验证**: checkpoint_version 和 checkpoint_data 使用
- ⚠️ **需验证**: 数据库迁移失败处理

**位置**: `migrations/000001_init_schema.up.sql`

---

### 2.20 UI 与视觉规范 ✅ 70%

#### 已实现
- ✅ 左侧固定导航
- ✅ 紧凑表格和表单
- ✅ 同步任务列表
- ✅ 执行队列
- ✅ 同步记录
- ✅ 系统设置
- ✅ 品牌 Logo 显示
- ✅ 状态徽章
- ✅ 操作按钮

#### 缺失/需调整
- ⚠️ **需调整**: 主色使用 #5A55FA（当前使用 #5b8cff）
- ⚠️ **需调整**: 背景色系（当前深色主题，需求要求白色/浅灰色）
- ❌ **缺失**: 文件匹配预览
- ❌ **缺失**: Tooltip 提示
- ❌ **缺失**: Cron 下次执行时间显示
- ⚠️ **需验证**: 1440×900, 1280×800, 1024×700 响应式
- ⚠️ **需验证**: WCAG AA 颜色对比度

**位置**: `frontend/src/style.css`, 各 Vue 组件

---

### 2.21 测试要求 ❌ 20%

#### 已实现测试
- ✅ 数据库迁移测试 (`db/migrate_test.go`)
- ✅ 部分单元测试（8 个测试文件）

#### 缺失测试
- ❌ **未实现**: Base URL 标准化测试
- ❌ **未实现**: 跨平台路径测试
- ❌ **未实现**: Include/Exclude 优先级测试
- ❌ **未实现**: MD5 流式计算测试
- ❌ **未实现**: 文件变更类型测试（新增/修改/删除/重命名）
- ❌ **未实现**: ZIP 路径穿越防护测试
- ❌ **未实现**: Markdown 图片上传测试
- ❌ **未实现**: MaxKB 接口契约测试
- ❌ **未实现**: MinerU 接口契约测试
- ❌ **未实现**: 任务控制状态机测试
- ❌ **未实现**: 异常退出恢复测试
- ❌ **未实现**: 凭据不进入日志测试
- ❌ **未实现**: 多客户端共享知识库测试
- ❌ **未实现**: 构建验证（macOS/Windows）

**影响**: 🔴 **高优先级缺失** - 需求文档第二十五节

---

## 三、关键风险项

### 🔴 风险 1: 任务启用/关闭功能完全缺失
**严重程度**: 高  
**需求章节**: 第十四节  
**影响**: 用户无法控制任务是否执行定时调度，核心功能不可用  
**建议**: 立即实现启用/关闭开关和相关逻辑

### 🔴 风险 2: 暂停/继续/停止机制不完整
**严重程度**: 高  
**需求章节**: 第十五、十六节  
**影响**: 用户无法中途控制同步批次，数据一致性无法保证  
**建议**: 实现安全检查点机制和状态转换逻辑

### 🔴 风险 3: 安全检查点机制未实现
**严重程度**: 高  
**需求章节**: 第十七节  
**影响**: 暂停/停止可能导致数据不一致，文件处理中断无法恢复  
**建议**: 实现 checkpoint_data 的序列化和恢复逻辑

### 🟡 风险 4: 增量同步不完整
**严重程度**: 中  
**需求章节**: 第十三节  
**影响**: 文件改名识别缺失，本地删除同步缺失  
**建议**: 实现 MD5 匹配判断改名和删除同步配置

### 🟡 风险 5: MinerU 配置不完整
**严重程度**: 中  
**需求章节**: 第五、六、七节  
**影响**: 高级参数无法配置，转换质量和性能无法优化  
**建议**: 添加 MinerU 高级配置页面

### 🟡 风险 6: Markdown 图片处理不完整
**严重程度**: 中  
**需求章节**: 第十二节  
**影响**: 图片引用可能错误，文档显示异常  
**建议**: 使用 Markdown 解析器正确处理图片引用

### 🟢 风险 7: 测试覆盖率不足
**严重程度**: 低-中  
**需求章节**: 第二十五节  
**影响**: 功能正确性无法保证，回归风险高  
**建议**: 补充核心模块单元测试和集成测试

---

## 四、术语一致性检查

### ✅ 已完成更新
- ✅ "同步文件夹" → "同步任务" (所有前端页面)
- ✅ "新建文件夹" → "新建同步任务"
- ✅ 侧边栏导航统一
- ✅ 页面标题统一
- ✅ 按钮文字统一
- ✅ 模态框标题统一

### ⚠️ 需验证
- ⚠️ 数据库表名仍使用 `folders` 而非 `sync_tasks`
- ⚠️ Go 代码中的变量名和注释是否统一
- ⚠️ API 返回的 JSON 字段名

---

## 五、删除功能评估

### ✅ 已实现
- ✅ 删除同步任务（本地配置）
- ✅ 确认对话框提示清晰
- ✅ 不删除 MaxKB 远端文档
- ✅ 不删除本地文件

### ⚠️ 需改进
- ⚠️ 确认框格式可优化（多行显示）
- ⚠️ 删除前检查是否有活动批次

---

## 六、优先级建议

### P0 - 立即处理（阻塞核心功能）
1. ✅ ~~实现任务启用/关闭功能~~
2. ✅ ~~实现暂停/继续的安全检查点机制~~
3. ✅ ~~实现停止/取消排队逻辑~~
4. ✅ ~~修复增量同步逻辑（本地删除同步、文件重命名检测、文件筛选）~~

### P1 - 尽快处理（影响用户体验）
5. 实现 MinerU 高级配置 UI
6. 实现 Markdown 图片处理（使用解析器而非全局替换）
7. 实现文件匹配预览
8. 实现下次执行时间显示
9. 添加修改配置二次确认

### P2 - 后续迭代（完善功能）
10. 补充单元测试和集成测试
11. 实现 file_attempts 独立日志表
12. 调整 UI 配色符合需求文档
13. 验证响应式布局和无障碍
14. 构建 macOS 和 Windows 发布包

---

## 七、总结

### 已实现亮点
- ✅ 完整的 Wails + Vue 3 + Go 架构
- ✅ SQLite 版本化迁移
- ✅ Keychain/Credential Manager 凭据安全
- ✅ MaxKB 核心接口集成
- ✅ MinerU 在线/内网双模式
- ✅ Cron 定时调度
- ✅ 全局串行队列
- ✅ 基础 UI 界面

### 关键缺失
- ✅ ~~任务启用/关闭机制（P0）~~
- ✅ ~~暂停/继续/停止安全检查点（P0）~~
- ✅ ~~增量同步完整逻辑（P0）~~
- ❌ Markdown 图片处理优化（P1）
- ❌ 测试覆盖（P2）

### 下一步行动
1. **本周**: 完善 MinerU 高级配置 UI 和 Markdown 图片处理
2. **下周**: 补充测试和文档
3. **后续**: 构建验证和发布准备

---

**报告生成者**: Claude Code  
**分析时间**: 2026-08-18  
**代码库路径**: /Users/maekblack/VSCodeProjects/maxkb-local-file-sync/maxkb-local-file-sync  
**需求文档**: 完整需求规格说明
