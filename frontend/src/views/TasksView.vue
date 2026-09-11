<script lang="ts" setup>
import { computed, onDeactivated, onMounted, ref } from 'vue'
import { Activity, AlertTriangle, ArrowLeft, CheckCircle2, ChevronRight, CircleAlert, Clock3, FileText, FolderSync, ListFilter, Search, Timer, XCircle } from 'lucide-vue-next'
import { ElButton, ElDrawer, ElEmpty, ElProgress } from 'element-plus'
import { useTasksStore } from '../stores/tasks'
import * as App from '../../wailsjs/go/main/App'
import SyncTaskGroupCard from '../components/SyncTaskGroupCard.vue'
import StatusBadge from '../components/StatusBadge.vue'
import type { TaskDTO, RunFileDTO, SyncTaskGroupDTO } from '../types'
import { errorMessage } from '../stores/store-helpers'
import { notifyError, notifySuccess, notifyWarning } from '../utils/notify'
import { stageLabel } from '../utils/status'

const store = useTasksStore()
const selected = ref<RunFileDTO[]>([])
const selectedTask = ref<TaskDTO | null>(null)
const selectedGroup = ref<SyncTaskGroupDTO | null>(null)
const groupVisible = ref(false)
const detailVisible = ref(false)
const detailLoading = ref(false)
const queueLoading = ref(false)
const queueError = ref('')
const detailFilter = ref<'ALL' | 'SUCCESS' | 'FAILED' | 'PENDING'>('ALL')
const errorVisible = ref(false)
interface FailureGroup {
  key: string
  title: string
  code?: string
  summary: string
  stage?: string
  count: number
  paths: string[]
  messages: string[]
}
interface SelectedError {
  title: string
  category?: string
  code?: string
  summary?: string
  path?: string
  message: string
  stage?: string
  failureCount?: number
  groups?: FailureGroup[]
  usedMinerU?: boolean
}
const selectedError = ref<SelectedError>({ title: '执行失败', message: '' })
const errorLoading = ref(false)
const controlError = ref('')
const controlBusy = ref('')
const filter = ref('ALL')
let detailRequestId = 0
let controlRequestId = 0
let errorRequestId = 0
// Store-side calls time out at 15s. Keep the view timeout longer so the store
// can finish its own cleanup before the view reports a timeout.
const BACKEND_CALL_TIMEOUT = 20000
const filterOptions = [
  { key: 'ALL', label: '全部' }, { key: 'QUEUED', label: '排队中' }, { key: 'RUNNING', label: '同步中' }, { key: 'PAUSED', label: '已暂停' }, { key: 'INTERRUPTED', label: '已中断' }, { key: 'PARTIAL_SUCCESS', label: '部分完成' }, { key: 'FAILED', label: '失败' }, { key: 'SUCCESS', label: '已完成' }, { key: 'STOPPED', label: '已停止' },
]
const visibleGroups = computed<SyncTaskGroupDTO[]>(() => {
  const grouped = new Map<string, TaskDTO[]>()
  for (const task of store.tasks) {
    const key = task.folderId || task.folderName || 'unknown'
    const runs = grouped.get(key) || []
    runs.push(task)
    grouped.set(key, runs)
  }

  return Array.from(grouped.entries()).map(([groupKey, runs]) => {
    const ordered = [...runs].sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime())
    const visibleRuns = filter.value === 'ALL' ? ordered : ordered.filter(run => run.runStatus === filter.value)
    if (!visibleRuns.length) return null
    const latest = visibleRuns[0]
    return {
      groupKey,
      folderId: latest.folderId,
      folderName: latest.folderName,
      runs: ordered,
      latest,
      successRuns: ordered.filter(run => run.runStatus === 'SUCCESS').length,
      failedRuns: ordered.filter(run => ['FAILED', 'PARTIAL_SUCCESS'].includes(run.runStatus) || run.failedCount > 0).length,
      activeRuns: ordered.filter(run => ['QUEUED', 'RUNNING', 'PAUSE_REQUESTED', 'PAUSED', 'STOP_REQUESTED', 'INTERRUPTED'].includes(run.runStatus)).length,
    }
  }).filter((group): group is SyncTaskGroupDTO => group !== null)
})
const detailFiles = computed(() => {
  if (detailFilter.value === 'ALL') return selected.value
  if (detailFilter.value === 'FAILED') return selected.value.filter(file => file.finalStatus === 'FAILED' || file.finalStatus === 'MINERU_FAILED')
  if (detailFilter.value === 'SUCCESS') return selected.value.filter(file => file.finalStatus === 'SUCCESS' || file.finalStatus === 'REMOTE_DELETED')
  return selected.value.filter(file => !['SUCCESS', 'FAILED', 'MINERU_FAILED', 'REMOTE_DELETED'].includes(file.finalStatus))
})
const detailSuccessCount = computed(() => selected.value.filter(file => file.finalStatus === 'SUCCESS' || file.finalStatus === 'REMOTE_DELETED').length)
const detailFailedCount = computed(() => selected.value.filter(file => file.finalStatus === 'FAILED' || file.finalStatus === 'MINERU_FAILED').length)
const detailPendingCount = computed(() => Math.max(0, selected.value.length - detailSuccessCount.value - detailFailedCount.value))
const detailProgress = computed(() => {
  if (!selectedTask.value?.totalFiles) return 0
  return Math.min(100, Math.round((selectedTask.value.processedFiles / selectedTask.value.totalFiles) * 100))
})

