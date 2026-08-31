# Wails 构建资源

本目录包含 MaxKB 本地文件同步客户端的 Wails 平台构建资源：

- `bin/`：构建输出目录，生成文件不应提交到源码仓库。
- `darwin/`：macOS `Info.plist` 等资源。
- `windows/`：Windows manifest、图标和安装器资源。

## 构建命令

### macOS Intel / Apple Silicon DMG

在 macOS 主机执行：

```bash
./scripts/build-macos-dmg.sh
# Intel Mac
MACOS_ARCH=x64 ./scripts/build-macos-dmg.sh
```

脚本默认构建 `darwin/arm64` Wails `.app`；设置 `MACOS_ARCH=x64` 时构建 Intel 版本，并生成标准 Finder 拖拽式 DMG，输出到 `dist/macos/`。DMG 内包含 `Applications` 快捷方式和中文安装说明。签名、公证通过 `CODESIGN_IDENTITY`、`DMG_SIGN_IDENTITY` 和 `NOTARY_PROFILE` 环境变量可选启用。

### Windows x64 / ARM64 NSIS

在 Windows 主机执行：

```powershell
# x64
.\scripts\build-windows.ps1 -Architecture x64
# ARM64
.\scripts\build-windows.ps1 -Architecture arm64
```

脚本会生成一个 NSIS `.exe`，安装向导中可选择“仅当前用户安装”或“所有用户安装”，并支持自定义安装目录。需要安装 Go、Node.js、Wails CLI 和 NSIS。

`GOOS=windows GOARCH=amd64 go build ./...` 只能验证 Go 包交叉编译，不能替代 Wails GUI、WebView2、文件夹选择器、NSIS、UAC 或 Credential Manager 的 Windows 实机验收。

## 当前验证边界

- 前端 production build、裸 Go build 和 `go vet` 已通过。
- 当前 macOS 已完成 Wails `darwin/arm64` `.app` 构建、DMG 生成、应用元数据校验和 SHA-256 校验；x64、Windows x64/arm64 仍需在对应目标环境实机验收。
- 当前没有 Windows 主机和 `makensis`，尚未在本机完成 Windows `.exe` 安装包构建。
- 尚未完成签名、公证、Windows 安装启动和两个平台的完整升级/卸载实机验收。

构建产物不得包含真实凭据、Cookie、用户资料或业务文件内容。发布前请在目标平台执行签名、安装、启动、升级和数据目录保留测试。
