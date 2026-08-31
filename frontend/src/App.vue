<script lang="ts" setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { RefreshCw, Settings2, FolderSync, ListChecks, AlertTriangle, Activity, ChevronRight } from 'lucide-vue-next'
import { ElTooltip } from 'element-plus'
import { useFoldersStore } from './stores/folders'
import { useTasksStore } from './stores/tasks'
import { useConfigStore } from './stores/config'
import * as WailsApp from '../wailsjs/go/main/App'

const route = useRoute()
const foldersStore = useFoldersStore()
const tasksStore = useTasksStore()
const configStore = useConfigStore()
const isBusy = computed(() => tasksStore.hasRunningTasks)
const queueTotal = computed(() => tasksStore.queueStats.queued + tasksStore.queueStats.running + tasksStore.queueStats.paused)
const initializationLoading = ref(false)
const refreshLoading = ref(false)
const wailsReconnectOverlayVisible = ref(false)
const initializationFailures = ref<Array<{ label: string; message: string }>>([])
const frontendRuntimeError = ref('')
let initializationRequest = 0
let automaticRetryCount = 0
let retryTimer: ReturnType<typeof setTimeout> | null = null
let runtimeOverlayObserver: MutationObserver | null = null
let runtimeOverlayTimer: ReturnType<typeof setInterval> | null = null
let reconnectOverlayWasVisible = false
let bridgeWasHealthy = false
let isUnmounted = false

const currentPage = computed(() => {
  if (route.path.startsWith('/folders')) return { eyebrow: '工作区', title: route.path.includes('/files') ? '文件状态' : '同步任务' }
  if (route.path === '/tasks') return { eyebrow: '工作区', title: '执行队列' }
  if (route.path === '/reconcile') return { eyebrow: '工作区', title: '异常处理' }
  if (route.path === '/settings') return { eyebrow: '管理', title: '系统设置' }
  return { eyebrow: 'MaxKB Sync', title: '控制台' }
})

function errorMessage(error: unknown, fallback: string) {
  if (typeof error === 'object' && error !== null && 'message' in error) {
    const message = (error as { message?: unknown }).message
    if (typeof message === 'string' && message.trim()) return message
  }
  if (typeof error === 'string' && error.trim()) return error
  return fallback
}

const requiredBridgeMethods = [
  'ListFolders',
  'GetMaxKBConfig',
  'GetMinerUConfig',
  'ListRunningTasks',
  'GetQueueStats',
  'ListTasks',
  'GetAppVersion',
] as const

function hasWailsBridge() {
  const wailsWindow = window as Window & {
    WailsInvoke?: unknown
    runtime?: Record<string, unknown>
    wails?: { Callback?: unknown; EventsNotify?: unknown }
    go?: { main?: { App?: Record<string, unknown> } }
  }
  const bridge = wailsWindow.go?.main?.App
  // `window.go` can remain as a stale object after a Wails dev/native restart.
  // Check the host IPC surface as well; method-shape checks alone cannot tell
  // whether this document was bootstrapped by a live Wails runtime.
  return Boolean(
    typeof wailsWindow.WailsInvoke === 'function' &&
    wailsWindow.runtime &&
    typeof wailsWindow.runtime === 'object' &&
    typeof wailsWindow.wails?.Callback === 'function' &&
    typeof wailsWindow.wails?.EventsNotify === 'function' &&
    bridge &&
    requiredBridgeMethods.every(method => typeof bridge[method] === 'function'),
  )
}

function isWailsReconnectOverlayVisible() {
  const overlay = document.querySelector<HTMLElement>('.wails-reconnect-overlay')
  if (!overlay) return false
  const style = window.getComputedStyle(overlay)
  return style.display !== 'none' && style.visibility !== 'hidden' && Number(style.opacity || '1') > 0.01
}

function updateWailsReconnectOverlayState() {
  if (isUnmounted) return
  const visible = isWailsReconnectOverlayVisible()
  wailsReconnectOverlayVisible.value = visible
  if (visible) {
    reconnectOverlayWasVisible = true
    return
  }

  // Wails dev can reconnect its websocket while leaving the generated RPC
  // callback session stale. Once a previously healthy app has visibly gone
  // through a reconnect, recreate the document and the complete Wails bridge.
  if (reconnectOverlayWasVisible && bridgeWasHealthy) {
    reconnectOverlayWasVisible = false
    reloadForStaleBridge()
  }
}