onMounted(() => { void refreshQueue(false) })

function closeTransientLayers() {
  ++detailRequestId
  ++errorRequestId
  errorLoading.value = false
  detailLoading.value = false
  groupVisible.value = false
  detailVisible.value = false
  errorVisible.value = false
}

// Element Plus drawers/dialogs are teleported to body. If this view is ever
// wrapped in keep-alive again, deactivation must close them or their mask can
// survive outside the cached component and intercept every click.
onDeactivated(closeTransientLayers)

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

function duration(task: TaskDTO) {
  if (!task.startedAt) return '—'
  const end = task.completedAt ? new Date(task.completedAt) : new Date()
  const start = new Date(task.startedAt)
  const seconds = Math.max(0, Math.floor((end.getTime() - start.getTime()) / 1000))
  if (seconds < 60) return `${seconds} 秒`
  return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
}

function messageOf(error: unknown) {
  return errorMessage(error, '未知错误')
}

/**
 * Start the bridge call inside the wrapper so a missing Wails runtime is also
 * handled as a rejected Promise. If a command times out but later settles, run
 * one reconciliation refresh; otherwise a late backend mutation could leave
 * the view showing the state captured before the command completed.
 */
function withTimeout<T>(
  operation: () => Promise<T> | T,
  label: string,
  timeout = BACKEND_CALL_TIMEOUT,
  onLateSettle?: () => void | Promise<void>,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  let timedOut = false
  let lateRefreshStarted = false
  const notifyLateSettle = () => {
    if (!timedOut || lateRefreshStarted || !onLateSettle) return
    lateRefreshStarted = true
    try {
      void Promise.resolve(onLateSettle()).catch(() => {
        // The original timeout is already reported; a best-effort late refresh
        // must never become an unhandled rejection.
      })
    } catch {
      // Ignore synchronous errors from the best-effort callback.
    }
  }

  const operationPromise = Promise.resolve().then(operation)
  const observedOperation = operationPromise.then(
    value => { notifyLateSettle(); return value },
    error => { notifyLateSettle(); throw error },
  )
  const timeoutPromise = new Promise<never>((_, reject) => {
    timer = setTimeout(() => {
      timedOut = true
      reject(new Error(`${label}超时，请确认后端已启动后重试。`))
    }, timeout)
  })

  return Promise.race([observedOperation, timeoutPromise]).finally(() => {
    if (timer !== undefined) clearTimeout(timer)
  })
}

async function loadDetail(taskId: string, notify = true) {
  const requestId = ++detailRequestId
  detailLoading.value = true
  try {
    const files = await withTimeout(() => store.getRunFiles(taskId), '读取任务详情')
    // 避免快速切换批次时，较早返回的请求覆盖当前详情。
    if (requestId === detailRequestId) selected.value = files
  } catch (e: unknown) {
    if (requestId === detailRequestId) {
      selected.value = []
      if (notify) notifyError(`读取任务详情失败：${messageOf(e)}`)
    }
    throw e
  } finally {
    if (requestId === detailRequestId) detailLoading.value = false
  }
}

async function view(taskId: string) {
  const task = store.tasks.find(item => item.taskId === taskId)
  if (!task) {
    notifyWarning('任务记录已更新，请先刷新执行队列。')
    return
  }
  selectedTask.value = task
  selected.value = []
  groupVisible.value = false
  detailVisible.value = true
  detailFilter.value = 'ALL'
  try {
    await loadDetail(taskId)
  } catch {
    // loadDetail 已负责提示；这里避免产生未处理的异步事件。
  } finally {
    // loadDetail 自身也有 finally；这里是 view 入口的兜底，防止事件处理链异常时卡住遮罩。
    if (detailLoading.value && selectedTask.value?.taskId === taskId) detailLoading.value = false
  }
}

function backToGroup() {
  // Invalidate a pending detail request when leaving the detail drawer.
  ++detailRequestId
  detailLoading.value = false
  detailVisible.value = false
  if (selectedGroup.value) groupVisible.value = true
}

const errorCategoryLabels: Record<string, string> = {
  MINERU_CONVERT: 'MinerU 转换报错',
  MINERU_DOWNLOAD: 'MinerU 文件下载报错',
  MINERU_RESULT: 'MinerU 结果处理报错',
  MAXKB_UPLOAD: '文件上传 MaxKB 报错',
  MAXKB_SPLIT: 'MaxKB 智能分段报错',
  MAXKB_CREATE: 'MaxKB 创建文档报错',
  MAXKB_DELETE: 'MaxKB 删除文档报错',
  LOCAL_SNAPSHOT: '本地文件读取报错',
  SOURCE_CHANGED: '同步期间文件发生变化',
  RECONCILE: '远端状态待人工确认',
  CONFIGURATION: '服务配置报错',
  UNSUPPORTED_FILE_TYPE: '文件格式不支持',
  TASK_QUEUE: '执行队列报错',
  TASK_CREATE: '同步批次创建报错',
  LOCAL_SYSTEM: '本地存储或执行服务报错',
  OTHER: '同步处理报错',
}

