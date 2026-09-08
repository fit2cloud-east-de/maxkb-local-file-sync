下面是当前项目 **Windows x64 / ARM64 安装包的完整打包流程**。流程以项目目录：

```text
C:\Program Files (x86)\VSCodeProject\maxkb-local-file-sync
```

为例。

> 注意：当前项目支持的 Windows 架构是：
>
> - `x64`，对应 `windows/amd64`
> - `arm64`，对应 `windows/arm64`
>
> 当前脚本**不支持 32 位 x86/386**。这里的 `x64` 不是 32 位的 `x86`。

---

# 一、Windows 打包需要的环境

## 1. 必需环境

需要安装：

1. Git
2. Go
3. Node.js 和 npm
4. Wails CLI v2.14.0
5. NSIS
6. Microsoft WebView2 Runtime

Wails 官方支持 Windows AMD64 和 ARM64，构建时需要 Go 和 npm；生成 Windows 安装包还需要 NSIS。([v2.wails.io](https://v2.wails.io/docs/gettingstarted/installation/?utm_source=openai))

当前项目的 `go.mod` 要求：

```text
Go 1.25.0
```

因此建议安装 Go 1.25.x 或更高兼容版本。

---

## 2. 推荐使用 winget 安装

使用 PowerShell 执行：

```powershell
winget install --id Git.Git -e
winget install --id GoLang.Go -e
winget install --id OpenJS.NodeJS.LTS -e
winget install --id NSIS.NSIS -e
```

安装完成后，**关闭当前 PowerShell 窗口并重新打开一个新的 PowerShell**，让环境变量生效。

---

## 3. 检查基础环境

```powershell
git --version
go version
node --version
npm --version
```

正常情况下应能看到类似：

```text
git version 2.x.x
go version go1.25.x windows/amd64
v22.x.x
10.x.x
```

检查 NSIS：

```powershell
Get-Command makensis
makensis /VERSION
```

如果能够显示 `makensis.exe` 路径和版本号，说明 NSIS 已正确安装。

常见安装路径：

```text
C:\Program Files (x86)\NSIS\makensis.exe
```

---

# 二、进入项目根目录

必须进入包含以下文件的项目根目录：

```text
go.mod
go.sum
wails.json
frontend\
scripts\
build\
```

执行：

```powershell
cd "C:\Program Files (x86)\VSCodeProject\maxkb-local-file-sync"
```

检查当前位置：

```powershell
Get-Location
Get-ChildItem go.mod
Get-ChildItem wails.json
Get-ChildItem scripts\build-windows.ps1
```

应该看到：

```text
C:\Program Files (x86)\VSCodeProject\maxkb-local-file-sync
```

> 不要在 `frontend` 目录直接执行 `build-windows.ps1`。  
> 前端目录只用于安装 npm 依赖和单独验证前端构建。

---

# 三、安装 Wails CLI

当前项目使用 Wails：

```text
github.com/wailsapp/wails/v2 v2.14.0
```

安装固定版本：

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.14.0
```

Wails 官方也支持通过 `go install` 安装 CLI。([v2.wails.io](https://v2.wails.io/docs/gettingstarted/installation/?utm_source=openai))

---

## 1. 配置 Wails 到当前 PowerShell 的 PATH

```powershell
$env:Path += ";$(go env GOPATH)\bin"
```

检查：

```powershell
wails version
```

期望看到：

```text
Wails CLI v2.14.0
```

如果 `wails` 找不到，检查：

```powershell
go env GOPATH
Test-Path "$(go env GOPATH)\bin\wails.exe"
```

如果文件存在，可以直接临时加入 PATH：

```powershell
$env:Path += ";$(go env GOPATH)\bin"
```

如果希望永久加入当前用户环境变量：

```powershell
$goBin = "$(go env GOPATH)\bin"
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")

if ($userPath -notlike "*$goBin*") {
    [Environment]::SetEnvironmentVariable(
        "Path",
        "$userPath;$goBin",
        "User"
    )
}
```

然后重新打开 PowerShell。

---

# 四、处理网络代理问题

如果机器设置了 `127.0.0.1:7890`，但 Clash 没有运行，Go 下载依赖时会出现：

```text
proxyconnect tcp: dial tcp 127.0.0.1:7890
```

先查看代理环境变量：

```powershell
Get-ChildItem Env:HTTP_PROXY
Get-ChildItem Env:HTTPS_PROXY
Get-ChildItem Env:ALL_PROXY
```

如果当前没有可用代理，可以临时清除：

```powershell
Remove-Item Env:HTTP_PROXY -ErrorAction SilentlyContinue
Remove-Item Env:HTTPS_PROXY -ErrorAction SilentlyContinue
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue
```

然后设置 Go 官方代理并允许直连：

```powershell
go env -w GOPROXY=https://proxy.golang.org,direct
```

如果所在网络访问 `proxy.golang.org` 较慢，可以改为：

```powershell
go env -w GOPROXY=https://goproxy.cn,direct
```

但如果系统环境变量仍然指向失效的 `127.0.0.1:7890`，依然会失败，需要先清除系统代理或启动对应代理软件。

重新执行：

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.14.0
```

---

# 五、检查 Wails 和 WebView2

执行：

```powershell
wails doctor
```

重点检查：

- Go 是否可用
- npm 是否可用
- Wails CLI 是否可用
- WebView2 是否存在
- Windows 构建环境是否正常

Wails Windows 应用运行依赖 Microsoft WebView2 Runtime；部分 Windows 系统已经自带，如果没有，需要安装。Wails 官方提供了 `wails doctor` 用于检查相关依赖。([v2.wails.io](https://v2.wails.io/docs/gettingstarted/installation/?utm_source=openai))

如果检查发现缺少 WebView2，可以安装：

```powershell
winget install --id Microsoft.EdgeWebView2Runtime
```

安装后重新执行：

```powershell
wails doctor
```

---

# 六、安装前端依赖

进入前端目录：

```powershell
cd "C:\Program Files (x86)\VSCodeProject\maxkb-local-file-sync\frontend"
```

清理可能不完整的依赖：

```powershell
if (Test-Path ".\node_modules") {
    Remove-Item ".\node_modules" -Recurse -Force
}
```

安装锁定版本依赖：

```powershell
npm ci --include=dev
```

项目构建需要开发依赖中的：

```text
vue-tsc
vite
typescript
```

验证：

```powershell
Test-Path ".\node_modules\.bin\vue-tsc.cmd"
Test-Path ".\node_modules\element-plus\es\index.d.ts"
```

两个命令都应该返回：

```text
True
```

单独测试前端：

```powershell
npm run build
```

成功时会看到：

```text
built in ...
```

然后返回项目根目录：

```powershell
cd ..
```

---

# 七、下载并校验 Go 依赖

在项目根目录执行：

```powershell
go mod download
go mod verify
```

建议先执行测试：

```powershell
go test ./... -count=1
go vet ./...
```

如果需要执行竞态检测：

```powershell
go test -race ./... -count=1
```

---

# 八、解除 PowerShell 脚本限制

如果执行脚本时出现：

```text
无法加载文件 build-windows.ps1
未对文件进行数字签名
```

在当前 PowerShell 窗口执行：

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
```

解除文件阻止标记：

```powershell
Unblock-File -Path ".\scripts\build-windows.ps1"
```

如果 `project.nsi` 或其他构建文件也被 Windows 标记为来自互联网，可以一起解除：

```powershell
Get-ChildItem -Path "." -Recurse -File |
    Unblock-File
```

然后再执行打包。

> `-Scope Process` 只对当前 PowerShell 窗口生效，关闭窗口后不会永久改变系统安全策略。

---

# 九、设置 PowerShell 中文输出

如果控制台里的中文显示为乱码，可以在当前窗口执行：

```powershell
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false)
chcp 65001
```

不过当前项目的 Windows 构建脚本已经将最终 exe 文件名设置为 ASCII：

```text
MaxKB-Local-File-Sync.exe
```

这样可以避免 Windows PowerShell 5.1 读取 UTF-8 脚本时，把中文输出文件名传给 Wails 后产生乱码。

用户看到的应用名称仍然是：

```text
MaxKB 本地文件同步工具
```

---

# 十、打包 Windows x64 安装包

确保当前目录是项目根目录：

```powershell
cd "C:\Program Files (x86)\VSCodeProject\maxkb-local-file-sync"
```

执行：

```powershell
.\scripts\build-windows.ps1 -Architecture x64
```

脚本内部会自动完成：

1. 读取 `wails.json` 中的版本号；
2. 安装或检查前端依赖；
3. 生成 Wails 前端绑定；
4. 编译 Vue 前端；
5. 生成应用资源；
6. 编译 Go Windows 应用；
7. 生成 x64 exe；
8. 调用 NSIS 生成安装程序；
9. 复制安装包到 `dist\windows`；
10. 生成 SHA-256 校验文件。

Wails 使用 `-nsis` 参数生成 NSIS 安装包，默认产物会放在 `build/bin`；当前项目脚本会进一步复制并重命名到 `dist/windows`。([wails.io](https://wails.io/docs/guides/windows-installer/?utm_source=openai))

---

## x64 的预期输出

当前版本为 `1.0.0` 时：

```text
build\bin\MaxKB-Local-File-Sync.exe
```

安装包：

```text
dist\windows\MaxKB-Local-File-Sync-v1.0.0-windows-x64-setup.exe
```

校验文件：

```text
dist\checksums\MaxKB-Local-File-Sync-v1.0.0-windows-x64.sha256
```

---

# 十一、打包 Windows ARM64 安装包

在同一个项目根目录执行：

```powershell
.\scripts\build-windows.ps1 -Architecture arm64
```

预期输出：

```text
build\bin\MaxKB-Local-File-Sync.exe
```

安装包：

```text
dist\windows\MaxKB-Local-File-Sync-v1.0.0-windows-arm64-setup.exe
```

校验文件：

```text
dist\checksums\MaxKB-Local-File-Sync-v1.0.0-windows-arm64.sha256
```

当前项目脚本使用：

```text
x64   -> windows/amd64
arm64 -> windows/arm64
```

Wails CLI 本身支持使用 `-platform windows/amd64` 和 `-platform windows/arm64` 指定目标平台。([v2.wails.io](https://v2.wails.io/docs/reference/cli/?utm_source=openai))

---

# 十二、是否必须在对应 CPU 的机器上打包？

不要求必须在对应 CPU 机器上打包。

一般可以在一台 Windows x64 机器上分别生成：

```text
windows/amd64
windows/arm64
```

但是建议：

- x64 安装包在 Windows x64 机器上做安装和启动验证；
- ARM64 安装包在 Windows ARM64 机器上做最终安装和启动验证；
- 如果没有 ARM64 真机，可以使用对应的虚拟机或测试设备验证。

也就是说：

```text
打包：可以集中在 Windows x64 机器完成
最终验证：最好在对应架构设备完成
```

当前脚本没有提供 32 位 Windows x86/386 目标。如果确实需要 32 位安装包，需要额外修改构建脚本、Wails 目标平台和安装包验证流程。

---

# 十三、指定版本打包

当前版本读取自：

```text
wails.json
```

当前配置：

```json
"productVersion": "1.0.0"
```

如果要打包其他版本，需要先修改：

```json
"productVersion": "1.0.1"
```

然后执行：

```powershell
.\scripts\build-windows.ps1 `
    -Version 1.0.1 `
    -Architecture x64
```

脚本会校验传入版本和 `wails.json` 中的版本必须一致。

如果版本不一致，会直接终止，避免生成文件名和应用内部版本不一致的安装包。

---

# 十四、只生成 exe，不生成安装包

如果只需要 Windows 可执行文件，可以执行：

```powershell
wails build `
    -clean `
    -platform windows/amd64 `
    -trimpath `
    -ldflags "-s -w -X main.appVersion=v1.0.0" `
    -nopackage `
    -o MaxKB-Local-File-Sync.exe
```

生成文件：

```text
build\bin\MaxKB-Local-File-Sync.exe
```

ARM64：

```powershell
wails build `
    -clean `
    -platform windows/arm64 `
    -trimpath `
    -ldflags "-s -w -X main.appVersion=v1.0.0" `
    -nopackage `
    -o MaxKB-Local-File-Sync.exe
```

正常发布时，建议优先使用项目脚本，因为脚本还会生成 NSIS 安装包和 SHA-256 校验文件。

---

# 十五、前端已经构建完成时跳过前端编译

如果前端已经确认构建完成，可以使用：

```powershell
.\scripts\build-windows.ps1 `
    -Architecture x64 `
    -SkipFrontend
```

ARM64：

```powershell
.\scripts\build-windows.ps1 `
    -Architecture arm64 `
    -SkipFrontend
```

但只有在确认 `frontend\dist` 内容是最新的情况下，才建议使用 `-SkipFrontend`。

通常第一次打包不要跳过前端。

---

# 十六、安装包中的安装方式

生成的安装程序是一个统一安装包，不再拆分成两个 setup.exe。

启动安装程序后可以选择：

## 1. 仅当前用户安装

默认目录类似：

```text
%LOCALAPPDATA%\Programs\MaxKB\MaxKB 本地文件同步工具
```

特点：

- 只对当前 Windows 用户生效；
- 通常不需要管理员权限；
- 不安装到系统级 Program Files。

## 2. 所有用户安装

默认目录类似：

```text
C:\Program Files\MaxKB\MaxKB 本地文件同步工具
```

特点：

- 所有用户可以使用；
- 需要管理员权限；
- Windows 会显示 UAC 确认。

## 3. 自定义目录

在安装向导的目录页面，可以手动选择安装目录。

安装目录只保存应用程序文件，用户数据和 SQLite 数据目录分开保存。升级或卸载应用时，不应删除任务、同步映射、日志和数据库。

---

# 十七、安装包验证流程

## 1. 验证文件是否存在

```powershell
Get-ChildItem ".\dist\windows"
Get-ChildItem ".\dist\checksums"
```

## 2. 验证 SHA-256

```powershell
Get-FileHash `
    ".\dist\windows\MaxKB-Local-File-Sync-v1.0.0-windows-x64-setup.exe" `
    -Algorithm SHA256
```

检查生成的 `.sha256` 文件：

```powershell
Get-Content `
    ".\dist\checksums\MaxKB-Local-File-Sync-v1.0.0-windows-x64.sha256"
```

## 3. 验证 exe 架构

可以使用 Visual Studio Developer PowerShell 中的：

```powershell
dumpbin /headers ".\build\bin\MaxKB-Local-File-Sync.exe" |
    Select-String "machine"
```

或者安装后直接在目标系统运行。

## 4. 验证启动

安装后检查：

- 应用是否能够正常启动；
- 窗口标题是否为：
  ```text
  MaxKB 本地文件同步工具
  ```
- 页面按钮是否可以点击；
- 系统设置是否可以打开；
- MaxKB 配置是否可以保存；
- MinerU 配置是否可以测试；
- SQLite 是否可以初始化；
- Windows Credential Manager 是否可以读写；
- 关闭应用后重新启动，任务和配置是否保留。

---

# 十八、安装包功能验证

建议至少验证以下场景。

## 当前用户安装

1. 启动安装包；
2. 选择“仅为当前用户安装”；
3. 选择自定义目录；
4. 完成安装；
5. 启动应用；
6. 创建一个测试任务；
7. 关闭应用；
8. 再次启动，确认任务仍存在。

## 所有用户安装

1. 启动安装包；
2. 选择“为所有用户安装”；
3. 确认出现 UAC；
4. 选择默认或自定义目录；
5. 完成安装；
6. 使用普通用户登录 Windows；
7. 确认应用可以启动；
8. 确认数据目录和凭据权限正常。

## 升级验证

1. 安装旧版本；
2. 创建测试任务；
3. 配置 MaxKB 和 MinerU；
4. 安装新版本；
5. 启动应用；
6. 确认任务、文件映射、同步记录仍然存在；
7. 确认系统凭据仍可读取。

## 卸载验证

卸载后应确认：

- 应用程序文件被删除；
- 开始菜单快捷方式被删除；
- 桌面快捷方式被删除；
- 用户数据目录默认保留；
- SQLite 数据库默认保留；
- 系统凭据不会因为普通程序文件卸载而丢失。

---

# 十九、常见问题处理

## 问题 1：`go` 不是命令

错误：

```text
'go' 不是内部或外部命令
```

处理：

1. 安装 Go；
2. 关闭当前终端；
3. 重新打开 PowerShell；
4. 执行：

```powershell
go version
```

如果仍然找不到，检查：

```powershell
Test-Path "C:\Program Files\Go\bin\go.exe"
```

临时加入 PATH：

```powershell
$env:Path += ";C:\Program Files\Go\bin"
```

---

## 问题 2：`wails` 不是命令

执行：

```powershell
$env:Path += ";$(go env GOPATH)\bin"
wails version
```

检查：

```powershell
Test-Path "$(go env GOPATH)\bin\wails.exe"
```

---

## 问题 3：`vue-tsc` 找不到

错误：

```text
'vue-tsc' is not recognized
```

进入前端目录执行：

```powershell
cd ".\frontend"

Remove-Item ".\node_modules" -Recurse -Force -ErrorAction SilentlyContinue

npm ci --include=dev

Test-Path ".\node_modules\.bin\vue-tsc.cmd"

npm run build
```

必须确保：

```text
Test-Path ".\node_modules\.bin\vue-tsc.cmd"
```

返回：

```text
True
```

不要设置：

```powershell
npm config set omit dev
```

也不要在生产依赖模式下执行安装。

---

## 问题 4：PowerShell 脚本无法执行

错误：

```text
未对文件进行数字签名
```

执行：

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
Unblock-File -Path ".\scripts\build-windows.ps1"
```

再重新运行：

```powershell
.\scripts\build-windows.ps1 -Architecture x64
```

---

## 问题 5：NSIS 找不到

检查：

```powershell
Get-Command makensis
```

如果找不到，临时加入：

```powershell
$env:Path += ";C:\Program Files (x86)\NSIS"
```

再检查：

```powershell
makensis /VERSION
```

---

## 问题 6：`project.nsi` 编码错误

错误：

```text
Bad text encoding: project.nsi
```

当前项目已经对 NSIS 文件编码和 Windows 中文输出做过处理。建议按以下顺序操作：

```powershell
git pull
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
Unblock-File -Path ".\scripts\build-windows.ps1"
Get-ChildItem -Path ".\build\windows" -Recurse -File | Unblock-File
```

然后重新打包。

不要直接用旧项目目录中的 `project.nsi` 覆盖当前版本。

---

## 问题 7：`IntFmt expects 3 parameters`

错误：

```text
IntFmt expects 3 parameters, got 2
```

这是旧版 `project.nsi` 中的参数写法问题。当前版本已经修复，正确写法类似：

```nsi
IntFmt $0 "0x%08X" $0
```

建议重新获取最新仓库代码后再构建，不要继续使用旧的 `build/windows/installer/project.nsi`。

---

## 问题 8：构建输出文件名乱码

如果看到类似：

```text
MaxKB 鏈湴鏂囦欢鍚屾宸ュ叿.exe
```

说明使用了旧构建脚本或旧 Wails 输出配置。

当前脚本已经固定使用 ASCII 文件名：

```text
MaxKB-Local-File-Sync.exe
```

重新确认：

```powershell
Get-Content ".\scripts\build-windows.ps1" |
    Select-String "AppBinaryName"
```

应该能看到：

```powershell
$AppBinaryName = "MaxKB-Local-File-Sync.exe"
```

---

## 问题 9：下载 Go 依赖时连接 `127.0.0.1:7890`

先清除当前 PowerShell 代理：

```powershell
Remove-Item Env:HTTP_PROXY -ErrorAction SilentlyContinue
Remove-Item Env:HTTPS_PROXY -ErrorAction SilentlyContinue
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue
```

然后：

```powershell
go env -w GOPROXY=https://proxy.golang.org,direct
```

如果使用 Clash，则先确认 Clash 确实监听：

```text
127.0.0.1:7890
```

否则不要保留该代理配置。

---

## 问题 10：安装包生成成功，但应用启动失败

优先检查：

```powershell
wails doctor
```

重点确认 WebView2 Runtime 是否可用。

也可以先直接运行：

```powershell
.\build\bin\MaxKB-Local-File-Sync.exe
```

如果 exe 可以运行，而安装后不能运行，重点检查：

- 安装目录权限；
- WebView2 Runtime；
- Windows Defender 是否拦截；
- 当前用户是否可以访问应用数据目录；
- 是否安装到了受限目录。

---

# 二十、一次性完整命令清单

下面是一套从项目根目录开始执行的完整流程：

```powershell
cd "C:\Program Files (x86)\VSCodeProject\maxkb-local-file-sync"

Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
Unblock-File -Path ".\scripts\build-windows.ps1"

[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false)
chcp 65001

git status
go version
node --version
npm --version

$env:Path += ";$(go env GOPATH)\bin"

wails version
wails doctor

go mod download
go mod verify

cd ".\frontend"

Remove-Item ".\node_modules" -Recurse -Force -ErrorAction SilentlyContinue
npm ci --include=dev
npm run build

cd ..

go test ./... -count=1
go vet ./...

.\scripts\build-windows.ps1 -Architecture x64
```

生成 ARM64：

```powershell
.\scripts\build-windows.ps1 -Architecture arm64
```

查看产物：

```powershell
Get-ChildItem ".\dist\windows"
Get-ChildItem ".\dist\checksums"
```

---

# 二十一、最终发布目录

最终可以把以下文件上传到 GitHub Releases：

```text
dist/windows/MaxKB-Local-File-Sync-v1.0.0-windows-x64-setup.exe
dist/windows/MaxKB-Local-File-Sync-v1.0.0-windows-arm64-setup.exe
dist/checksums/MaxKB-Local-File-Sync-v1.0.0-windows-x64.sha256
dist/checksums/MaxKB-Local-File-Sync-v1.0.0-windows-arm64.sha256
```

不要上传：

```text
真实 API Key
真实 Token
Cookie
业务文件
SQLite 数据库
运行日志
本地用户配置
签名证书
证书密码
```

如果要进行正式发布，建议后续增加：

- Windows 代码签名证书；
- GitHub Actions 自动构建；
- GitHub Releases 自动上传；
- 安装包 SHA-256 校验；
- 发布版本说明；
- x64 和 ARM64 分开标记；
- 在真实 Windows x64 和 ARM64 设备上做启动验证。

Wails 官方支持通过 `-nsis` 生成 NSIS 安装包，并支持 `windows/amd64`、`windows/arm64` 目标平台。([wails.io](https://wails.io/docs/guides/windows-installer/?utm_source=openai))