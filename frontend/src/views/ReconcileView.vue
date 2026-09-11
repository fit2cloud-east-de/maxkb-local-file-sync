<script lang="ts" setup>
import { onMounted, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import * as App from '../../wailsjs/go/main/App'
import type { ReconcileDTO } from '../types'
import { useTasksStore } from '../stores/tasks'
import { errorMessage, requireArray, withTimeout } from '../stores/store-helpers'
import { notifyError, notifySuccess } from '../utils/notify'
import { stageLabel } from '../utils/status'
import StatusBadge from '../components/StatusBadge.vue'

const tasksStore = useTasksStore()
const items = ref<ReconcileDTO[]>([])
const loading = ref(false)
const error = ref('')
const busy = ref('')
const idOf = (item: ReconcileDTO) => item.runFileId || item.runFileID || ''
const PAGE_CALL_TIMEOUT_MS = 15_000

const errorCategoryLabels: Record<string, string> = {
  MINERU_CONVERT: 'MinerU 转换',
  MINERU_DOWNLOAD: 'MinerU 文件下载',
  MINERU_RESULT: 'MinerU 结果处理',
  MAXKB_UPLOAD: '文件上传 MaxKB',
  MAXKB_SPLIT: 'MaxKB 智能分段',
  MAXKB_CREATE: 'MaxKB 创建文档',
  MAXKB_DELETE: 'MaxKB 删除文档',
  LOCAL_SNAPSHOT: '本地文件读取',
  CONFIGURATION: '服务配置',
  UNSUPPORTED_FILE_TYPE: '文件格式',
  SOURCE_CHANGED: '本地文件变化',
  LOCAL_SYSTEM: '本地存储或执行服务',
  RECONCILE: '远端状态待人工确认',
}

function errorType(item: ReconcileDTO) {
  const category = item.errorCategory === 'MAXKB_SPLIT' && item.minerUTaskID
    ? 'MinerU 产物上传及 MaxKB 智能分段'
    : item.errorCategory ? errorCategoryLabels[item.errorCategory] : ''
  if (category && item.errorCode) return `${category}（${item.errorCode}）`
  return category || item.errorCode || '远端操作结果不明确'
}

async function load() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    items.value = requireArray<ReconcileDTO>(
      await withTimeout(() => App.ListReconcileRequired(), '读取异常项', PAGE_CALL_TIMEOUT_MS),
      '异常项',
    )
  } catch (e: unknown) {
    error.value = errorMessage(e, '读取异常项失败')
  } finally {
    loading.value = false
  }
}

async function askForResolution(item: ReconcileDTO, resolution: string): Promise<string | null> {
  if (resolution === 'REMOTE_SUCCEEDED') {
    try {
      const result = await ElMessageBox.prompt(
        '请输入已在 MaxKB 中确认存在的 document ID。客户端只会记录该 ID，不会重复创建文档。',
        '确认远端操作成功',
        {
          inputValue: item.maxKBDocumentID || '',
          inputPlaceholder: 'MaxKB document ID',
          inputValidator: value => Boolean(value?.trim()) || '请输入 document ID',
          confirmButtonText: '确认成功',
          cancelButtonText: '取消',
        },
      )
      return result.value.trim()
    } catch {
      return null
    }
  }

  const isRetry = resolution === 'REMOTE_ABSENT_RETRY'
  try {
    await ElMessageBox.confirm(
      isRetry
        ? '仅在已确认远端不存在对应结果时继续。确认后将重新执行可能非幂等的步骤。'
        : '确认将该文件标记为失败？快照和远端引用会保留用于审计。',
      isRetry ? '确认远端不存在并重试' : '标记异常项处理失败',
      {
        type: isRetry ? 'warning' : 'error',
        confirmButtonText: isRetry ? '确认并重试' : '确认标记失败',
        cancelButtonText: '取消',
      },
    )
    return ''
  } catch {
    return null
  }
}

async function resolve(item: ReconcileDTO, resolution: string) {
  const id = idOf(item)
  if (!id || busy.value) return

  const documentID = await askForResolution(item, resolution)
  if (documentID === null) return

  busy.value = id
  error.value = ''
  try {
    await withTimeout(() => App.ResolveReconcile(id, resolution, documentID), '处理异常项', PAGE_CALL_TIMEOUT_MS)
    await Promise.all([load(), tasksStore.refreshStatus()])
    notifySuccess(resolution === 'MARK_FAILED' ? '已标记为失败' : '异常项已处理')
  } catch (e: unknown) {
    error.value = errorMessage(e, '处理异常项失败')
    notifyError(error.value)
  } finally {
    busy.value = ''
  }
}

onMounted(() => { void load() })
</script>

<template>
  <div class="view-page">
    <header class="page-header">
      <div><h1>异常处理</h1></div>
      <button class="btn btn-secondary" :disabled="loading" @click="load">↻ 刷新</button>
    </header>
    <div v-if="loading" class="loading">加载中…</div>
    <div v-else-if="error" class="error-msg">{{ error }}</div>
    <div v-else-if="!items.length" class="empty-state"><h3>无待处理项</h3><p>当前没有需要人工处理的文件。</p></div>
    <div v-else class="reconcile-list">
      <article v-for="item in items" :key="idOf(item)" class="panel reconcile-card">
        <div class="panel-title"><div><p class="eyebrow">{{ item.folderName }}</p><h2 class="mono">{{ item.relativePath }}</h2></div><StatusBadge status="RECONCILE_REQUIRED" /></div>
        <p class="reason">{{ item.reason || '远端操作结果未知' }}</p>
        <dl class="facts"><div><dt>阶段</dt><dd>{{ stageLabel(item.processingStage) }}</dd></div><div><dt>异常类型</dt><dd>{{ errorType(item) }}</dd></div><div><dt>文档 ID</dt><dd class="mono">{{ item.maxKBDocumentID || item.maxKBSourceFileID || '未知' }}</dd></div><div><dt>批次任务</dt><dd class="mono">{{ item.maxKBBatchTaskID || '未知' }}</dd></div><div><dt>快照 MD5</dt><dd class="mono">{{ item.snapshotMD5 || '未知' }}</dd></div></dl>
        <div class="actions">
          <button class="btn btn-primary" :disabled="Boolean(busy)" @click="resolve(item, 'REMOTE_SUCCEEDED')">确认远端成功</button>
          <button class="btn btn-secondary" :disabled="Boolean(busy)" @click="resolve(item, 'REMOTE_ABSENT_RETRY')">确认不存在并重试</button>
          <button class="btn btn-danger" :disabled="Boolean(busy)" @click="resolve(item, 'MARK_FAILED')">标记失败</button>
        </div>
      </article>
    </div>
  </div>
</template>