function errorCategoryLabel(category?: string, usedMinerU = false) {
  if (category === 'MAXKB_UPLOAD' && usedMinerU) return 'MinerU 产物上传 MaxKB 报错'
  if (category === 'MAXKB_SPLIT' && usedMinerU) return 'MinerU 产物上传及 MaxKB 智能分段报错'
  return (category && errorCategoryLabels[category]) || '同步处理报错'
}

function errorTitle(code?: string, category?: string, fallback = '任务执行失败', usedMinerU = false) {
  if (category) return errorCategoryLabel(category, usedMinerU)
  if (code?.startsWith('MINERU_')) return 'MinerU 处理报错'
  if (code?.startsWith('MAXKB_')) return 'MaxKB 处理报错'
  return fallback
}

const errorCodeLabels: Record<string, string> = {
  CONFIGURATION: '服务未正确配置',
  UNSUPPORTED_FILE_TYPE: '文件格式不支持',
  SNAPSHOT_FAILED: '本地文件快照创建失败',
  SNAPSHOT_READ_FAILED: '本地文件快照读取失败',
  SNAPSHOT_RECONCILE_REQUIRED: '本地文件快照状态不明确',
  MINERU_SUBMIT_UNKNOWN: 'MinerU 任务提交结果不明确',
  MINERU_CONVERT_AUTH_FAILED: 'MinerU 认证失败',
  MINERU_CONVERT_PERMISSION_DENIED: 'MinerU 权限不足',
  MINERU_CONVERT_UNSUPPORTED: 'MinerU 不支持该文件',
  MINERU_CONVERT_PROTOCOL_ERROR: 'MinerU 接口协议异常',
  MINERU_CONVERT_TIMEOUT: 'MinerU 转换超时',
  MINERU_CONVERT_FAILED: 'MinerU 转换失败',
  MINERU_STATUS_UNSUPPORTED: 'MinerU 任务状态不支持',
  MINERU_RESULT_DOWNLOAD_FAILED: 'MinerU 结果下载失败',
  MINERU_RESULT_READ_FAILED: 'MinerU 结果读取失败',
  MINERU_RESULT_SAVE_FAILED: 'MinerU 结果保存失败',
  MINERU_RESULT_INVALID: 'MinerU 结果无效',
  MAXKB_UPLOAD_FAILED: '文件上传 MaxKB 失败',
  MAXKB_UPLOAD_UNKNOWN: '文件上传 MaxKB 结果不明确',
  MAXKB_SPLIT_TIMEOUT: 'MaxKB 智能分段超时',
  MAXKB_SPLIT_UNKNOWN: 'MaxKB 智能分段结果不明确',
  MAXKB_SPLIT_AUTH_FAILED: 'MaxKB 智能分段认证失败',
  MAXKB_SPLIT_PERMISSION_DENIED: 'MaxKB 智能分段权限不足',
  MAXKB_SPLIT_INCOMPATIBLE: 'MaxKB 智能分段响应不兼容',
  MAXKB_SPLIT_FAILED: 'MaxKB 智能分段失败',
  MAXKB_CREATE_FAILED: 'MaxKB 文档创建失败',
  MAXKB_CREATE_UNKNOWN: 'MaxKB 文档创建结果不明确',
  MAXKB_DELETE_FAILED: 'MaxKB 文档删除失败',
  MAXKB_DELETE_UNKNOWN: 'MaxKB 文档删除结果不明确',
  SOURCE_CHANGED: '同步期间本地文件发生变化',
  RECONCILE_REQUIRED: '远端状态需要人工确认',
  CRASH_WINDOW_UNKNOWN: '应用中断期间远端状态不明确',
  RETRY_REQUIRES_RECONCILIATION: '重新同步前需要确认远端状态',
  TASK_QUEUE_FAILED: '执行队列异常',
  TASK_CREATE_FAILED: '同步批次创建失败',
  LOCAL_STORAGE_FAILED: '本地存储异常',
  EXECUTION_INTERNAL_FAILED: '本地执行服务异常',
}

function errorCodeLabel(code?: string) {
  if (!code) return ''
  return errorCodeLabels[code] ? `${errorCodeLabels[code]}（${code}）` : code
}

