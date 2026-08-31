/**
 * Shared safeguards for Wails RPC calls made by Pinia stores.
 *
 * A Wails bridge call cannot be cancelled from the browser, but timing out the
 * store-side wait still lets UI state leave its loading state and lets the
 * caller retry after the backend has restarted.
 */
export const DEFAULT_WAILS_TIMEOUT_MS = 15_000
const BRIDGE_PROBE_TIMEOUT_MS = 1_500
const BRIDGE_PROBE_CACHE_MS = 2_000
let bridgeProbeInFlight: Promise<void> | null = null
let bridgeHealthyUntil = 0

type WailsBridgeWindow = Window & {
  go?: { main?: { App?: Record<string, unknown> } }
}

function notifyStaleBridge(message: string) {
  window.dispatchEvent(new CustomEvent('maxkb:wails-bridge-stale', {
    detail: { message },
  }))
}

/**
 * Verify that the generated Wails method table is backed by a live IPC
 * session. During `wails dev` the JavaScript objects may survive a backend
 * restart even though calls made through them will never settle.
 */
async function ensureWailsBridgeAlive(): Promise<void> {
  if (Date.now() < bridgeHealthyUntil) return
  if (bridgeProbeInFlight) return bridgeProbeInFlight

  bridgeProbeInFlight = (async () => {
    const bridge = (window as WailsBridgeWindow).go?.main?.App
    const getVersion = bridge?.GetAppVersion
    if (typeof getVersion !== 'function') {
      const message = 'Wails 本地连接尚未建立，请等待后端启动。'
      notifyStaleBridge(message)
      throw new Error(message)
    }

    let timer: ReturnType<typeof setTimeout> | undefined
    try {
      const version = await Promise.race([
        Promise.resolve().then(() => getVersion()),
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => reject(new Error('Wails IPC 健康检查超时')), BRIDGE_PROBE_TIMEOUT_MS)
        }),
      ])
      if (typeof version !== 'string' || !version.trim()) {
        throw new Error('Wails IPC 健康检查返回无效')
      }
      bridgeHealthyUntil = Date.now() + BRIDGE_PROBE_CACHE_MS
    } catch (error) {
      bridgeHealthyUntil = 0
      const message = errorMessage(error, 'Wails 本地连接已失效')
      notifyStaleBridge(message)
      throw new Error(`${message}，界面将自动重新连接。`)
    } finally {
      if (timer !== undefined) clearTimeout(timer)
    }
  })().finally(() => {
    bridgeProbeInFlight = null
  })

  return bridgeProbeInFlight
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function redactSensitiveText(value: string): string {
  return value
    .replace(/Bearer\s+[^\s,;]+/gi, 'Bearer [REDACTED]')
    .replace(/((?:api[-_ ]?key|token|cookie|authorization)\s*[:=]\s*)[^\s,;}&]+/gi, '$1[REDACTED]')
    .trim()
    .slice(0, 1000)
}

function valueToMessage(value: unknown, depth = 0): string | null {
  if (depth > 3 || value === null || value === undefined) return null
  if (value instanceof Error && value.message) return value.message
  if (typeof value === 'string' && value.trim()) return value.trim()
  if (!isRecord(value)) return null

  for (const key of ['message', 'error', 'detail', 'details', 'cause', 'reason']) {
    const message = valueToMessage(value[key], depth + 1)
    if (message) return message
  }

  const status = value.status ?? value.statusCode ?? value.httpStatus
  const code = value.code
  if (status !== undefined || code !== undefined) {
    const parts = [status !== undefined ? `状态 ${String(status)}` : '', code !== undefined ? `代码 ${String(code)}` : '']
      .filter(Boolean)
      .join('，')
    if (parts) return parts
  }
  return null
}

/**
 * 识别“扫描后没有待同步变更”这一预期业务结果。
 *
 * Wails 将 Go error 作为文本传到前端，因此优先使用 API 提供的稳定标记，
 * 同时保留旧版本英文消息兼容，避免开发环境热更新时出现错误红提示。
 */
export function isNoPendingChangesError(error: unknown): boolean {
  const message = errorMessage(error, '').toLowerCase()
  return message.includes('no_pending_changes') || message.includes('no pending changes to sync')
}

/** Convert Wails string/object/Go error values into a readable, safe message. */
export function errorMessage(error: unknown, fallback = '操作失败'): string {
  const message = valueToMessage(error)
  if (!message) return fallback
  const safe = redactSensitiveText(message)
  return safe || fallback
}

/** Reject malformed successful responses before callers hit opaque `.map` errors. */
export function requireArray<T>(value: unknown, label: string): T[] {
  if (!Array.isArray(value)) {
    throw new Error(`${label}返回格式无效：预期数组。`)
  }
  return value as T[]
}

/**
 * Call a Wails API and stop waiting after a bounded period.
 * The factory also captures synchronous bridge failures (for example when the
 * Wails runtime is not available after a restart).
 */
export function withTimeout<T>(
  operation: () => Promise<T> | T,
  label: string,
  timeoutMs = DEFAULT_WAILS_TIMEOUT_MS,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  // Probe before starting the real operation. This turns the otherwise silent
  // Wails-dev stale-session hang into a visible error within 1.5 seconds and
  // lets App.vue rebuild the page/bridge automatically.
  const operationPromise = ensureWailsBridgeAlive().then(operation)
  const timeoutPromise = new Promise<never>((_, reject) => {
    timer = setTimeout(() => {
      reject(new Error(`${label}超时，请确认 Wails 后端已启动后重试。`))
    }, timeoutMs)
  })

  // Promise.race attaches rejection handlers to both branches, so a late Wails
  // rejection after the UI timeout does not become an unhandled rejection.
  return Promise.race([operationPromise, timeoutPromise]).finally(() => {
    if (timer !== undefined) clearTimeout(timer)
  })
}