function reloadFrontend() {
  // A page reload is the only frontend-side way to recreate the Wails
  // document/runtime. It is useful when a dev IPC websocket or native host
  // bridge was replaced while this WebView stayed alive.
  window.location.reload()
}

function startRuntimeOverlayWatch() {
  updateWailsReconnectOverlayState()
  runtimeOverlayObserver = new MutationObserver(updateWailsReconnectOverlayState)
  runtimeOverlayObserver.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'style'] })
  runtimeOverlayTimer = setInterval(updateWailsReconnectOverlayState, 1000)
}

function stopRuntimeOverlayWatch() {
  runtimeOverlayObserver?.disconnect()
  runtimeOverlayObserver = null
  if (runtimeOverlayTimer) {
    clearInterval(runtimeOverlayTimer)
    runtimeOverlayTimer = null
  }
}


const BRIDGE_PROBE_TIMEOUT_MS = 2500
const RELOAD_HISTORY_KEY = 'maxkb-sync:wails-reload-history'

function timeoutAfter(ms: number, message: string) {
  return new Promise<never>((_, reject) => {
    window.setTimeout(() => reject(new Error(message)), ms)
  })
}

async function probeWailsBridge() {
  try {
    const version = await Promise.race([
      Promise.resolve().then(() => WailsApp.GetAppVersion()),
      timeoutAfter(BRIDGE_PROBE_TIMEOUT_MS, 'Wails IPC 探测超时'),
    ])
    if (typeof version !== 'string' || !version.trim()) throw new Error('Wails IPC 探测返回无效')
    bridgeWasHealthy = true
    sessionStorage.removeItem(RELOAD_HISTORY_KEY)
    return true
  } catch {
    return false
  }
}

function reloadForStaleBridge() {
  const now = Date.now()
  let history: number[] = []
  try {
    const raw = sessionStorage.getItem(RELOAD_HISTORY_KEY)
    if (raw) history = (JSON.parse(raw) as unknown[]).map(Number).filter(value => Number.isFinite(value) && now - value < 60_000)
  } catch {
    history = []
  }

  // 最多自动重载两次，避免后端持续不可用时形成刷新循环。
  if (history.length >= 2) return false
  history.push(now)
  sessionStorage.setItem(RELOAD_HISTORY_KEY, JSON.stringify(history))
  window.setTimeout(() => window.location.reload(), 80)
  return true
}

function reloadInterface() {
  sessionStorage.removeItem(RELOAD_HISTORY_KEY)
  window.location.reload()
}

function sanitizeFrontendError(value: unknown) {
  return errorMessage(value, '前端发生未知错误')
    .replace(/Bearer\s+[^\s,;]+/gi, 'Bearer [REDACTED]')
    .replace(/((?:api[-_ ]?key|token|cookie|authorization)\s*[:=]\s*)[^\s,;}&]+/gi, '$1[REDACTED]')
    .slice(0, 500)
}

function handleWindowError(event: ErrorEvent) {
  frontendRuntimeError.value = sanitizeFrontendError(event.error ?? event.message)
}

function handleUnhandledRejection(event: PromiseRejectionEvent) {
  frontendRuntimeError.value = sanitizeFrontendError(event.reason)
}

function handleStaleBridge(event: Event) {
  const detail = (event as CustomEvent<{ message?: string }>).detail
  initializationFailures.value = [{
    label: 'Wails 本地服务',
    message: detail?.message || '当前页面与本地后端的连接已失效，正在重新加载界面。',
  }]
  reloadForStaleBridge()
}

function waitForWailsBridge(timeout = 3000) {
  if (hasWailsBridge()) return Promise.resolve(true)

  return new Promise<boolean>(resolve => {
    const startedAt = Date.now()
    const timer = window.setInterval(() => {
      if (isUnmounted || hasWailsBridge()) {
        window.clearInterval(timer)
        resolve(!isUnmounted && hasWailsBridge())
        return
      }
      if (Date.now() - startedAt >= timeout) {
        window.clearInterval(timer)
        resolve(false)
      }
    }, 100)
  })
}