function errorSummary(code?: string, category?: string) {
  switch (code) {
    case 'MINERU_SUBMIT_UNKNOWN': return 'MinerU 任务提交结果不明确，需要确认服务端是否已接收。'
    case 'MINERU_CONVERT_AUTH_FAILED': return 'MinerU 认证失败，请检查访问 Token。'
    case 'MINERU_CONVERT_PERMISSION_DENIED': return 'MinerU 没有权限处理该文件或任务。'
    case 'MINERU_CONVERT_UNSUPPORTED': return '该文件格式不在 MinerU 支持范围内。'
    case 'MINERU_CONVERT_TIMEOUT': return 'MinerU 转换等待超时。'
    case 'MINERU_CONVERT_PROTOCOL_ERROR': return 'MinerU 接口响应格式或协议不符合预期。'
    case 'MINERU_CONVERT_FAILED': return 'MinerU 转换任务执行失败。'
    case 'MINERU_STATUS_UNSUPPORTED': return 'MinerU 返回了客户端无法识别的任务状态。'
    case 'MINERU_RESULT_DOWNLOAD_FAILED': return 'MinerU 已完成转换，但结果文件下载失败。'
    case 'MINERU_RESULT_READ_FAILED': return 'MinerU 结果文件无法读取。'
    case 'MINERU_RESULT_INVALID': return 'MinerU 返回的结果文件为空或格式无效。'
    case 'MINERU_RESULT_SAVE_FAILED': return 'MinerU 结果文件保存失败。'
    case 'MAXKB_UPLOAD_FAILED': return '文件上传到 MaxKB 失败。'
    case 'MAXKB_UPLOAD_UNKNOWN': return '文件上传结果不明确，请在异常处理中确认后再重试。'
    case 'MAXKB_SPLIT_TIMEOUT': return 'MaxKB 智能分段请求超时，远端结果需要确认。'
    case 'MAXKB_SPLIT_UNKNOWN': return 'MaxKB 智能分段结果不明确，请在异常处理中确认。'
    case 'MAXKB_SPLIT_AUTH_FAILED': return 'MaxKB 认证失败，请检查 API Key。'
    case 'MAXKB_SPLIT_PERMISSION_DENIED': return '当前 MaxKB API Key 没有执行智能分段的权限。'
    case 'MAXKB_SPLIT_INCOMPATIBLE': return 'MaxKB 智能分段接口返回格式与当前版本不兼容。'
    case 'MAXKB_SPLIT_FAILED': return 'MaxKB 智能分段失败。'
    case 'MAXKB_CREATE_FAILED': return 'MaxKB 文档创建失败。'
    case 'MAXKB_CREATE_UNKNOWN': return 'MaxKB 文档创建结果不明确，请在异常处理中确认。'
    case 'MAXKB_DELETE_FAILED': return 'MaxKB 原文档删除失败。'
    case 'MAXKB_DELETE_UNKNOWN': return 'MaxKB 原文档删除结果不明确，请在异常处理中确认。'
    case 'SNAPSHOT_FAILED': return '读取本地文件并生成同步快照失败。'
    case 'SNAPSHOT_READ_FAILED': return '本地同步快照无法读取。'
    case 'SNAPSHOT_RECONCILE_REQUIRED': return '本地快照缺失或损坏，且远端操作已经开始，需要人工确认。'
    case 'CONFIGURATION': return '同步服务尚未正确配置，请先检查系统设置。'
    case 'UNSUPPORTED_FILE_TYPE': return '该文件格式不能直接上传，且未配置对应的 MinerU 转换。'
    case 'TASK_QUEUE_FAILED': return '同步批次已保存，但本地执行服务未正常运行。'
    case 'LOCAL_STORAGE_FAILED': return '本地数据库或任务状态保存失败。'
    case 'EXECUTION_INTERNAL_FAILED': return '本地执行服务发生异常。'
    case 'SOURCE_CHANGED': return '同步过程中本地文件发生变化，本次未提交该文件。'
    case 'RECONCILE_REQUIRED': return '远端操作结果不明确，需要人工确认后处理。'
    case 'CRASH_WINDOW_UNKNOWN': return '应用中断时远端操作可能已经执行，需要人工确认。'
    case 'RETRY_REQUIRES_RECONCILIATION': return '重新同步前需要先确认上一次远端操作结果。'
    default: return category ? errorCategoryLabel(category) : '文件处理失败，请展开技术详情查看原因。'
  }
}

function buildFailureGroups(files: RunFileDTO[]): FailureGroup[] {
  const groups = new Map<string, FailureGroup>()
  for (const file of files) {
    const key = [file.errorCategory || 'OTHER', file.errorCode || '', file.processingStage || '', file.usedMinerU ? 'mineru' : 'direct'].join('|')
    let group = groups.get(key)
    if (!group) {
      group = {
        key,
        title: errorTitle(file.errorCode, file.errorCategory, '文件处理失败', file.usedMinerU),
        code: file.errorCode,
        summary: errorSummary(file.errorCode, file.errorCategory),
        stage: meaningfulStage(file.processingStage),
        count: 0,
        paths: [],
        messages: [],
      }
      groups.set(key, group)
    }
    group.count++
    if (file.relativePath && !group.paths.includes(file.relativePath)) group.paths.push(file.relativePath)
    if (file.errorMessage && !group.messages.includes(file.errorMessage)) group.messages.push(file.errorMessage)
  }
  return Array.from(groups.values())
}

