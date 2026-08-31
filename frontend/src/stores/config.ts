import { defineStore } from 'pinia'
import { ref } from 'vue'
import type {
  MaxKBConfigDTO,
  MinerUArtifactBindings,
  MinerUArtifactCleanupResultDTO,
  MinerUArtifactConfigDTO,
  MinerUConfigDTO,
} from '../types'
import * as App from '../../wailsjs/go/main/App'

import { errorMessage, withTimeout } from './store-helpers'

const CONFIG_REQUEST_TIMEOUT_MS = 15_000

const DEFAULT_MINERU_ARTIFACT_CONFIG: MinerUArtifactConfigDTO = {
  resultSaveDir: '',
  cleanupPolicy: 'never',
  cleanupAfterValue: 30,
  cleanupAfterUnit: 'day',
  cleanupAfterDays: 30,
  cleanupKeepBatches: 5,
  cleanupCron: '0 3 * * *',
  cleanupResult: {
    status: '',
    deletedCount: 0,
    skippedCount: 0,
    error: '',
    at: '',
    deletedFiles: 0,
  },
}

type WailsAppBridge = Record<string, unknown>

function getOptionalArtifactMethod<K extends keyof MinerUArtifactBindings>(name: K): MinerUArtifactBindings[K] | null {
  if (typeof window === 'undefined') return null
  const bridge = (window as Window & { go?: { main?: { App?: WailsAppBridge } } }).go?.main?.App
  const method = bridge?.[name as string]
  return typeof method === 'function'
    ? ((...args: unknown[]) => Promise.resolve().then(() => (method as (...callArgs: unknown[]) => unknown).apply(bridge, args))) as MinerUArtifactBindings[K]
    : null
}

type ArtifactConfigPayload = Partial<MinerUArtifactConfigDTO> & {
  lastCleanupAt?: unknown
  lastCleanupStatus?: unknown
  lastCleanupDeletedCount?: unknown
  lastCleanupSkippedCount?: unknown
  lastCleanupError?: unknown
}

function asNonNegativeInteger(value: unknown, fallback = 0): number {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? Math.max(0, Math.round(numberValue)) : fallback
}

function normalizeCleanupResult(value: unknown): MinerUArtifactCleanupResultDTO {
  const source = (typeof value === 'object' && value !== null ? value : {}) as Record<string, unknown>
  const deletedCount = asNonNegativeInteger(source.deletedCount ?? source.deletedFiles)
  return {
    status: typeof source.status === 'string' ? source.status : '',
    deletedCount,
    skippedCount: asNonNegativeInteger(source.skippedCount),
    error: typeof source.error === 'string' ? source.error : '',
    at: typeof source.at === 'string' ? source.at : '',
    // Compatibility for the existing settings view. New code should use deletedCount.
    deletedFiles: deletedCount,
    deletedBytes: Number.isFinite(Number(source.deletedBytes)) ? Math.max(0, Number(source.deletedBytes)) : undefined,
  }
}

function normalizeArtifactConfig(value: unknown): MinerUArtifactConfigDTO {
  const source = (typeof value === 'object' && value !== null ? value : {}) as ArtifactConfigPayload
  const cleanupResultSource = source.cleanupResult ?? {
    status: source.lastCleanupStatus,
    deletedCount: source.lastCleanupDeletedCount,
    skippedCount: source.lastCleanupSkippedCount,
    error: source.lastCleanupError,
    at: source.lastCleanupAt,
  }
  const rawPolicy = source.cleanupPolicy
  const cleanupPolicy = rawPolicy === 'after_days'
    ? 'after_duration'
    : rawPolicy === 'immediate' || rawPolicy === 'after_duration' || rawPolicy === 'keep_batches'
      ? rawPolicy
      : 'never'
  const legacyDays = asNonNegativeInteger(source.cleanupAfterDays, 30)
  const cleanupAfterValue = asNonNegativeInteger(source.cleanupAfterValue, legacyDays || 30)
  const cleanupAfterUnit = source.cleanupAfterUnit === 'hour' ? 'hour' : 'day'

  return {
    resultSaveDir: typeof source.resultSaveDir === 'string' ? source.resultSaveDir : '',
    cleanupPolicy,
    cleanupAfterValue,
    cleanupAfterUnit,
    cleanupAfterDays: cleanupAfterUnit === 'day' ? cleanupAfterValue : legacyDays,
    cleanupKeepBatches: asNonNegativeInteger(source.cleanupKeepBatches, 5),
    cleanupCron: typeof source.cleanupCron === 'string' && source.cleanupCron.trim() ? source.cleanupCron : '0 3 * * *',
    cleanupResult: normalizeCleanupResult(cleanupResultSource),
  }
}

