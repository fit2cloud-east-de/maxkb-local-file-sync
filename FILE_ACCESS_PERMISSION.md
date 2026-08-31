# macOS 文件访问权限设置

如果在选择目录时无法看到某些文件夹（如 Containers 目录下的企业微信等应用），需要为应用授予完全磁盘访问权限。

## 操作步骤

### 方法一：系统设置（推荐）

1. 打开"系统设置"（System Settings）
2. 点击"隐私与安全性"（Privacy & Security）
3. 在左侧列表中找到"完全磁盘访问权限"（Full Disk Access）
4. 点击右下角的锁图标 🔒，输入密码解锁
5. 点击 ➕ 号按钮
6. 在开发模式下，找到并添加：
   - 应用路径：`/Users/maekblack/VSCodeProjects/maxkb-local-file-sync/maxkb-local-file-sync/build/bin/MaxKB 本地文件同步工具.app`
7. 确保应用旁边的开关是打开状态 ✅
8. 重启应用使权限生效

### 方法二：命令行授权（开发模式）

在开发模式下，也可以通过以下命令为终端授予完全磁盘访问权限：

```bash
# 为当前使用的终端应用授予权限
# 如果使用 iTerm2:
tccutil reset SystemPolicyAllFiles com.googlecode.iterm2

# 如果使用默认终端 Terminal.app:
tccutil reset SystemPolicyAllFiles com.apple.Terminal
```

然后在系统设置中手动添加对应的终端应用到"完全磁盘访问权限"列表。

## 需要访问的目录

应用需要访问以下特殊目录：

- `~/Library/Containers/` - 各种应用的沙盒容器（包括企业微信、钉钉等）
- `~/Library/Group Containers/` - 应用组共享容器
- `~/Documents/` - 用户文档目录
- `~/Desktop/` - 桌面目录
- 其他用户指定的任意目录

## 开发模式注意事项

- 每次重新构建应用后，可能需要重新授权
- 开发模式的应用路径是 `build/bin/*.app`
- 生产版本需要在打包后重新授权

## 验证权限

授权后，打开应用并点击"选择目录…"按钮，应该能够看到：

✅ Containers 目录下的所有子目录
✅ 企业微信、钉钉等应用的数据目录
✅ 任意用户目录

如果仍然看不到某些目录，请：

1. 完全退出应用
2. 重新打开应用
3. 再次尝试选择目录

## 安全说明

完全磁盘访问权限允许应用读取系统中的所有文件。本应用：

- 只读取用户手动选择的目录
- 不会扫描或访问未经授权的目录
- 所有文件操作都需要用户明确确认
- 代码完全开源，可供审查
