import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { TaskDTO, RunFileDTO, QueueStatsDTO } from '../types'
import * as App from '../../wailsjs/go/main/App'
import { errorMessage, isNoPendingChangesError, requireArray, withTimeout } from './store-helpers'

const TASK_REQUEST_TIMEOUT_MS = 15_000

export const useTasksStore = defineStore('tasks', () => {
  const tasks = ref<TaskDTO[]>([])
  const runningTasks = ref<TaskDTO[]>([])
  const queueStats = ref<QueueStatsDTO>({ queued: 0, running: 0, paused: 0, reconcileRequired: 0 })
  const loading = ref(false)
  const error = ref<string | null>(null)
  let pollTimer: ReturnType<typeof setInterval> | null = null
  let pollInFlight = false
  let loadingCount = 0
  let latestRunningFetchID = 0
  let latestQueueFetchID = 0
  let latestTasksFetchID = 0
  let latestRefreshID = 0

  const activeStatuses = new Set(['QUEUED', 'RUNNING', 'PAUSE_REQUESTED', 'PAUSED', 'STOP_REQUESTED', 'INTERRUPTED'])
  const hasRunningTasks = computed(() => runningTasks.value.some(task => activeStatuses.has(task.runStatus)))
  // RECONCILE_REQUIRED is not a running batch, but the backend reconciler may
  // resolve it asynchronously after MaxKB finishes processing. Keep the UI
  // polling until those durable outcomes are reflected locally.
  const hasBackgroundWork = computed(() => hasRunningTasks.value || queueStats.value.reconcileRequired > 0)

  function beginLoading() {
    loadingCount += 1
    loading.value = true
  }

  function endLoading() {
    loadingCount = Math.max(0, loadingCount - 1)
    loading.value = loadingCount > 0
  }

  function normalizeTask(task: unknown): TaskDTO {
    if (!task || typeof task !== 'object') throw new Error('任务接口返回格式无效。')
    const item = task as Partial<TaskDTO>
    const successCount = item.successCount ?? 0
    const failedCount = item.failedCount ?? 0
    const skippedCount = item.skippedCount ?? 0
    return {
      ...item,
      successCount,
      failedCount,
      skippedCount,
      processedFiles: item.processedFiles ?? successCount + failedCount + skippedCount,
      reconcileCount: item.reconcileCount ?? 0,
      recoveryCount: item.recoveryCount ?? 0,
    } as TaskDTO
  }

  function normalizeQueueStats(value: unknown): QueueStatsDTO {
    if (!value || typeof value !== 'object') throw new Error('执行队列统计返回格式无效。')
    const item = value as Partial<QueueStatsDTO>
    return {
      queued: Number.isFinite(Number(item.queued)) ? Number(item.queued) : 0,
      running: Number.isFinite(Number(item.running)) ? Number(item.running) : 0,
      paused: Number.isFinite(Number(item.paused)) ? Number(item.paused) : 0,
      reconcileRequired: Number.isFinite(Number(item.reconcileRequired)) ? Number(item.reconcileRequired) : 0,
    }
  }

  async function loadRunningTasks(refreshID?: number): Promise<string | null> {
    const requestID = ++latestRunningFetchID
    try {
      const result = requireArray<TaskDTO>(
        await withTimeout(() => App.ListRunningTasks(), '读取运行中任务', TASK_REQUEST_TIMEOUT_MS),
        '运行中任务',
      )
      if (requestID === latestRunningFetchID) {
        runningTasks.value = result.map(normalizeTask)
      }
      return null
    } catch (e: unknown) {
      const message = errorMessage(e, '读取运行中任务失败')
      if (requestID === latestRunningFetchID && (refreshID === undefined || refreshID === latestRefreshID)) error.value = message
      return message
    }
  }

  async function fetchRunningTasks() {
    error.value = null
    return loadRunningTasks()
  }

  async function loadQueueStats(refreshID?: number): Promise<string | null> {
    const requestID = ++latestQueueFetchID
    try {
      const result = normalizeQueueStats(await withTimeout(() => App.GetQueueStats(), '读取执行队列统计', TASK_REQUEST_TIMEOUT_MS))
      if (requestID === latestQueueFetchID) queueStats.value = result
      return null
    } catch (e: unknown) {
      const message = errorMessage(e, '读取执行队列统计失败')
      if (requestID === latestQueueFetchID && (refreshID === undefined || refreshID === latestRefreshID)) error.value = message
      return message
    }
  }

  async function fetchQueueStats() {
    error.value = null
    return loadQueueStats()
  }

  async function loadAllTasks(limit: number, refreshID?: number): Promise<string | null> {
    const requestID = ++latestTasksFetchID
    try {
      const result = requireArray<TaskDTO>(
        await withTimeout(() => App.ListTasks('', limit), '读取执行记录', TASK_REQUEST_TIMEOUT_MS),
        '执行记录',
      )
      if (requestID === latestTasksFetchID) tasks.value = result.map(normalizeTask)
      return null
    } catch (e: unknown) {
      const message = errorMessage(e, '读取执行记录失败')
      if (requestID === latestTasksFetchID && (refreshID === undefined || refreshID === latestRefreshID)) error.value = message
      return message
    }
  }

  async function fetchTasks(folderId: string, limit = 20) {
    const requestID = ++latestTasksFetchID
    beginLoading()
    error.value = null
    try {
      const result = requireArray<TaskDTO>(
        await withTimeout(() => App.ListTasks(folderId, limit), '读取执行记录', TASK_REQUEST_TIMEOUT_MS),
        '执行记录',
      )
      if (requestID === latestTasksFetchID) tasks.value = result.map(normalizeTask)
    } catch (e: unknown) {
      if (requestID === latestTasksFetchID) error.value = errorMessage(e, '读取执行记录失败')
    } finally {
      endLoading()
    }
  }

  async function fetchAllTasks(limit = 100, silent = false) {
    error.value = null
    if (!silent) beginLoading()
    try {
      await loadAllTasks(limit)
    } finally {
      if (!silent) endLoading()
    }
  }

  /** Refresh all queue sources without rejecting for a partial read failure. */
  async function refreshStatus(): Promise<boolean> {
    const refreshID = ++latestRefreshID
    beginLoading()
    error.value = null
    try {
      const errors = (await Promise.all([
        loadRunningTasks(refreshID),
        loadQueueStats(refreshID),
        loadAllTasks(100, refreshID),
      ])).filter((message): message is string => Boolean(message))
      if (refreshID === latestRefreshID && errors.length) {
        error.value = [...new Set(errors)].join('；')
      }
      return errors.length === 0
    } finally {
      endLoading()
    }
  }

  async function createTask(folderId: string, triggerType = 'manual'): Promise<TaskDTO> {
    error.value = null
    try {
      const task = normalizeTask(await withTimeout(() => App.CreateTask(folderId, triggerType), '创建同步批次', TASK_REQUEST_TIMEOUT_MS))
      // Invalidate an older ListTasks/ListRunningTasks response. This prevents
      // the classic “queue count is 1, execution list is 0” race after creation.
      ++latestTasksFetchID
      ++latestRunningFetchID
      tasks.value = [task, ...tasks.value.filter(item => item.taskId !== task.taskId)]
      if (activeStatuses.has(task.runStatus)) {
        runningTasks.value = [task, ...runningTasks.value.filter(item => item.taskId !== task.taskId)]
      } else {
        runningTasks.value = runningTasks.value.filter(item => item.taskId !== task.taskId)
      }
      // The batch was created successfully; a follow-up stats read must not
      // turn the successful button operation into a rejected promise.
      await loadQueueStats()
      startPolling()
      return task
    } catch (e: unknown) {
      // 无变更是正常的幂等结果，不应污染队列页的全局错误状态。
      if (isNoPendingChangesError(e)) {
        error.value = null
      } else {
        error.value = errorMessage(e, '创建同步批次失败')
      }
      throw new Error(errorMessage(e, '创建同步批次失败'))
    }
  }

  async function retryFailedTask(taskId: string): Promise<TaskDTO> {
    error.value = null
    try {
      const task = normalizeTask(await withTimeout(() => App.RetryFailedTask(taskId), '创建重新同步批次', TASK_REQUEST_TIMEOUT_MS))
      // 防止旧的列表请求覆盖刚创建的重试批次。
      ++latestTasksFetchID
      ++latestRunningFetchID
      tasks.value = [task, ...tasks.value.filter(item => item.taskId !== task.taskId)]
      if (activeStatuses.has(task.runStatus)) {
        runningTasks.value = [task, ...runningTasks.value.filter(item => item.taskId !== task.taskId)]
      }
      await loadQueueStats()
      startPolling()
      return task
    } catch (e: unknown) {
      // Retry is a user action. Its business error is rendered by the task
      // card/toast and must not remain in the queue page's data-read error,
      // otherwise navigating to the queue shows a stale red error panel even
      // when queue statistics and history loaded successfully.
      const message = errorMessage(e, '创建重新同步批次失败')
      error.value = null
      throw new Error(message)
    }
  }

  async function controlTask(taskId: string, action: 'pause' | 'resume' | 'stop') {
    const labels = { pause: '暂停同步批次', resume: '继续同步批次', stop: '停止同步批次' }
    error.value = null
    try {
      if (action === 'pause') await withTimeout(() => App.PauseTask(taskId), labels[action], TASK_REQUEST_TIMEOUT_MS)
      if (action === 'resume') await withTimeout(() => App.ResumeTask(taskId), labels[action], TASK_REQUEST_TIMEOUT_MS)
      if (action === 'stop') await withTimeout(() => App.StopTask(taskId), labels[action], TASK_REQUEST_TIMEOUT_MS)
      await refreshStatus()
      startPolling()
    } catch (e: unknown) {
      error.value = errorMessage(e, `${labels[action]}失败`)
      throw new Error(error.value)
    }
  }

  async function pauseTask(taskId: string) { return controlTask(taskId, 'pause') }
  async function resumeTask(taskId: string) { return controlTask(taskId, 'resume') }
  async function stopTask(taskId: string) { return controlTask(taskId, 'stop') }

  async function getRunFiles(taskId: string): Promise<RunFileDTO[]> {
    try {
      return requireArray<RunFileDTO>(
        await withTimeout(() => App.GetRunFiles(taskId), '读取任务详情', TASK_REQUEST_TIMEOUT_MS),
        '任务详情',
      )
    } catch (e: unknown) {
      throw new Error(errorMessage(e, '读取任务详情失败'))
    }
  }

  async function pollOnce() {
    if (pollInFlight) return
    pollInFlight = true
    try {
      const ok = await refreshStatus()
      // Keep polling through a transient Wails/backend restart. A failed read
      // must not permanently disable recovery once the backend is back.
      if (ok && !hasBackgroundWork.value) stopPolling()
    } finally {
      pollInFlight = false
    }
  }

  function startPolling() {
    if (pollTimer) return
    pollTimer = setInterval(() => { void pollOnce() }, 3000)
  }

  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }

  return {
    tasks, runningTasks, queueStats, loading, error, hasRunningTasks, hasBackgroundWork,
    fetchRunningTasks, fetchQueueStats, fetchTasks, fetchAllTasks, refreshStatus,
    createTask, retryFailedTask, pauseTask, resumeTask, stopTask, getRunFiles,
    startPolling, stopPolling,
  }
})