export const useConfigStore = defineStore('config', () => {
  const maxKBConfig = ref<MaxKBConfigDTO>({ baseUrl: '', apiKey: '' })
  const minerUConfig = ref<MinerUConfigDTO>({ enabled: false, baseUrl: '', apiKey: '', mode: 'online' })
  const minerUArtifactConfig = ref<MinerUArtifactConfigDTO>({ ...DEFAULT_MINERU_ARTIFACT_CONFIG })
  const minerUArtifactCapabilities = ref({ read: false, write: false, cleanup: false })
  const loading = ref(false)
  const artifactLoading = ref(false)
  const error = ref<string | null>(null)
  const testingMaxKB = ref(false)
  const testingMinerU = ref(false)
  const maxKBTestResult = ref<string | null>(null)
  const minerUTestResult = ref<string | null>(null)

  let loadRequestID = 0
  let artifactLoadRequestID = 0
  let maxKBTestID = 0
  let minerUTestID = 0

  async function loadConfigs() {
    const requestID = ++loadRequestID
    loading.value = true
    error.value = null
    try {
      const [mkb, mu] = await Promise.all([
        withTimeout(() => App.GetMaxKBConfig(), '读取 MaxKB 配置', CONFIG_REQUEST_TIMEOUT_MS),
        withTimeout(() => App.GetMinerUConfig(), '读取 MinerU 配置', CONFIG_REQUEST_TIMEOUT_MS),
      ])
      if (requestID !== loadRequestID) return
      maxKBConfig.value = mkb
      minerUConfig.value = mu
    } catch (e: unknown) {
      if (requestID === loadRequestID) error.value = errorMessage(e, '读取系统配置失败')
    } finally {
      // A stale request must not turn off loading for a newer request.
      if (requestID === loadRequestID) loading.value = false
    }
  }

  async function loadMinerUArtifactConfig() {
    const requestID = ++artifactLoadRequestID
    const getConfig = getOptionalArtifactMethod('GetMinerUArtifactSettings')
    const configure = getOptionalArtifactMethod('ConfigureMinerUArtifactSettings')
    const cleanup = getOptionalArtifactMethod('CleanupMinerUArtifacts')
    minerUArtifactCapabilities.value = {
      read: Boolean(getConfig),
      write: Boolean(configure),
      cleanup: Boolean(cleanup),
    }
    if (!getConfig) {
      // Older app.go bindings do not have the optional system-level API. Keep
      // the form usable for previewing a future configuration without turning
      // an absent capability into a misleading loading error.
      minerUArtifactConfig.value = { ...DEFAULT_MINERU_ARTIFACT_CONFIG }
      artifactLoading.value = false
      return
    }

    artifactLoading.value = true
    try {
      const config = await withTimeout(
        () => getConfig(),
        '读取 MinerU 产物配置',
        CONFIG_REQUEST_TIMEOUT_MS,
      )
      if (requestID === artifactLoadRequestID) minerUArtifactConfig.value = normalizeArtifactConfig(config)
    } catch (e: unknown) {
      if (requestID === artifactLoadRequestID) error.value = errorMessage(e, '读取 MinerU 产物配置失败')
    } finally {
      if (requestID === artifactLoadRequestID) artifactLoading.value = false
    }
  }

  async function saveMaxKBConfig(config: MaxKBConfigDTO) {
    error.value = null
    try {
      await withTimeout(() => App.ConfigureMaxKB(config), '保存 MaxKB 配置', CONFIG_REQUEST_TIMEOUT_MS)
      maxKBConfig.value = { ...config }
    } catch (e: unknown) {
      error.value = errorMessage(e, '保存 MaxKB 配置失败')
      throw new Error(error.value)
    }
  }

  async function saveMinerUConfig(config: MinerUConfigDTO) {
    error.value = null
    try {
      await withTimeout(() => App.ConfigureMinerU(config), '保存 MinerU 配置', CONFIG_REQUEST_TIMEOUT_MS)
      minerUConfig.value = { ...config }
    } catch (e: unknown) {
      error.value = errorMessage(e, '保存 MinerU 配置失败')
      throw new Error(error.value)
    }
  }

  async function saveMinerUArtifactConfig(config: MinerUArtifactConfigDTO) {
    const configure = getOptionalArtifactMethod('ConfigureMinerUArtifactSettings')
    if (!configure) throw new Error('当前后端尚未提供 MinerU 产物配置绑定，请更新 app.go 后重试。')
    error.value = null
    try {
      const normalized = normalizeArtifactConfig(config)
      const payload: MinerUArtifactConfigDTO = {
        resultSaveDir: normalized.resultSaveDir,
        cleanupPolicy: normalized.cleanupPolicy,
        cleanupAfterValue: normalized.cleanupAfterValue,
        cleanupAfterUnit: normalized.cleanupAfterUnit,
        cleanupKeepBatches: normalized.cleanupKeepBatches,
        cleanupCron: normalized.cleanupCron,
      }
      await withTimeout(
        () => configure(payload),
        '保存 MinerU 产物配置',
        CONFIG_REQUEST_TIMEOUT_MS,
      )
      // Cleanup result is read-only and must survive a settings save.
      minerUArtifactConfig.value = {
        ...normalized,
        cleanupResult: minerUArtifactConfig.value.cleanupResult,
      }
    } catch (e: unknown) {
      error.value = errorMessage(e, '保存 MinerU 产物配置失败')
      throw new Error(error.value)
    }
  }

  async function cleanupMinerUArtifacts(): Promise<MinerUArtifactCleanupResultDTO> {
    const cleanup = getOptionalArtifactMethod('CleanupMinerUArtifacts')
    if (!cleanup) throw new Error('当前后端尚未提供 MinerU 产物清理绑定，请更新 app.go 后重试。')
    error.value = null
    try {
      const result = await withTimeout(
        () => cleanup(),
        '立即清理 MinerU 产物',
        CONFIG_REQUEST_TIMEOUT_MS,
      )
      const normalized = normalizeCleanupResult(result)
      minerUArtifactConfig.value = {
        ...minerUArtifactConfig.value,
        cleanupResult: normalized,
      }
      return normalized
    } catch (e: unknown) {
      error.value = errorMessage(e, '清理 MinerU 产物失败')
      throw new Error(error.value)
    }
  }

  async function testMaxKB(config: MaxKBConfigDTO) {
    const testID = ++maxKBTestID
    testingMaxKB.value = true
    maxKBTestResult.value = null
    try {
      const version = await withTimeout(() => App.TestMaxKBConnection(config), '测试 MaxKB 连接', CONFIG_REQUEST_TIMEOUT_MS)
      if (testID === maxKBTestID) {
        maxKBTestResult.value = 'success:' + String(version ?? '')
        error.value = null
      }
    } catch (e: unknown) {
      if (testID === maxKBTestID) {
        const message = errorMessage(e, 'MaxKB 连接失败')
        maxKBTestResult.value = 'error:' + message
        error.value = message
      }
    } finally {
      if (testID === maxKBTestID) testingMaxKB.value = false
    }
  }

  async function testMinerU(config: MinerUConfigDTO) {
    const testID = ++minerUTestID
    testingMinerU.value = true
    minerUTestResult.value = null
    try {
      await withTimeout(() => App.TestMinerUConnection(config), '测试 MinerU 连接', CONFIG_REQUEST_TIMEOUT_MS)
      if (testID === minerUTestID) {
        minerUTestResult.value = 'success'
        error.value = null
      }
    } catch (e: unknown) {
      if (testID === minerUTestID) {
        const message = errorMessage(e, 'MinerU 连接失败')
        minerUTestResult.value = 'error:' + message
        error.value = message
      }
    } finally {
      if (testID === minerUTestID) testingMinerU.value = false
    }
  }

  return {
    maxKBConfig,
    minerUConfig,
    minerUArtifactConfig,
    minerUArtifactCapabilities,
    loading,
    artifactLoading,
    error,
    testingMaxKB,
    testingMinerU,
    maxKBTestResult,
    minerUTestResult,
    loadConfigs,
    loadMinerUArtifactConfig,
    saveMaxKBConfig,
    saveMinerUConfig,
    saveMinerUArtifactConfig,
    cleanupMinerUArtifacts,
    testMaxKB,
    testMinerU,
  }
})
