# MaxKB 本地文件同步客户端前端

本目录是 Wails 桌面应用的 Vue 3 + TypeScript 前端，不是独立 Web 产品。

## 技术栈

- Vue 3 `<script setup>`
- TypeScript
- Vite
- Element Plus
- Pinia
- Vue Router
- `lucide-vue-next`

## 页面

- `src/views/FoldersView.vue`：同步任务、工作空间/知识库选择、目录绑定、文件匹配预览。
- `src/views/TasksView.vue`：持久化队列、批次控制和文件明细。
- `src/views/FolderFilesView.vue`：单任务文件状态。
- `src/views/ReconcileView.vue`：处理 `RECONCILE_REQUIRED`。
- `src/views/SettingsView.vue`：MaxKB、MinerU 配置和连接测试。

Wails 生成的绑定位于 `wailsjs/go`，后端业务逻辑不应直接堆在绑定文件中。修改 Go 暴露的方法后，使用 Wails 重新生成绑定并检查 TypeScript 类型。

## 本地开发

```bash
npm install
npm run dev
```

## 类型检查和生产构建

```bash
npm run build
```

该命令先执行 `vue-tsc --noEmit`，再执行 Vite production build。当前构建可能提示 JavaScript/CSS chunk 较大；这是性能警告，不等同于构建失败。

## 安全边界

不得在前端源码、调试输出、截图或构建产物中写入真实 API Key、Token、Cookie、用户资料或业务文件内容。已保存凭据只通过后端返回掩码值，实际凭据由 macOS Keychain 或 Windows Credential Manager 管理。

更多产品、数据库、构建和真实服务契约说明见上级 [`../README.md`](../README.md) 与 [`../UPDATE_PLAN.md`](../UPDATE_PLAN.md)。
