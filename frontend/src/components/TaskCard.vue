<script lang="ts" setup>
import { computed } from 'vue'
import { AlertTriangle, CheckCircle2, ChevronRight, CircleStop, Clock3, FileText, Pause, Play, RotateCcw, Square, Timer } from 'lucide-vue-next'
import { ElButton, ElProgress, ElTooltip } from 'element-plus'
import type { TaskDTO } from '../types'
import StatusBadge from './StatusBadge.vue'
import { stageLabel } from '../utils/status'

const props = defineProps<{ task: TaskDTO; busy?: boolean }>()
const emit = defineEmits<{
  (e: 'pause', taskId: string): void
  (e: 'resume', taskId: string): void
  (e: 'stop', taskId: string): void
  (e: 'view', taskId: string): void
  (e: 'view-error', taskId: string): void
}>()

const progress = computed(() => {
  if (!props.task.totalFiles) return 0
  return Math.min(100, Math.round((props.task.processedFiles / props.task.totalFiles) * 100))
})

const progressLabel = computed(() => `${props.task.processedFiles}/${props.task.totalFiles}`)
const progressColor = computed(() => {
  if (props.task.runStatus === 'SUCCESS') return 'var(--success)'
  if (props.task.runStatus === 'PARTIAL_SUCCESS') return 'var(--warning)'
  if (props.task.runStatus === 'FAILED') return 'var(--danger)'
  return 'var(--primary)'
})
const hasFailure = computed(() => props.task.failedCount > 0 || Boolean(props.task.errorMessage || props.task.errorSummary))
const failureText = computed(() => props.task.errorSummary || props.task.errorMessage || `${props.task.failedCount} 个文件处理失败`)
const canResume = computed(() => ['PAUSED', 'INTERRUPTED'].includes(props.task.runStatus))
const triggerLabel = computed(() => props.task.triggerType === 'cron' ? '定时触发' : '手动触发')