function meaningfulStage(stage?: string) {
  return stage && stage !== 'INIT' ? stage : undefined
}

function isFailedRunFile(file: RunFileDTO) {
  return Boolean(file.errorMessage || file.errorCode) || ['FAILED', 'MINERU_FAILED', 'RECONCILE_REQUIRED', 'STOPPED'].includes(file.finalStatus)
}

async function showTaskError(task: TaskDTO) {
  const requestId = ++errorRequestId
  const taskMessage = task.errorMessage || task.errorSummary || ''
  selectedError.value = {
    title: errorTitle(undefined, undefined, '任务执行失败'),
    summary: taskMessage || '任务执行失败，请查看失败文件。',
    message: taskMessage,
    stage: meaningfulStage(task.processingStage),
    failureCount: task.failedCount || undefined,
  }
  errorVisible.value = true
  errorLoading.value = true

  try {
    const files = await withTimeout(() => store.getRunFiles(task.taskId), '读取失败文件详情')
    if (requestId !== errorRequestId || !errorVisible.value) return

    const failedFiles = files.filter(isFailedRunFile)
    const groups = buildFailureGroups(failedFiles)
    if (failedFiles.length > 1) {
      const categoryNames = groups.map(group => group.title.replace(/报错$/, ''))
      selectedError.value = {
        title: groups.length > 1 ? '任务包含多个处理错误' : groups[0]?.title || '任务执行失败',
        summary: `共 ${failedFiles.length} 个文件处理失败，涉及 ${groups.length} 类问题：${categoryNames.join('、')}。`,
        message: '',
        failureCount: failedFiles.length,
        groups,
      }
      return
    }
    const failedFile = failedFiles[0]
    if (failedFile) {
      selectedError.value = {
        title: errorTitle(failedFile.errorCode, failedFile.errorCategory, '任务执行失败', failedFile.usedMinerU),
        category: failedFile.errorCategory,
        code: failedFile.errorCode,
        summary: errorSummary(failedFile.errorCode, failedFile.errorCategory),
        path: failedFile.relativePath,
        message: failedFile.errorMessage || taskMessage || '文件未提供具体错误信息。',
        stage: meaningfulStage(failedFile.processingStage) || meaningfulStage(task.processingStage),
        failureCount: failedFiles.length || task.failedCount || undefined,
        usedMinerU: failedFile.usedMinerU,
      }
    } else {
      selectedError.value = {
        title: errorTitle(task.errorCode, task.errorCategory, '任务执行失败'),
        code: task.errorCode,
        category: task.errorCategory,
        summary: task.errorCode ? errorSummary(task.errorCode, task.errorCategory) : taskMessage || '任务未提供具体错误信息。',
        message: taskMessage || '任务未提供具体错误信息。',
        stage: meaningfulStage(task.processingStage),
        failureCount: task.failedCount || undefined,
      }
    }
  } catch {
    // 文件详情读取失败时仍保留任务级错误，不让弹窗退化成 INIT 或空白内容。
    if (requestId === errorRequestId && errorVisible.value) {
      selectedError.value = {
        title: errorTitle(task.errorCode, task.errorCategory, '任务执行失败'),
        code: task.errorCode,
        category: task.errorCategory,
        summary: task.errorCode ? errorSummary(task.errorCode, task.errorCategory) : taskMessage || '任务未提供具体错误信息。',
        message: taskMessage || '任务未提供具体错误信息。',
        stage: meaningfulStage(task.processingStage),
        failureCount: task.failedCount || undefined,
      }
    }
  } finally {
    if (requestId === errorRequestId) errorLoading.value = false
  }
}

function openGroup(groupKey: string) {
  const group = visibleGroups.value.find(item => item.groupKey === groupKey)
  if (!group) return
  selectedGroup.value = group
  groupVisible.value = true
}

function showTaskErrorById(taskId: string) {
  const task = store.tasks.find(item => item.taskId === taskId)
  if (task) void showTaskError(task)
}

function showFileError(file: RunFileDTO) {
  ++errorRequestId
  errorLoading.value = false
  selectedError.value = { title: errorTitle(file.errorCode, file.errorCategory, '文件处理失败', file.usedMinerU), category: file.errorCategory, code: file.errorCode, summary: errorSummary(file.errorCode, file.errorCategory), path: file.relativePath, message: file.errorMessage || '文件未提供具体错误信息。', stage: meaningfulStage(file.processingStage), usedMinerU: file.usedMinerU }
  errorVisible.value = true
}

async function refreshSelectedDetail() {
  if (!selectedTask.value || !detailVisible.value) return
  const taskId = selectedTask.value.taskId
  const latest = store.tasks.find(item => item.taskId === taskId)
  if (latest) selectedTask.value = latest
  await loadDetail(taskId, false)
}

