# 发布签名与公证指南

本文只说明如何准备发布凭据，不包含任何真实证书、私钥、Token、密码或用户资料。

## 1. Windows Authenticode 签名

### 推荐选择

Windows 安装包是 NSIS `.exe`，正式对外发布应使用公开信任的 Authenticode 代码签名证书。可以选择：

1. 向公开证书颁发机构（CA）购买 OV/EV Code Signing Certificate；
2. 使用组织已有的公开代码签名证书；
3. 使用 Microsoft 的云托管签名服务，避免在 CI 中长期保存私钥；
4. 自签名证书只用于内测，不能作为正式发布凭据。

微软文档说明，代码签名证书可以从证书供应商购买、由组织内部签发，或使用自签名证书进行测试。正式发布不应使用自签名证书。

### 获取步骤

1. 准备发布主体信息：公司/组织名称、注册地址、域名和联系人信息；
2. 在选定 CA 的代码签名产品页提交申请；
3. 完成 CA 要求的组织验证；
4. 按 CA 要求生成 CSR，或使用硬件/云托管密钥生成方式；
5. 证书签发后导入 Windows 证书存储，或者导出受密码保护的 `.pfx`；
6. 确认用途包含 Code Signing，证书链完整且未过期；
7. 在 Windows 构建机安装 Windows SDK，使用 `signtool.exe` 签名；
8. 使用 CA 提供的 RFC 3161 时间戳服务，避免证书过期后历史版本失去有效时间证明。

本项目脚本当前支持对生成的 Windows 安装包执行签名：

```powershell
.\scripts\build-windows.ps1 `
  -Version 1.0.0 `
  -Sign `
  -CertificateFile C:\secure\maxkb-signing.pfx `
  -CertificatePassword $env:WINDOWS_CERT_PASSWORD
```

正式发布流水线还应对应用本体 `.exe` 一并签名，再将已签名应用打入 NSIS 安装包；证书私钥不得提交到仓库。

### GitHub Actions 保存方式

优先级从高到低：

1. 云托管签名：GitHub Actions 使用 OIDC 获取短期身份，不在仓库保存长期私钥；
2. 将 `.pfx` 转为 Base64 后保存为 GitHub Actions Environment Secret，同时单独保存证书密码；
3. 仅在受保护的发布环境中允许签名作业，限制 tag、分支和审批人。

禁止：

- 把 `.pfx`、`.pem`、私钥或密码提交到 Git；
- 将证书密码写进 workflow 明文；
- 在普通构建日志打印证书内容或命令行密码；
- 使用自签名证书生成正式 Release。

## 2. macOS Developer ID 与公证

本项目采用 DMG 分发，因此核心凭据是 `Developer ID Application`。只有在改为 PKG 分发时才需要额外使用 `Developer ID Installer`。

### 获取步骤

1. 注册并加入 Apple Developer Program；
2. 由团队 Account Holder 登录 Apple Developer 账户；
3. 在 macOS 的“钥匙串访问”中创建证书签名请求（CSR）；
4. 打开 Certificates, Identifiers & Profiles，选择 Certificates，点击 `+`；
5. 选择 `Developer ID Application`，上传 CSR 并下载 `.cer`；
6. 双击 `.cer` 导入钥匙串，确认“我的证书”中同时存在证书和对应私钥；
7. 将证书和私钥导出为受密码保护的 `.p12`，只放入受保护的发布环境；
8. 在构建机启用 Hardened Runtime，使用 Developer ID Application 对 App 及其嵌套可执行文件签名；
9. 使用 `notarytool` 提交 App 或 DMG 公证；
10. 公证通过后使用 `stapler` 将票据附加到可分发产物，并执行 Gatekeeper 验证。

Apple 官方文档明确要求，站外分发的 macOS 软件使用 Developer ID 签名并提交公证；自定义自动化流程应使用 `notarytool`，不要继续使用已停止支持的 `altool` 上传流程。

### 公证凭据

推荐使用 App Store Connect API Key：

- Issuer ID；
- Key ID；
- 只下载一次的 `.p8` 私钥文件；
- Apple Developer Team ID。

也可以在专用 macOS 构建机使用 Apple ID + App-Specific Password，但不应将 Apple ID 密码写入 CI。

### 本地验证命令示例

```bash
security find-identity -v -p codesigning
codesign --verify --deep --strict --verbose=2 "MaxKB 本地文件同步工具.app"
spctl --assess --type execute --verbose=4 "MaxKB 本地文件同步工具.app"
xcrun notarytool submit "MaxKB-Local-File-Sync-v1.0.0-macos-arm64.dmg" \
  --keychain-profile "MAXKB_NOTARY" \
  --wait
xcrun stapler staple "MaxKB-Local-File-Sync-v1.0.0-macos-arm64.dmg"
xcrun stapler validate "MaxKB-Local-File-Sync-v1.0.0-macos-arm64.dmg"
```

`MAXKB_NOTARY` 是本机钥匙串中的 profile 名称，不是固定凭据，也不得写入源码。

### GitHub Actions 保存方式

建议使用 GitHub Environment `release` 保存：

- `MACOS_CERTIFICATE_P12_BASE64`；
- `MACOS_CERTIFICATE_PASSWORD`；
- `MACOS_KEYCHAIN_PASSWORD`；
- `APPLE_API_KEY_P8_BASE64`；
- `APPLE_API_KEY_ID`；
- `APPLE_API_ISSUER_ID`；
- `APPLE_TEAM_ID`。

发布 workflow 中临时创建钥匙串，导入 `.p12`，完成签名和公证后删除临时钥匙串及解码文件。任何失败路径都要清理临时文件。

## 3. 当前项目的落地顺序

1. 先提供 GitHub 仓库 owner/name；
2. 建立 `release` Environment 和审批规则；
3. 配置 Windows 公开代码签名；
4. 配置 Apple Developer ID Application 和 notarization；
5. 先实现正式 Release 自动发布；
6. 再接入 prerelease 通道和客户端自动更新；
7. 在 Windows x64、Windows arm64、macOS x64、macOS arm64 分别完成安装、升级、回滚和 Gatekeeper/SmartScreen 验收。
