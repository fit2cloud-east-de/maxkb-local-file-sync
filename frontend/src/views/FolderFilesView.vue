<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { FileDTO, FileStatsDTO } from '../types'
import StatusBadge from '../components/StatusBadge.vue'
import * as App from '../../wailsjs/go/main/App'
import { errorMessage, withTimeout } from '../stores/store-helpers'
import { ElMessageBox } from 'element-plus'
import { notifyError, notifySuccess } from '../utils/notify'

const route = useRoute()
const router = useRouter()
const folderId = route.params.folderId as string

const files = ref<FileDTO[]>([])
const stats = ref<FileStatsDTO>({ total: 0, synced: 0, pending: 0, stale: 0, failed: 0, needsDelete: 0 })
const loading = ref(false)
const error = ref<string | null>(null)
const activeTab = ref('ALL')
const folderName = ref('')
const PAGE_CALL_TIMEOUT_MS = 15_000

const tabs = [
  { key: 'ALL', label: '全部' },
  { key: 'PENDING', label: '待同步' },
  { key: 'SYNCED', label: '已同步' },
  { key: 'FAILED', label: '失败' },
  { key: 'STALE_REMOTE_EXISTS', label: '已过期' },
  { key: 'NEEDS_DELETE', label: '待删除' },
]

const filteredFiles = computed(() => {
  if (activeTab.value === 'ALL') return files.value
  return files.value.filter(f => f.fileStatus === activeTab.value)
})

onMounted(async () => {
  loading.value = true
  error.value = null
  try {
    const [folderData, fileList, fileStats] = await Promise.all([
      withTimeout(() => App.GetFolder(folderId), '读取同步任务', PAGE_CALL_TIMEOUT_MS),
      withTimeout(() => App.ListFiles(folderId), '读取文件列表', PAGE_CALL_TIMEOUT_MS),
      withTimeout(() => App.GetFileStats(folderId), '读取文件统计', PAGE_CALL_TIMEOUT_MS),
    ])
    folderName.value = folderData.name
    files.value = fileList
    stats.value = { ...fileStats, failed: 0, stale: fileStats.stale ?? 0 }
  } catch (e: any) {
    error.value = errorMessage(e, '读取文件状态失败')
  } finally {
    loading.value = false
  }
})

async function switchTab(tab: string) {
  activeTab.value = tab
}

async function deleteFile(fileId: string) {
  try {
    await ElMessageBox.confirm(
      '只移除本地跟踪记录，不会删除本地文件或 MaxKB 文档。',
      '移除文件跟踪记录',
      { type: 'warning', confirmButtonText: '确认移除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await withTimeout(() => App.DeleteFile(fileId), '移除文件跟踪记录', PAGE_CALL_TIMEOUT_MS)
    files.value = files.value.filter(f => f.fileId !== fileId)
    const refreshed = await withTimeout(() => App.GetFileStats(folderId), '刷新文件统计', PAGE_CALL_TIMEOUT_MS)
    stats.value = { ...refreshed, failed: 0, stale: refreshed.stale ?? 0 }
    notifySuccess('文件跟踪记录已移除')
  } catch (e: any) {
    notifyError('移除失败：' + errorMessage(e, '本地服务不可用'))
  }
}

function formatDate(s: string) {
  if (!s) return '—'
  return new Date(s).toLocaleString()
}
</script>

<template>
  <div class="view">
    <div class="view-header">
      <button class="btn btn-ghost btn-sm" @click="router.push('/folders')">← 返回</button>
      <h2>{{ folderName || '文件列表' }}</h2>
    </div>

    <div class="stats-strip">
      <span>共 <strong>{{ stats.total }}</strong></span>
      <span class="s-synced">已同步 <strong>{{ stats.synced }}</strong></span>
      <span class="s-pending">待处理 <strong>{{ stats.pending }}</strong></span>
      <span class="s-failed">失败 <strong>{{ stats.failed }}</strong></span>
      <span class="s-stale">过期 <strong>{{ stats.stale }}</strong></span>
      <span class="s-delete">待删 <strong>{{ stats.needsDelete }}</strong></span>
    </div>

    <div class="tabs">
      <button
        v-for="tab in tabs" :key="tab.key"
        class="tab-btn" :class="{ active: activeTab === tab.key }"
        @click="switchTab(tab.key)"
      >{{ tab.label }}</button>
    </div>

    <div v-if="loading" class="loading">加载中…</div>
    <div v-else-if="error" class="error-msg">{{ error }}</div>
    <div v-else-if="filteredFiles.length === 0" class="empty-state">该分类下暂无文件。</div>

    <div v-else class="file-table-wrap">
      <table class="file-table">
        <thead>
          <tr>
            <th>路径</th>
            <th>状态</th>
            <th>更新时间</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="file in filteredFiles" :key="file.fileId">
            <td class="file-path">{{ file.relativePath }}</td>
            <td><StatusBadge :status="file.fileStatus" type="file" /></td>
            <td class="file-date">{{ formatDate(file.updatedAt) }}</td>
            <td>
              <button class="btn btn-danger btn-xs" @click="deleteFile(file.fileId)">移除</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.view { padding: 28px; }
.view-header { display: flex; align-items: center; gap: 16px; margin-bottom: 20px; }
.view-header h2 { margin: 0; font-size: 20px; }
.stats-strip { display: flex; gap: 20px; font-size: 13px; color: var(--text-secondary); margin-bottom: 18px; flex-wrap: wrap; }
.stats-strip strong { color: var(--text-primary); }
.s-synced strong { color: var(--success); }
.s-pending strong { color: var(--warning); }
.s-failed strong { color: var(--danger); }
.s-stale strong, .s-delete strong { color: var(--warning); }
.tabs { display: flex; gap: 2px; margin-bottom: 18px; border-bottom: 1px solid #1e2d50; }
.tab-btn {
  background: none; border: none; color: var(--text-secondary);
  padding: 8px 14px; cursor: pointer; font-size: 13px; border-bottom: 2px solid transparent;
  transition: color 0.2s, border-color 0.2s;
}
.tab-btn:hover { color: var(--text-primary); }
.tab-btn.active { color: var(--primary); border-bottom-color: var(--primary); }
.file-table-wrap { overflow-x: auto; }
.file-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.file-table th {
  text-align: left; padding: 10px 12px; color: var(--text-secondary);
  font-size: 11px; text-transform: uppercase; background: #fafbfc; border-bottom: 1px solid var(--border);
}
.file-table td { padding: 10px 12px; color: var(--text-primary); border-bottom: 1px solid var(--border); }
.file-table tr:last-child td { border-bottom: 0; }
.file-table tr:hover td { background: #fbfbff; color: var(--text-primary); }
.file-path { font-family: monospace; font-size: 12px; word-break: break-all; }
.file-date { font-size: 11px; color: var(--text-secondary); white-space: nowrap; }
.empty-state { text-align: center; padding: 40px 0; color: var(--text-secondary); }
</style>