type StartupOperation = {
  label: string
  run: () => Promise<unknown>
  getStoreError: () => string | null
}

async function runStartupOperations(operations: StartupOperation[]) {
  const results = await Promise.allSettled(operations.map(operation => operation.run()))
  const failures: Array<{ label: string; message: string }> = []

  results.forEach((result, index) => {
    const operation = operations[index]
    if (result.status === 'rejected') {
      failures.push({ label: operation.label, message: errorMessage(result.reason, '调用本地服务失败') })
      return
    }
    const storeError = operation.getStoreError()
    if (storeError) failures.push({ label: operation.label, message: storeError })
  })

  return failures
}

function scheduleInitializationRetry() {
  if (isUnmounted || retryTimer) return
  automaticRetryCount += 1
  const delay = Math.min(10000, 1200 * 2 ** Math.min(automaticRetryCount - 1, 3))
  retryTimer = setTimeout(() => {
    retryTimer = null
    void initializeApp()
  }, delay)
}

async function initializeApp(manualRetry = false) {
  if (initializationLoading.value || refreshLoading.value) return
  if (manualRetry) {
    automaticRetryCount = 0
    if (retryTimer) {
      clearTimeout(retryTimer)
      retryTimer = null
    }
  }

  const requestID = ++initializationRequest
  initializationLoading.value = true
  initializationFailures.value = []
  let failures: Array<{ label: string; message: string }> = []

  try {
    const bridgeReady = await waitForWailsBridge()
    if (isUnmounted || requestID !== initializationRequest) return

    if (!bridgeReady) {
      failures = [{
        label: 'Wails 本地服务',
        message: '未连接到 Wails 本地服务，后端可能仍在重启。',
      }]
    } else if (!await probeWailsBridge()) {
      failures = [{
        label: 'Wails 本地服务',
        message: '检测到页面仍连接旧的后端会话，正在重新加载界面以建立新连接。',
      }]
      reloadForStaleBridge()
    } else {
      failures = await runStartupOperations([
        { label: '同步任务', run: () => foldersStore.fetchFolders(), getStoreError: () => foldersStore.error },
        { label: '系统配置', run: () => configStore.loadConfigs(), getStoreError: () => configStore.error },
        { label: '执行队列', run: () => tasksStore.refreshStatus(), getStoreError: () => tasksStore.error },
      ])
    }
  } catch (error: unknown) {
    failures = [{ label: '应用初始化', message: errorMessage(error, '初始化本地服务失败') }]
  } finally {
    if (isUnmounted || requestID !== initializationRequest) return
    initializationFailures.value = failures
    initializationLoading.value = false
  }

  if (isUnmounted || requestID !== initializationRequest) return
  if (failures.length) scheduleInitializationRetry()
  else automaticRetryCount = 0

  if (tasksStore.hasBackgroundWork) tasksStore.startPolling()
  else tasksStore.stopPolling()
}

onMounted(() => {
  // 不在 mounted 钩子中 await 初始化，保证首屏导航和按钮始终先挂载。
  window.addEventListener('error', handleWindowError)
  window.addEventListener('unhandledrejection', handleUnhandledRejection)
  window.addEventListener('maxkb:wails-bridge-stale', handleStaleBridge)
  startRuntimeOverlayWatch()
  void initializeApp()
})

onUnmounted(() => {
  window.removeEventListener('error', handleWindowError)
  window.removeEventListener('unhandledrejection', handleUnhandledRejection)
  window.removeEventListener('maxkb:wails-bridge-stale', handleStaleBridge)
  isUnmounted = true
  initializationRequest += 1
  if (retryTimer) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  tasksStore.stopPolling()
  stopRuntimeOverlayWatch()
})