function formatDate(s?: string) {
  if (!s) return '—'
  const date = new Date(s)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

function viewError() {
  emit('view-error', props.task.taskId)
}
</script>

<template>
  <article class="task-card" :class="[`status-${task.runStatus.toLowerCase()}`, { 'has-failure': hasFailure }]">
    <header class="task-card-header">
      <div class="task-heading">
        <div class="task-icon" :class="task.runStatus.toLowerCase()" aria-hidden="true">
          <Timer :size="18" />
        </div>
        <div class="task-heading-copy">
          <div class="task-title-row">
            <h3 :title="task.folderName || task.folderId">{{ task.folderName || task.folderId }}</h3>
            <StatusBadge :status="task.runStatus" />
          </div>
          <div class="task-meta">
            <span>{{ triggerLabel }}</span>
            <span class="meta-divider">·</span>
            <span>批次 {{ task.taskId.slice(0, 8) }}</span>
            <span class="meta-divider">·</span>
            <span>{{ formatDate(task.createdAt) }}</span>
          </div>
        </div>
      </div>
      <el-button class="detail-button" text size="small" @click="emit('view', task.taskId)">
        详情
        <ChevronRight :size="14" />
      </el-button>
    </header>

    <section class="task-progress" aria-label="同步进度">
      <div class="progress-heading">
        <div>
          <span class="section-label">当前阶段</span>
          <strong>{{ stageLabel(task.processingStage) }}</strong>
        </div>
        <div class="progress-value">
          <strong>{{ progress }}%</strong>
          <span>{{ progressLabel }} 文件</span>
        </div>
      </div>
      <el-progress :percentage="progress" :show-text="false" :stroke-width="8" :color="progressColor" />
    </section>

    <section class="task-stats" aria-label="同步统计">
      <div class="stat-item stat-success">
        <CheckCircle2 :size="16" />
        <div><strong>{{ task.successCount }}</strong><span>成功</span></div>
      </div>
      <button v-if="hasFailure" type="button" class="stat-item stat-failure stat-button" @click="viewError">
        <CircleStop :size="16" />
        <div><strong>{{ task.failedCount }}</strong><span>失败</span></div>
        <ChevronRight :size="14" class="stat-arrow" />
      </button>
      <div v-else class="stat-item stat-failure">
        <CircleStop :size="16" />
        <div><strong>{{ task.failedCount }}</strong><span>失败</span></div>
      </div>
      <div class="stat-item stat-skipped">
        <FileText :size="16" />
        <div><strong>{{ task.skippedCount }}</strong><span>跳过</span></div>
      </div>
      <div class="stat-item stat-total">
        <Clock3 :size="16" />
        <div><strong>{{ task.totalFiles }}</strong><span>总文件</span></div>
      </div>
    </section>

    <button v-if="hasFailure" type="button" class="failure-panel" @click="viewError">
      <AlertTriangle :size="16" />
      <span class="failure-message" :title="failureText">{{ failureText }}</span>
      <span class="failure-link">查看失败详情 <ChevronRight :size="14" /></span>
    </button>

    <footer class="task-card-footer">
      <div class="task-time">
        <span>开始 {{ formatDate(task.startedAt) }}</span>
        <span v-if="task.completedAt" class="meta-divider">· 完成 {{ formatDate(task.completedAt) }}</span>
      </div>
      <div class="task-actions">
        <template v-if="task.runStatus === 'RUNNING'">
          <ElTooltip content="在当前文件安全检查点暂停">
            <el-button text size="small" :disabled="busy" @click="emit('pause', task.taskId)">
              <Pause :size="14" /> 暂停
            </el-button>
          </ElTooltip>
          <el-button text type="danger" size="small" :disabled="busy" @click="emit('stop', task.taskId)">
            <Square :size="14" /> 停止
          </el-button>
        </template>
        <template v-else-if="canResume">
          <el-button type="primary" plain size="small" :disabled="busy" @click="emit('resume', task.taskId)">
            <Play :size="14" /> 继续
          </el-button>
          <el-button text type="danger" size="small" :disabled="busy" @click="emit('stop', task.taskId)">
            <Square :size="14" /> 停止
          </el-button>
        </template>
        <span v-else-if="task.runStatus === 'PAUSE_REQUESTED'" class="action-state">
          <RotateCcw :size="14" /> 正在安全暂停
        </span>
        <span v-else-if="task.runStatus === 'STOP_REQUESTED'" class="action-state">
          <RotateCcw :size="14" /> 正在安全停止
        </span>
        <el-button text size="small" @click="emit('view', task.taskId)">
          <FileText :size="14" /> 文件明细
        </el-button>
      </div>
    </footer>
  </article>
</template>

<style scoped>
.task-card {
  display: flex;
  flex-direction: column;
  gap: 18px;
  min-width: 0;
  padding: 20px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface, #fff);
  box-shadow: 0 4px 14px rgb(15 23 42 / 4%);
  transition: border-color .2s ease, box-shadow .2s ease;
}
.task-card:hover { border-color: rgb(90 85 250 / 35%); box-shadow: 0 8px 22px rgb(15 23 42 / 7%); }
.task-card.has-failure { border-color: rgb(239 68 68 / 28%); }
.task-card-header, .task-title-row, .task-heading, .task-meta, .progress-heading, .task-card-footer, .task-actions, .failure-panel { display: flex; align-items: center; }
.task-card-header { justify-content: space-between; gap: 12px; }
.task-heading { min-width: 0; gap: 12px; }
.task-icon { display: grid; flex: 0 0 36px; width: 36px; height: 36px; place-items: center; border-radius: 9px; color: #5A55FA; background: #eeedff; }
.task-icon.running, .task-icon.pause_requested, .task-icon.stop_requested { color: #5A55FA; background: #eeedff; }
.task-icon.success { color: #16a34a; background: #eaf8ef; }
.task-icon.failed { color: #dc2626; background: #fef0f0; }
.task-icon.partial_success, .task-icon.paused, .task-icon.interrupted { color: #d97706; background: #fff7e8; }
.task-heading-copy { min-width: 0; }
.task-title-row { min-width: 0; gap: 9px; }
.task-title-row h3 { overflow: hidden; margin: 0; color: var(--text, #030712); font-size: 15px; font-weight: 650; line-height: 1.35; text-overflow: ellipsis; white-space: nowrap; }
.task-meta { min-width: 0; margin-top: 5px; color: var(--muted, #9CA3AF); font-size: 11px; line-height: 1.4; }
.task-meta span:not(.meta-divider) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.meta-divider { flex: 0 0 auto; margin: 0 5px; color: #d1d5db; }
.detail-button { flex: 0 0 auto; color: var(--text-secondary, #4B5563); }
.detail-button :deep(span) { display: inline-flex; align-items: center; gap: 3px; }
.task-progress { padding: 14px; border-radius: 9px; background: var(--surface-muted, #f7f7fa); }
.progress-heading { justify-content: space-between; gap: 16px; margin-bottom: 11px; }
.progress-heading > div:first-child { display: flex; min-width: 0; flex-direction: column; gap: 3px; }
.section-label { color: var(--muted, #9CA3AF); font-size: 11px; }
.progress-heading strong { overflow: hidden; color: var(--text, #030712); font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.progress-value { display: flex; flex: 0 0 auto; align-items: baseline; gap: 7px; }
.progress-value strong { color: #5A55FA; font-size: 17px; }
.progress-value span { color: var(--muted, #9CA3AF); font-size: 11px; }
.task-stats { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.stat-item { display: flex; min-width: 0; align-items: center; gap: 8px; padding: 10px 9px; border: 1px solid transparent; border-radius: 8px; color: var(--text-secondary, #4B5563); text-align: left; }
.stat-item > div { display: flex; min-width: 0; flex-direction: column; gap: 2px; }
.stat-item strong { color: var(--text, #030712); font-size: 15px; line-height: 1; }
.stat-item span { color: var(--muted, #9CA3AF); font-size: 11px; }
.stat-success { background: #f0faf3; color: #16a34a; }
.stat-failure { background: #fff5f5; color: #dc2626; }
.stat-skipped { background: #f5f6f8; color: #6b7280; }
.stat-total { background: #f3f2ff; color: #5A55FA; }
.stat-button { width: 100%; cursor: pointer; font: inherit; transition: border-color .15s ease, background .15s ease; }
.stat-button:hover { border-color: #fca5a5; background: #feecec; }
.stat-arrow { margin-left: auto; }
.failure-panel { width: 100%; gap: 8px; padding: 10px 12px; border: 1px solid #fecaca; border-radius: 8px; color: #b91c1c; background: #fff8f8; cursor: pointer; font: inherit; text-align: left; }
.failure-panel:hover { border-color: #f87171; background: #fff1f1; }
.failure-message { min-width: 0; overflow: hidden; flex: 1; font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.failure-link { display: inline-flex; flex: 0 0 auto; align-items: center; gap: 2px; font-size: 11px; font-weight: 600; white-space: nowrap; }
.task-card-footer { justify-content: space-between; gap: 12px; padding-top: 2px; }
.task-time { display: flex; min-width: 0; color: var(--muted, #9CA3AF); font-size: 11px; }
.task-actions { flex: 0 0 auto; justify-content: flex-end; gap: 3px; }
.task-actions :deep(.el-button) { margin-left: 0; }
.action-state { display: inline-flex; align-items: center; gap: 5px; color: var(--muted, #9CA3AF); font-size: 11px; }

@media (max-width: 620px) {
  .task-card { padding: 16px; }
  .task-stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .task-card-footer { align-items: flex-start; flex-direction: column; }
  .task-actions { width: 100%; justify-content: flex-start; flex-wrap: wrap; }
}
</style>
