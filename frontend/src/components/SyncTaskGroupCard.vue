<script lang="ts" setup>
import { computed } from 'vue'
import { AlertTriangle, CheckCircle2, ChevronRight, Clock3, FolderSync, Play, RefreshCw, Square, Timer, XCircle } from 'lucide-vue-next'
import { ElButton } from 'element-plus'
import StatusBadge from './StatusBadge.vue'
import { stageLabel } from '../utils/status'
import type { SyncTaskGroupDTO } from '../types'

const props = defineProps<{ group: SyncTaskGroupDTO; busyTaskId?: string }>()
const emit = defineEmits<{
  (e: 'view', groupKey: string): void
  (e: 'view-error', taskId: string): void
  (e: 'pause', taskId: string): void
  (e: 'resume', taskId: string): void
  (e: 'stop', taskId: string): void
  (e: 'retry', taskId: string): void
}>()

const latest = computed(() => props.group.latest)
// Only the latest batch can produce the inline failure summary. The group-level
// failedRuns count also includes historical batches, so using it here could show
// "最近批次有 0 个文件处理失败" after a later successful/empty batch.
const hasFailure = computed(() => latest.value.failedCount > 0 || Boolean(latest.value.errorMessage || latest.value.errorSummary))
const canResume = computed(() => ['PAUSED', 'INTERRUPTED'].includes(latest.value.runStatus))
const canRetry = computed(() => ['FAILED', 'PARTIAL_SUCCESS'].includes(latest.value.runStatus) && latest.value.failedCount > 0)
const isBusy = computed(() => props.busyTaskId === latest.value.taskId)
const triggerLabel = computed(() => {
  if (latest.value.triggerType === 'cron') return '定时触发'
  if (latest.value.triggerType === 'single_file_retry') return '失败重试'
  return '手动触发'
})
const progressLabel = computed(() => {
  switch (latest.value.runStatus) {
    case 'SUCCESS': return '同步完成'
    case 'PARTIAL_SUCCESS': return '部分完成'
    case 'FAILED': return '同步失败'
    case 'RUNNING': return '处理中'
    case 'QUEUED': return '等待执行'
    case 'PAUSE_REQUESTED': return '正在暂停'
    case 'PAUSED': return '已暂停'
    case 'STOP_REQUESTED': return '正在停止'
    case 'STOPPED': return '已停止'
    case 'CANCELLED': return '已取消'
    case 'INTERRUPTED': return '已中断'
    default: return stageLabel(latest.value.processingStage)
  }
})
const progressStatusClass = computed(() => {
  if (latest.value.runStatus === 'SUCCESS') return 'success'
  if (latest.value.runStatus === 'PARTIAL_SUCCESS') return 'warning'
  if (latest.value.runStatus === 'FAILED') return 'failure'
  return ''
})

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

function viewError() {
  emit('view-error', latest.value.taskId)
}
</script>

<template>
  <article class="sync-task-group-card" :class="{ 'has-failure': hasFailure }">
    <header class="sync-task-group-header">
      <div class="sync-task-heading">
        <div class="sync-task-icon"><FolderSync :size="19" /></div>
        <div class="sync-task-heading-copy">
          <div class="sync-task-title-row">
            <h3 :title="group.folderName || group.folderId">{{ group.folderName || group.folderId || '未命名同步任务' }}</h3>
            <StatusBadge :status="latest.runStatus" />
          </div>
          <div class="sync-task-last-execution">最近执行 {{ formatDate(latest.createdAt) }}</div>
        </div>
      </div>
    </header>

    <button type="button" class="latest-run-summary" @click="emit('view', group.groupKey)">
      <div class="latest-run-label"><Timer :size="15" /><span>最近批次</span><code>{{ latest.taskId.slice(0, 8) }}</code><span>· {{ triggerLabel }}</span></div>
      <div class="latest-run-stage"><span>{{ progressLabel }}</span><strong>{{ latest.processedFiles }}/{{ latest.totalFiles }} 文件</strong></div>
      <div class="latest-run-progress" :class="progressStatusClass"><span :style="{ width: `${latest.totalFiles ? Math.min(100, Math.round((latest.processedFiles / latest.totalFiles) * 100)) : 0}%` }" /></div>
    </button>

    <section class="sync-task-group-stats" aria-label="同步任务统计">
      <div class="group-stat"><Clock3 :size="16" /><div><strong>{{ group.runs.length }}</strong><span>执行批次</span></div></div>
      <div class="group-stat group-stat-success"><CheckCircle2 :size="16" /><div><strong>{{ group.successRuns }}</strong><span>成功批次</span></div></div>
      <button v-if="hasFailure" type="button" class="group-stat group-stat-failure" @click="viewError"><XCircle :size="16" /><div><strong>{{ group.failedRuns }}</strong><span>失败批次</span></div><ChevronRight :size="14" /></button>
      <div v-else class="group-stat group-stat-failure"><XCircle :size="16" /><div><strong>{{ group.failedRuns }}</strong><span>失败批次</span></div></div>
      <div class="group-stat group-stat-active"><Timer :size="16" /><div><strong>{{ group.activeRuns }}</strong><span>活动批次</span></div></div>
    </section>

    <button v-if="hasFailure" type="button" class="group-failure-summary" @click="viewError">
      <AlertTriangle :size="16" />
      <span>{{ latest.errorSummary || latest.errorMessage || `最近批次有 ${latest.failedCount} 个文件处理失败` }}</span>
      <b>查看原因 <ChevronRight :size="14" /></b>
    </button>

    <footer class="sync-task-group-footer">
      <div class="sync-task-actions">
        <el-button v-if="latest.runStatus === 'RUNNING'" text size="small" :loading="isBusy" @click="emit('pause', latest.taskId)">暂停</el-button>
        <el-button v-if="['RUNNING', 'PAUSE_REQUESTED', 'STOP_REQUESTED'].includes(latest.runStatus)" text type="danger" size="small" :loading="isBusy" @click="emit('stop', latest.taskId)"><Square :size="13" /> 停止</el-button>
        <el-button v-if="canResume" type="primary" plain size="small" :loading="isBusy" @click="emit('resume', latest.taskId)"><Play :size="13" /> 继续</el-button>
        <el-button v-if="canRetry" type="warning" plain size="small" :loading="isBusy" @click="emit('retry', latest.taskId)"><RefreshCw :size="13" /> 重新同步</el-button>
        <el-button type="primary" size="small" @click="emit('view', group.groupKey)">查看执行记录</el-button>
      </div>
    </footer>
  </article>
</template>