async function refreshAll() {
  if (refreshLoading.value || initializationLoading.value) return
  if (retryTimer) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  automaticRetryCount = 0
  const requestID = ++initializationRequest
  refreshLoading.value = true
  initializationLoading.value = true
  initializationFailures.value = []
  let failures: Array<{ label: string; message: string }> = []

  try {
    const bridgeReady = await waitForWailsBridge()
    if (!bridgeReady) {
      failures = [{ label: 'Wails 本地服务', message: '未连接到 Wails 本地服务，后端可能仍在重启。' }]
    } else if (!await probeWailsBridge()) {
      failures = [{ label: 'Wails 本地服务', message: '当前页面的后端连接已经失效，正在重新加载界面。' }]
      reloadForStaleBridge()
    } else {
      failures = await runStartupOperations([
        { label: '同步任务', run: () => foldersStore.fetchFolders(), getStoreError: () => foldersStore.error },
        { label: '执行队列', run: () => tasksStore.refreshStatus(), getStoreError: () => tasksStore.error },
      ])
    }
  } catch (error: unknown) {
    failures = [{ label: '刷新状态', message: errorMessage(error, '刷新本地服务失败') }]
  } finally {
    if (requestID === initializationRequest && !isUnmounted) {
      initializationFailures.value = failures
      initializationLoading.value = false
      refreshLoading.value = false
    }
  }

  if (requestID !== initializationRequest || isUnmounted) return
  if (failures.length) scheduleInitializationRetry()
  else automaticRetryCount = 0
  if (tasksStore.hasBackgroundWork) tasksStore.startPolling()
  else tasksStore.stopPolling()
}
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark"><FolderSync :size="20" stroke-width="2.2" /></div>
        <div class="brand-copy"><strong>MaxKB Sync</strong><span>本地文件同步客户端</span></div>
      </div>

      <nav class="nav" aria-label="主导航">
        <div class="nav-section-label">工作区</div>
        <RouterLink to="/folders" class="nav-item" :class="{ active: route.path.startsWith('/folders') }">
          <FolderSync :size="18" /><span>同步任务</span><ChevronRight class="nav-arrow" :size="15" />
        </RouterLink>
        <RouterLink to="/tasks" class="nav-item" :class="{ active: route.path === '/tasks' }">
          <ListChecks :size="18" /><span>执行队列</span><el-badge v-if="queueTotal" :value="queueTotal" class="nav-badge" />
        </RouterLink>
        <RouterLink to="/reconcile" class="nav-item" :class="{ active: route.path === '/reconcile' }">
          <AlertTriangle :size="18" /><span>异常处理</span><el-badge v-if="tasksStore.queueStats.reconcileRequired" :value="tasksStore.queueStats.reconcileRequired" type="warning" class="nav-badge" />
        </RouterLink>
        <div class="nav-section-label nav-section-spaced">管理</div>
        <RouterLink to="/settings" class="nav-item" :class="{ active: route.path === '/settings' }">
          <Settings2 :size="18" /><span>系统设置</span>
        </RouterLink>
      </nav>

      <div class="sidebar-bottom">
        <div class="connection-card">
          <span class="connection-indicator" :class="{ active: isBusy }"></span>
          <div><strong>{{ isBusy ? '正在同步' : '执行器空闲' }}</strong><span>{{ isBusy ? '全局串行队列运行中' : '等待新的同步批次' }}</span></div>
        </div>
        <div class="sidebar-meta">MaxKB Local Sync <span>v0.1</span></div>
      </div>
    </aside>

    <main class="main-content">
      <header class="topbar">
        <div class="breadcrumbs"><span>{{ currentPage.eyebrow }}</span><ChevronRight :size="14" /><strong>{{ currentPage.title }}</strong></div>
        <div class="topbar-actions">
          <div class="queue-pill" :class="{ active: isBusy }"><Activity :size="16" /><span>{{ isBusy ? '队列运行中' : '队列空闲' }}</span><b>{{ queueTotal }}</b></div>
          <el-tooltip content="刷新任务和队列状态" placement="bottom">
            <el-button circle plain :loading="refreshLoading || foldersStore.loading || tasksStore.loading" :disabled="refreshLoading || initializationLoading" @click="refreshAll"><RefreshCw :size="16" /></el-button>
          </el-tooltip>
        </div>
      </header>
      <div
        v-if="wailsReconnectOverlayVisible || frontendRuntimeError || initializationLoading || initializationFailures.length"
        class="app-notice-stack"
        aria-live="polite"
      >
        <section
          v-if="wailsReconnectOverlayVisible"
          class="wails-recovery-notice"
          role="alert"
          aria-live="assertive"
        >
          <div class="startup-notice-content">
            <strong>Wails 本地连接已断开</strong>
            <p>开发运行时的重连遮罩正在阻止页面点击；后端恢复后仍无响应时，重新加载界面可重建 bridge。</p>
          </div>
          <el-button size="small" type="primary" @click="reloadFrontend">重新加载界面</el-button>
        </section>

        <section v-if="frontendRuntimeError" class="startup-notice startup-notice-error" role="alert">
          <div class="startup-notice-icon"><AlertTriangle :size="17" /></div>
          <div class="startup-notice-content">
            <strong>界面脚本发生错误</strong>
            <p>{{ frontendRuntimeError }}</p>
          </div>
          <el-button size="small" plain @click="reloadInterface">重新加载界面</el-button>
        </section>
        <section
          v-if="initializationLoading || initializationFailures.length"
          class="startup-notice"
          :class="{ 'startup-notice-error': !initializationLoading && initializationFailures.length }"
          role="status"
        >
          <div class="startup-notice-icon">
            <RefreshCw v-if="initializationLoading" class="startup-spin" :size="17" />
            <AlertTriangle v-else :size="17" />
          </div>
          <div class="startup-notice-content">
            <strong>{{ initializationLoading ? '正在连接本地服务' : '部分初始化未完成' }}</strong>
            <p v-if="initializationLoading">界面已可使用，正在读取任务、队列和系统配置。</p>
            <template v-else>
              <p>Wails 后端重启或单个接口失败不会阻断界面，其余功能仍可继续使用。</p>
              <ul>
                <li v-for="failure in initializationFailures" :key="`${failure.label}:${failure.message}`">
                  <b>{{ failure.label }}：</b>{{ failure.message }}
                </li>
              </ul>
            </template>
          </div>
          <div class="startup-notice-actions">
            <el-button size="small" plain :loading="initializationLoading" @click="initializeApp(true)">重试</el-button>
            <el-button v-if="!initializationLoading && initializationFailures.length" size="small" type="primary" plain @click="reloadInterface">重新加载界面</el-button>
          </div>
        </section>
      </div>
      <div class="content-scroll"><RouterView v-slot="{ Component }"><component :is="Component" /></RouterView></div>
    </main>
  </div>