async function refreshQueue(showSuccess = true) {
  if (queueLoading.value) return
  queueLoading.value = true
  queueError.value = ''
  try {
    await withTimeout(() => store.refreshStatus(), '刷新队列状态')
    if (store.error) throw new Error(store.error)
    await refreshSelectedDetail()
    controlError.value = ''
    if (store.hasBackgroundWork) store.startPolling()
    else store.stopPolling()
    if (showSuccess) notifySuccess('队列状态已刷新')
  } catch (e: unknown) {
    queueError.value = `刷新队列状态失败：${messageOf(e)}`
    notifyError(queueError.value)
  } finally {
    // refreshStatus 使用 silent 模式，不会驱动 store.loading；页面必须由本地状态收口。
    queueLoading.value = false
  }
}

async function refreshAfterControl() {
  await withTimeout(() => store.refreshStatus(), '刷新队列状态')
  if (store.error) throw new Error(store.error)
  queueError.value = ''
  await refreshSelectedDetail()
  if (store.hasBackgroundWork) store.startPolling()
  else store.stopPolling()
}

async function retryTask(taskId: string) {
  if (controlBusy.value) return
  const requestId = ++controlRequestId
  controlBusy.value = taskId
  controlError.value = ''
  try {
    await withTimeout(() => store.retryFailedTask(taskId), '创建重新同步批次', BACKEND_CALL_TIMEOUT, async () => {
      if (requestId === controlRequestId) await refreshQueue(false)
    })
    notifySuccess('已创建重新同步批次')
    await refreshQueue(false)
  } catch (e: unknown) {
    const message = messageOf(e)
    if (message.includes('RETRY_REQUIRES_RECONCILIATION')) {
      // 后端已将不确定的失败文件提升为 RECONCILE_REQUIRED；立即刷新队列，
      // 让“待处理”计数和任务卡片与异常处理中心的持久化状态保持一致。
      await refreshQueue(false)
      notifyWarning('该失败文件存在不确定的远端操作，已转入“异常处理”，请先确认远端状态。')
    } else if (message.includes('NO_RETRYABLE_FAILED_FILES')) {
      notifyWarning('当前批次没有可重新同步的失败文件。')
    } else {
      controlError.value = `重新同步失败：${message}`
      notifyError(controlError.value)
    }
  } finally {
    if (requestId === controlRequestId) controlBusy.value = ''
  }
}

async function control(action: 'pause' | 'resume' | 'stop', taskId: string) {
  if (controlBusy.value) return
  const requestId = ++controlRequestId
  const actionLabel = action === 'pause' ? '暂停' : action === 'resume' ? '继续' : '停止'
  controlBusy.value = taskId
  controlError.value = ''
  const reconcileIfCurrent = async () => {
    if (requestId !== controlRequestId) return
    await refreshAfterControl()
  }
  try {
    if (action === 'pause') await withTimeout(() => App.PauseTask(taskId), `${actionLabel}任务`, BACKEND_CALL_TIMEOUT, reconcileIfCurrent)
    else if (action === 'resume') await withTimeout(() => App.ResumeTask(taskId), `${actionLabel}任务`, BACKEND_CALL_TIMEOUT, reconcileIfCurrent)
    else await withTimeout(() => App.StopTask(taskId), `${actionLabel}任务`, BACKEND_CALL_TIMEOUT, reconcileIfCurrent)
    notifySuccess(`已发送${actionLabel}请求`)
  } catch (e: unknown) {
    controlError.value = `${actionLabel}任务失败：${messageOf(e)}`
    notifyError(controlError.value)
  } finally {
    // 先解除按钮锁定。状态刷新是收尾动作，后端重启时不能让刷新超时
    // 把控制按钮额外锁住一轮；旧控制请求的刷新结果也不能覆盖新操作的错误。
    if (requestId === controlRequestId) controlBusy.value = ''
    try {
      await refreshAfterControl()
    } catch (e: unknown) {
      if (requestId !== controlRequestId) return
      const refreshMessage = `状态刷新失败：${messageOf(e)}`
      const hadControlError = Boolean(controlError.value)
      controlError.value = controlError.value ? `${controlError.value}；${refreshMessage}` : refreshMessage
      if (!hadControlError) notifyError(refreshMessage)
    }
  }
}
</script>

