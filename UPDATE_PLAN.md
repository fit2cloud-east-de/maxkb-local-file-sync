# 在线更新升级方案

## 目标

应用代码和正式安装包由 GitHub 仓库托管。用户在应用内点击“检查更新”后，客户端能够发现新版本、展示更新说明、下载对应平台安装包，并在用户确认后完成升级，不要求用户手动重新寻找和下载安装文件。

这里的“无需重新下载安装”是指：用户无需手工重新下载和安装；客户端仍然需要在后台下载新版本文件，然后自动启动安装器或替换应用。跨平台桌面应用不应在运行中的主进程内直接覆盖自身。

## 发布源

正式版本使用 GitHub Releases，不使用 GitHub Actions 的临时 Artifacts 作为生产下载源。每个版本发布：

```text
v1.0.1/
├── MaxKB-Local-File-Sync-v1.0.1-windows-x64-setup.exe
├── MaxKB-Local-File-Sync-v1.0.1-windows-arm64-setup.exe
├── MaxKB-Local-File-Sync-v1.0.1-macos-x64.dmg
├── MaxKB-Local-File-Sync-v1.0.1-macos-arm64.dmg
├── SHA256SUMS.txt
└── release-notes.md
```

GitHub Release 的 `tag_name`、`assets` 和 `digest` 可用于发现版本和校验资产；正式实现仍要校验本地平台、架构、版本和签名。仓库 owner/name 需要在发布前填入应用发布配置，不能在代码中猜测。

## 客户端流程

```text
启动后延迟检查（不阻塞首页）
        ↓
获取当前版本
        ↓
读取 GitHub Releases 最新正式版本
        ↓
比较 SemVer
        ↓
没有更新：显示“已是最新版本”
有更新：显示版本、发布日期、更新说明和文件大小
        ↓
用户确认
        ↓
下载到应用私有临时目录
        ↓
流式计算 SHA-256
        ↓
校验 Release asset digest / SHA256SUMS
        ↓
校验平台和架构
        ↓
校验代码签名
        ↓
启动外部升级器或安装器
        ↓
退出当前应用
        ↓
完成升级后重新启动
```

检查更新、下载和升级都不能阻塞同步队列；同步批次正在执行时，默认只允许下载，不立即退出应用。用户确认后，如果存在运行中的批次，应提示等待批次结束，或者由用户明确选择“退出并稍后恢复”。

## Windows 升级

Windows 使用同一个 NSIS 安装包覆盖升级：

- 安装器识别已有安装目录；
- 关闭当前应用后替换程序文件；
- 不删除 `%LOCALAPPDATA%\\MaxKB\\MaxKB 本地文件同步工具`；
- 不删除 SQLite、任务、队列、日志和凭据引用；
- 升级前备份 SQLite；
- 启动时执行数据库迁移；
- 迁移失败时阻止启动并保留旧数据库备份。

当前用户安装和所有用户安装的升级范围必须与原安装范围一致。安装器不应在升级时悄悄把用户安装改成机器安装。

## macOS 升级

macOS 使用新版本 DMG 的自动打开方式：

- 下载新的 arm64 DMG 到临时目录；
- 校验 DMG；
- 打开 Finder DMG；
- 关闭当前应用；
- 通过 AppleScript / `open` 引导用户将新 App 替换到原 Applications 目录；
- 不静默覆盖用户明确选择之外的应用目录；
- 首期不实现直接覆盖运行中的 `.app`；
- 正式版本要求 Developer ID 签名和 Apple 公证。

如果后续希望实现真正的“一键自动替换”，建议增加独立的外部 updater helper。helper 不承载业务逻辑，只负责等待主程序退出、校验下载文件、备份旧 App、原子替换和失败回滚。

## 版本策略

采用三段 SemVer：

```text
MAJOR.MINOR.PATCH
```

- `PATCH`：修复 bug、安全修复、兼容性修复；支持客户端自动更新。
- `MINOR`：新增功能且保持数据库和配置兼容；支持客户端自动更新。
- `MAJOR`：数据库结构、运行时、系统权限或升级协议存在不兼容变化；显示“需要重新安装”，不做静默升级。

对于数据库迁移，即使是跨多个小版本，只要迁移链可连续执行，也可以自动升级；不能只依据版本数字跳过迁移。

## 安全要求

- 不使用 GitHub API Token；公开 Releases 使用匿名 HTTPS 请求。
- 固定 GitHub API 域名和 HTTPS，不允许通过 Release 响应替换下载主机。
- 不信任 Release body 中的下载链接；只选择预期命名的 asset。
- 下载完成后必须校验 SHA-256 和平台签名。
- Windows 校验 Authenticode 签名；macOS 校验 Developer ID、Team ID 和公证状态。
- 更新包不能包含用户 SQLite、Token、Cookie、日志或业务文件。
- 下载 URL、Authorization、Token 和 Release 私有信息不得进入普通日志。
- 网络失败、限流和临时错误可重试；签名、哈希、平台不匹配不得盲目重试。
- 更新失败要保留当前可运行版本，不得先删除旧版本。

## 发布流水线

GitHub Actions 在 tag 推送时：

1. 使用构建矩阵分别生成 Windows x64、Windows arm64、macOS x64 和 macOS arm64 资产；
2. 执行 Go 测试、race 测试、前端构建；
3. 生成 NSIS exe 和 macOS DMG；
4. 代码签名和 macOS 公证；
5. 生成 SHA-256SUMS.txt；
6. 创建 GitHub Release；
7. 上传正式资产；
8. 发布前执行安装、升级和回滚验收。

## 分阶段实施

### 第一阶段：可检查更新

- GitHub Release API Adapter；
- SemVer 比较；
- 平台和架构筛选；
- 更新提示；
- 不自动下载。

### 第二阶段：下载并交给安装器

- 后台下载；
- SHA-256 校验；
- Windows 启动 NSIS 安装器；
- macOS 打开 DMG；
- 运行中批次保护。

### 第三阶段：一键升级和回滚

- Windows 外部 updater helper；
- macOS updater helper；
- 替换前备份；
- 替换失败回滚；
- 升级状态持久化；
- 升级后恢复队列。

## 已确认的产品策略（2026-08-31）

- GitHub 仓库 owner/name：待用户提供，客户端暂不硬编码仓库身份。
- Release 通道：支持正式 Release 和 prerelease；默认使用正式 Release，用户可在设置中切换到 prerelease 通道。
- 更新检查：应用启动后延迟 30 秒执行一次，之后每 24 小时检查一次；设置页提供“立即检查更新”。检查更新不阻塞同步队列。
- 自动下载：发现兼容的 PATCH/MINOR 更新后，默认后台下载到应用私有临时目录；下载前后均显示进度和结果，不下载到用户业务目录。
- 自动安装：不静默退出应用。下载完成后提示用户；存在运行中的同步批次时只允许“稍后安装”，批次结束后可再次安装。用户确认后启动外部安装器/updater。
- 平台：支持 macOS arm64、macOS x64、Windows x64 和 Windows arm64。发布流水线必须为每个平台生成并校验对应资产。
- MAJOR 版本：不执行无感覆盖升级，显示“需要重新安装”，但仍可自动打开对应下载页面或安装包。
- 仓库身份：待用户提供 GitHub owner/name 后再接入 Release API；在此之前只保留 Adapter 和配置接口，不猜测仓库地址。

## 仍需补充的发布凭据

- Windows Authenticode 代码签名证书及安全的 CI 密钥存储；
- macOS Developer ID Application 证书、Developer ID Installer（如发布 PKG）以及公证凭据；
- GitHub Actions 的发布权限和 Release 自动发布配置。