</template>


<style scoped>
.app-notice-stack {
  position: fixed;
  top: 82px;
  left: calc(50% + var(--sidebar-width) / 2);
  z-index: 1000001;
  display: flex;
  width: min(470px, calc(100vw - var(--sidebar-width) - 48px));
  min-width: 0;
  flex-direction: column;
  gap: 10px;
  transform: translateX(-50%);
  pointer-events: none;
}

.wails-recovery-notice,
.startup-notice {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: flex-start;
  gap: 11px;
  margin: 0;
  padding: 11px 13px;
  color: #3730a3;
  background: #f5f3ff;
  border: 1px solid #ddd6fe;
  border-radius: 8px;
  box-shadow: 0 12px 30px rgba(15, 23, 42, .12);
  pointer-events: auto;
}

.wails-recovery-notice {
  color: #7f1d1d;
  background: #fff1f2;
  border-color: #fda4af;
}

.startup-notice-error {
  color: #92400e;
  background: #fffbeb;
  border-color: #fde68a;
}

.startup-notice-icon {
  display: flex;
  flex: 0 0 auto;
  padding-top: 1px;
}

.startup-notice-content {
  flex: 1;
  min-width: 0;
  font-size: 12px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.startup-notice-content strong {
  display: block;
  margin-bottom: 2px;
  font-size: 12px;
}

.startup-notice-content p {
  margin: 0;
}

.startup-notice-content ul {
  margin: 4px 0 0;
  padding-left: 17px;
}

.startup-notice-content li {
  word-break: break-word;
}

.wails-recovery-notice :deep(.el-button),
.startup-notice :deep(.el-button) {
  flex: 0 0 auto;
  margin-top: 1px;
}

.startup-notice-actions {
  display: flex;
  flex: 0 0 auto;
  gap: 6px;
}

.startup-spin {
  animation: startup-spin 1s linear infinite;
}

@keyframes startup-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