<template>
  <div class="view-page tasks-view">
    <header class="page-header">
      <div><h1>执行队列</h1></div>
    </header>

    <div class="queue-summary">
      <div class="summary-card"><div class="summary-icon"><Clock3 :size="17" /></div><div><span>排队中</span><strong>{{ store.queueStats.queued }}</strong></div></div>
      <div class="summary-card"><div class="summary-icon"><Activity :size="17" /></div><div><span>当前运行</span><strong class="success-text">{{ store.queueStats.running }}</strong></div></div>
      <div class="summary-card"><div class="summary-icon"><CircleAlert :size="17" /></div><div><span>已暂停</span><strong class="warning-text">{{ store.queueStats.paused }}</strong></div></div>
      <div class="summary-card"><div class="summary-icon"><XCircle :size="17" /></div><div><span>待处理</span><strong class="danger-text">{{ store.queueStats.reconcileRequired }}</strong></div></div>
    </div>

    <div class="filter-bar">
      <div class="filter-tabs"><ListFilter :size="15" class="muted" /><button v-for="item in filterOptions" :key="item.key" class="filter-tab" :class="{ active: filter === item.key }" @click="filter = item.key">{{ item.label }}</button></div>
    </div>

    <div v-if="queueLoading || store.loading" class="loading"><el-progress indeterminate :show-text="false" :stroke-width="4" /><p>正在读取本地执行记录…</p></div>
    <div v-else-if="queueError || store.error" class="error-msg">{{ queueError || store.error }}</div>
    <div v-else-if="!visibleGroups.length" class="empty-state"><el-empty description="当前筛选下没有同步任务" /></div>
    <div v-else class="task-grid"><SyncTaskGroupCard v-for="group in visibleGroups" :key="group.groupKey" :group="group" :busy-task-id="controlBusy" @pause="id => control('pause', id)" @resume="id => control('resume', id)" @stop="id => control('stop', id)" @retry="retryTask" @view="openGroup" @view-error="showTaskErrorById" /></div>

    <el-drawer v-model="groupVisible" title="同步任务执行记录" size="min(760px, 92vw)" destroy-on-close>
      <template v-if="selectedGroup">
        <div class="group-drawer-header">
          <div class="run-icon running"><FolderSync :size="19" /></div>
          <div><h2>{{ selectedGroup.folderName || selectedGroup.folderId || '未命名同步任务' }}</h2><p>{{ selectedGroup.runs.length }} 个执行批次 · 同一个本地同步任务</p></div>
        </div>
        <div class="batch-list-head"><div><h3>执行批次</h3><p class="muted">每次手动或定时同步都会生成独立批次，点击批次查看文件级处理结果。</p></div><span class="muted">共 {{ selectedGroup.runs.length }} 次</span></div>
        <div class="batch-list">
          <article v-for="run in selectedGroup.runs" :key="run.taskId" class="batch-row">
            <div class="batch-row-main"><div class="batch-status-icon" :class="run.runStatus.toLowerCase()"><Timer :size="16" /></div><div class="batch-copy"><div class="batch-title"><strong>批次 {{ run.taskId.slice(0, 8) }}</strong><StatusBadge :status="run.runStatus" /></div><p>{{ run.triggerType === 'cron' ? '定时触发' : '手动触发' }} · {{ formatDate(run.createdAt) }} · {{ stageLabel(run.processingStage) }}</p></div></div>
            <div class="batch-metrics"><span class="success-text">{{ run.successCount }} 成功</span><span class="danger-text">{{ run.failedCount }} 失败</span><span>{{ run.skippedCount }} 跳过</span><span>{{ run.processedFiles }}/{{ run.totalFiles }} 文件</span></div>
            <div class="batch-row-actions"><el-button v-if="run.errorMessage || run.errorSummary || run.failedCount > 0" text type="danger" size="small" @click="showTaskError(run)">查看原因</el-button><el-button type="primary" plain size="small" :loading="detailLoading && selectedTask?.taskId === run.taskId" :disabled="detailLoading" @click="view(run.taskId)">查看详情 <ChevronRight :size="14" /></el-button></div>
          </article>
        </div>
      </template>
    </el-drawer>

    <el-drawer v-model="detailVisible" size="min(720px, 92vw)" destroy-on-close @close="backToGroup">
      <template #header>
        <div class="detail-drawer-header">
          <el-button text size="small" class="detail-back-button" @click="backToGroup"><ArrowLeft :size="16" /> 返回执行记录</el-button>
          <strong>任务执行详情</strong>
        </div>
      </template>
      <template v-if="selectedTask">
        <div class="detail-hero">
          <div class="detail-hero-title"><div class="run-icon" :class="selectedTask.runStatus.toLowerCase()"><FileText :size="18" /></div><div><h2>{{ selectedTask.folderName || selectedTask.folderId }}</h2><p>{{ selectedTask.triggerType === 'cron' ? '定时触发' : '手动触发' }} · {{ formatDate(selectedTask.createdAt) }}</p></div><StatusBadge :status="selectedTask.runStatus" /></div>
          <div class="detail-meta"><span><b>任务 ID</b><code>{{ selectedTask.taskId }}</code></span><span><b>处理阶段</b>{{ stageLabel(selectedTask.processingStage) }}</span><span><b>耗时</b>{{ duration(selectedTask) }}</span></div>
          <div class="detail-progress"><div><span>整体进度</span><strong>{{ detailProgress }}%</strong></div><el-progress :percentage="detailProgress" :show-text="false" :stroke-width="8" color="#5A55FA" /></div>
        </div>

        <div class="detail-stats"><button class="detail-stat success" @click="detailFilter = 'SUCCESS'"><CheckCircle2 :size="17" /><span><b>{{ selectedTask.successCount || detailSuccessCount }}</b>成功</span></button><button class="detail-stat danger" @click="detailFilter = 'FAILED'"><XCircle :size="17" /><span><b>{{ selectedTask.failedCount || detailFailedCount }}</b>失败</span></button><button class="detail-stat muted-stat" @click="detailFilter = 'PENDING'"><Clock3 :size="17" /><span><b>{{ selectedTask.skippedCount || detailPendingCount }}</b>跳过/处理中</span></button><div class="detail-stat total"><FileText :size="17" /><span><b>{{ selectedTask.totalFiles }}</b>文件总数</span></div></div>

        <div v-if="selectedTask.errorMessage || selectedTask.errorSummary" class="detail-error-summary"><div><AlertTriangle :size="17" /><span>{{ selectedTask.errorMessage || selectedTask.errorSummary }}</span></div><el-button text type="danger" @click="showTaskError(selectedTask)">查看原因</el-button></div>
        <div class="detail-section-head"><div><h3>文件处理明细</h3><p class="muted">成功和失败文件均保留在本次任务中，失败项可点击查看具体原因。</p></div><span class="muted">{{ detailFiles.length }} / {{ selected.length }} 条</span></div>
        <div class="detail-filters"><button v-for="item in [{ key: 'ALL', label: '全部' }, { key: 'SUCCESS', label: `成功 ${detailSuccessCount}` }, { key: 'FAILED', label: `失败 ${detailFailedCount}` }, { key: 'PENDING', label: `处理中 ${detailPendingCount}` }]" :key="item.key" class="detail-filter" :class="{ active: detailFilter === item.key }" @click="detailFilter = item.key as 'ALL' | 'SUCCESS' | 'FAILED' | 'PENDING'">{{ item.label }}</button></div>
        <div v-if="detailLoading" class="loading detail-loading"><el-progress indeterminate :show-text="false" :stroke-width="4" /><p>正在加载文件明细…</p></div>
        <div v-else-if="!detailFiles.length" class="empty-state detail-empty"><el-empty description="当前筛选下没有文件" /></div>
        <div v-else class="file-detail-list">
          <div v-for="file in detailFiles" :key="file.runFileId" class="file-detail-row" :class="{ failed: file.finalStatus === 'FAILED' || file.finalStatus === 'MINERU_FAILED' }">
            <div class="file-detail-main"><FileText :size="16" class="muted" /><div><strong>{{ file.relativePath || '未知文件' }}</strong><p>{{ stageLabel(file.processingStage, '—') }}<span v-if="file.completedAt"> · 完成于 {{ formatDate(file.completedAt) }}</span></p></div></div>
            <StatusBadge :status="file.finalStatus" type="file" />
            <el-button v-if="file.errorMessage || file.finalStatus === 'FAILED' || file.finalStatus === 'MINERU_FAILED'" text type="danger" size="small" @click="showFileError(file)"><Search :size="14" /> 查看报错</el-button>
          </div>
        </div>
      </template>
    </el-drawer>

    <el-dialog v-model="errorVisible" title="错误详情" :width="selectedError.groups?.length ? '680px' : '520px'" destroy-on-close>
      <div class="error-detail-dialog">
        <div class="error-detail-title"><AlertTriangle :size="19" /><strong>{{ selectedError.title }}</strong></div>
        <dl>
          <template v-if="selectedError.path"><dt>文件</dt><dd class="mono">{{ selectedError.path }}</dd></template>
          <template v-if="selectedError.failureCount && selectedError.failureCount > 1"><dt>失败文件</dt><dd>共 {{ selectedError.failureCount }} 个文件失败，已按处理步骤汇总如下</dd></template>
          <template v-if="selectedError.stage"><dt>处理阶段</dt><dd><StatusBadge :status="selectedError.stage" type="file" /></dd></template>
          <dt>处理结果</dt><dd class="error-detail-message">{{ selectedError.summary || selectedError.message || '文件处理失败。' }}<span v-if="errorLoading" class="error-detail-loading">正在读取文件详情…</span></dd>
          <template v-if="selectedError.code"><dt>错误类型</dt><dd>{{ errorCodeLabel(selectedError.code) }}</dd></template>
          <template v-if="selectedError.message && selectedError.message !== selectedError.summary"><dt>技术详情</dt><dd class="error-detail-message">{{ selectedError.message }}</dd></template>
        </dl>
        <div v-if="selectedError.groups?.length" class="failure-group-list">
          <article v-for="group in selectedError.groups" :key="group.key" class="failure-group">
            <header><strong>{{ group.title }}</strong><span>{{ group.count }} 个文件</span></header>
            <div class="failure-group-facts">
              <span>处理阶段</span><b>{{ stageLabel(group.stage, '未知阶段') }}</b>
              <span>错误类型</span><b>{{ errorCodeLabel(group.code) || '未分类错误' }}</b>
              <span>处理结果</span><p>{{ group.summary }}</p>
              <span>涉及文件</span><p class="mono">{{ group.paths.slice(0, 5).join('\n') }}<template v-if="group.paths.length > 5"><br>另有 {{ group.paths.length - 5 }} 个文件</template></p>
              <template v-if="group.messages.length"><span>技术详情</span><p class="failure-group-technical">{{ group.messages.join('\n') }}</p></template>
            </div>
          </article>
        </div>
      </div>
      <template #footer><el-button type="primary" @click="errorVisible = false">知道了</el-button></template>
    </el-dialog>
  </div>
</template>
