<script lang="ts" setup>
import { computed, onActivated, onMounted, reactive, ref } from 'vue'
import { ElMessageBox, ElTooltip } from 'element-plus'
import { useConfigStore } from '../stores/config'
import { errorMessage, withTimeout } from '../stores/store-helpers'
import { notifyError, notifySuccess, notifyWarning } from '../utils/notify'
import type { MinerUArtifactConfigDTO } from '../types'
import * as App from '../../wailsjs/go/main/App'

const store = useConfigStore()
const saving = ref('')
const activeTab = ref('maxkb')
const maxkb = reactive({ baseUrl: '', apiKey: '' })
const mineru = reactive({ enabled: false, baseUrl: 'https://mineru.net', apiKey: '', mode: 'online' })
type ArtifactCleanupPolicy = 'immediate' | 'never' | 'after_duration' | 'after_days' | 'keep_batches'

const mineruArtifact = reactive({
  resultSaveDir: '',
  cleanupPolicy: 'never' as ArtifactCleanupPolicy,
  cleanupAfterValue: 30,
  cleanupAfterUnit: 'day' as 'hour' | 'day',
  cleanupAfterDays: 30,
  cleanupKeepBatches: 5,
  cleanupCron: '0 3 * * *',
})
const artifactCleanupSummary = reactive({
  at: '',
  status: '',
  deletedCount: 0,
  skippedCount: 0,
  error: '',
})
const maxkbApiKeyMasked = ref(false)
const mineruApiKeyMasked = ref(false)
const originalMaxKBApiKey = ref('')
const originalMinerUApiKey = ref('')

const artifactCapabilities = computed(() => store.minerUArtifactCapabilities)
const artifactCronValidating = ref(false)
const cronError = ref('')
const artifactConfigAvailable = computed(() => artifactCapabilities.value.read || artifactCapabilities.value.write || artifactCapabilities.value.cleanup)
const mineruConfigDisabled = computed(() => !mineru.enabled || saving.value !== '')
const artifactSaveDisabled = computed(() => !mineru.enabled || !artifactCapabilities.value.write || saving.value !== '' || artifactCronValidating.value)
const artifactCleanupDisabled = computed(() => !mineru.enabled || !artifactCapabilities.value.cleanup || saving.value !== '' || !mineruArtifact.resultSaveDir.trim())
const artifactCleanupPolicy = computed(() => mineruArtifact.cleanupPolicy)
const artifactHasCleanupResult = computed(() => Boolean(artifactCleanupSummary.at || artifactCleanupSummary.status || artifactCleanupSummary.error))

async function refreshSettingsForm() {
  try {
    await Promise.allSettled([store.loadConfigs(), store.loadMinerUArtifactConfig()])
    loadConfigsToForm()
    await loadArtifactCleanupSummary()
    if (store.error) notifyError(store.error)
  } catch (e: unknown) {
    notifyError(errorMessage(e, '读取系统配置失败'))
  }
}

async function loadArtifactCleanupSummary() {
  if (!artifactCapabilities.value.read) return
  try {
    // The store intentionally keeps a small, forward-compatible DTO. Read the
    // optional read-only cleanup fields here when the generated Wails binding
    // exposes them, without putting any sensitive value into the form.
    const settings = await withTimeout(() => App.GetMinerUArtifactSettings(), '读取 MinerU 清理结果', 15_000)
    const value = (typeof settings === 'object' && settings !== null ? settings : {}) as Record<string, unknown>
    artifactCleanupSummary.at = typeof value.lastCleanupAt === 'string' ? value.lastCleanupAt : ''
    artifactCleanupSummary.status = typeof value.lastCleanupStatus === 'string' ? value.lastCleanupStatus : ''
    artifactCleanupSummary.deletedCount = toNonNegativeNumber(value.lastCleanupDeletedCount)
    artifactCleanupSummary.error = typeof value.lastCleanupError === 'string' ? value.lastCleanupError : ''
  } catch {
    // The store load result is already surfaced above. A missing optional
    // summary must not make the whole Settings page appear to have failed.
  }
}

function toNonNegativeNumber(value: unknown) {
  const number = Number(value)
  return Number.isFinite(number) ? Math.max(0, Math.round(number)) : 0
}

onMounted(() => { void refreshSettingsForm() })
onActivated(() => { void refreshSettingsForm() })

function loadConfigsToForm() {
  Object.assign(maxkb, store.maxKBConfig)
  Object.assign(mineru, store.minerUConfig)
  const artifactConfig = store.minerUArtifactConfig
  const rawPolicy = artifactConfig.cleanupPolicy
  const policy: ArtifactCleanupPolicy = rawPolicy === 'after_days'
    ? 'after_duration'
    : rawPolicy === 'immediate' || rawPolicy === 'after_duration' || rawPolicy === 'keep_batches'
      ? rawPolicy
      : 'never'
  const legacyDays = Number.isFinite(Number(artifactConfig.cleanupAfterDays)) ? Math.max(0, Math.round(Number(artifactConfig.cleanupAfterDays))) : 30
  const afterValue = Number.isFinite(Number(artifactConfig.cleanupAfterValue))
    ? Math.max(0, Math.round(Number(artifactConfig.cleanupAfterValue)))
    : legacyDays || 30
  Object.assign(mineruArtifact, {
    resultSaveDir: artifactConfig.resultSaveDir || '',
    cleanupPolicy: policy,
    cleanupAfterValue: afterValue,
    cleanupAfterUnit: artifactConfig.cleanupAfterUnit === 'hour' ? 'hour' : 'day',
    cleanupAfterDays: legacyDays || afterValue,
    cleanupKeepBatches: Number.isFinite(Number(artifactConfig.cleanupKeepBatches)) ? Math.max(0, Math.round(Number(artifactConfig.cleanupKeepBatches))) : 5,
    cleanupCron: typeof artifactConfig.cleanupCron === 'string' && artifactConfig.cleanupCron.trim() ? artifactConfig.cleanupCron : '0 3 * * *',
  })
  if (!mineru.baseUrl) mineru.baseUrl = 'https://mineru.net'
  if (!mineru.mode) mineru.mode = 'online'

  originalMaxKBApiKey.value = store.maxKBConfig.apiKey || ''
  originalMinerUApiKey.value = store.minerUConfig.apiKey || ''

  if (originalMaxKBApiKey.value) {
    maxkbApiKeyMasked.value = true
    maxkb.apiKey = '••••••••••••••••••••••••••••••••••••••'
  } else {
    maxkbApiKeyMasked.value = false
  }
  if (originalMinerUApiKey.value) {
    mineruApiKeyMasked.value = true
    mineru.apiKey = '•••••••••••••••••••••••••••••••'
  } else {
    mineruApiKeyMasked.value = false
  }
}

async function saveMaxKB() {
  saving.value = 'maxkb'
  try {
    const payload = { ...maxkb }
    if (maxkbApiKeyMasked.value && maxkb.apiKey.startsWith('•••')) payload.apiKey = originalMaxKBApiKey.value
    else originalMaxKBApiKey.value = payload.apiKey
    await store.saveMaxKBConfig(payload)
    notifySuccess('MaxKB 配置已保存')
    maxkbApiKeyMasked.value = true
    maxkb.apiKey = '••••••••••••••••••••••••••••••••••••••'
  } catch (e: unknown) {
    notifyError(errorMessage(e, '保存 MaxKB 配置失败'))
  } finally {
    saving.value = ''
  }
}

async function testMaxKB() {
  saving.value = 'test-maxkb'
  try {
    const payload = { ...maxkb }
    if (maxkbApiKeyMasked.value && maxkb.apiKey.startsWith('•••')) payload.apiKey = originalMaxKBApiKey.value
    await store.testMaxKB(payload)
    if (store.maxKBTestResult?.startsWith('success:')) {
      notifySuccess(`MaxKB 连接正常 (${store.maxKBTestResult.substring(8)})`)
    } else {
      notifyError(store.maxKBTestResult ?? '连接失败')
    }
  } catch (e: unknown) {
    notifyError(errorMessage(e, 'MaxKB 连接测试失败'))
  } finally {
    saving.value = ''
  }
}

async function onMinerUEnabledChange(enabled: boolean) {
  if (enabled) {
    notifyWarning('已开启 MinerU，请完成服务连接和产物目录配置后点击“保存配置”。')
    return
  }

  const previous = { ...mineru }
  saving.value = 'mineru-toggle'
  try {
    // Turning the service off only changes its enabled state. Preserve the
    // masked credential marker so re-enabling does not ask for the token
    // again, and let the backend keep the credential in the system store.
    const preservedApiKey = originalMinerUApiKey.value || mineru.apiKey
    await store.saveMinerUConfig({ ...mineru, enabled: false, apiKey: preservedApiKey })
    if (preservedApiKey) {
      mineru.apiKey = '•••••••••••••••••••••••••••••'
      mineruApiKeyMasked.value = true
    }
    notifySuccess('MinerU 已关闭')
  } catch (e: unknown) {
    Object.assign(mineru, previous)
    notifyError(errorMessage(e, '关闭 MinerU 失败'))
  } finally {
    saving.value = ''
  }
}

async function saveMinerUConfiguration() {
  saving.value = 'mineru-config'
  try {
    await validateMinerUArtifactConfig()
    const servicePayload = { ...mineru }
    if (mineruApiKeyMasked.value && mineru.apiKey.startsWith('•••')) servicePayload.apiKey = originalMinerUApiKey.value
    else originalMinerUApiKey.value = servicePayload.apiKey

    // Persist the service state first. The backend validates the required
    // artifact directory against the persisted MinerU enabled state.
    await store.saveMinerUConfig(servicePayload)
    await store.saveMinerUArtifactConfig({
      resultSaveDir: mineruArtifact.resultSaveDir.trim(),
      cleanupPolicy: mineruArtifact.cleanupPolicy,
      cleanupAfterValue: Math.max(0, Math.round(mineruArtifact.cleanupAfterValue)),
      cleanupAfterUnit: mineruArtifact.cleanupAfterUnit,
      cleanupAfterDays: Math.max(0, Math.round(mineruArtifact.cleanupAfterValue)),
      cleanupKeepBatches: Math.max(0, Math.round(mineruArtifact.cleanupKeepBatches)),
      cleanupCron: mineruArtifact.cleanupCron.trim(),
    } as MinerUArtifactConfigDTO)
    await loadArtifactCleanupSummary()
    if (servicePayload.apiKey && servicePayload.apiKey !== '•••••••••••••••••••••••••••••') {
      mineruApiKeyMasked.value = true
      mineru.apiKey = '•••••••••••••••••••••••••••••'
    }
    notifySuccess('MinerU 配置已保存')
  } catch (e: unknown) {
    notifyError(errorMessage(e, '保存 MinerU 配置失败'))
  } finally {
    saving.value = ''
  }
}

async function testMinerU() {
  saving.value = 'test-mineru'
  try {
    const payload = { ...mineru }
    if (mineruApiKeyMasked.value && mineru.apiKey.startsWith('•••')) payload.apiKey = originalMinerUApiKey.value
    await store.testMinerU(payload)
    if (store.minerUTestResult === 'success') notifySuccess('MinerU 连接正常')
    else notifyError(store.minerUTestResult ?? '连接失败')
  } catch (e: unknown) {
    notifyError(errorMessage(e, 'MinerU 连接测试失败'))
  } finally {
    saving.value = ''
  }
}

const cleanupStatusLabel = computed(() => {
  const status = artifactCleanupSummary.status.toLowerCase()
  if (status === 'success') return '成功'
  if (status === 'partial') return '部分完成'
  if (status === 'failed') return '失败'
  if (status.startsWith('skipped')) return '已跳过'
  return artifactCleanupSummary.status || '未知'
})
const cleanupStatusClass = computed(() => {
  const status = artifactCleanupSummary.status.toLowerCase()
  if (status === 'success') return 'status-success'
  if (status === 'failed' || status === 'partial' || artifactCleanupSummary.error) return 'status-danger'
  return 'status-warning'
})

function formatCleanupTime(value: string) {
  if (!value) return '暂无记录'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

async function selectMinerUArtifactDirectory() {
  saving.value = 'select-mineru-artifact-dir'
  try {
    const path = await withTimeout(() => App.SelectDirectory(), '选择 MinerU 产物目录', 60_000)
    if (path?.trim()) mineruArtifact.resultSaveDir = path.trim()
  } catch (e: unknown) {
    notifyError(errorMessage(e, '选择 MinerU 产物目录失败'))
  } finally {
    saving.value = ''
  }
}

function setArtifactCleanupPolicy(value: string | number | boolean) {
  const policy: ArtifactCleanupPolicy = value === 'immediate' || value === 'after_duration' || value === 'after_days' || value === 'keep_batches'
    ? (value === 'after_days' ? 'after_duration' : value)
    : 'never'
  mineruArtifact.cleanupPolicy = policy
  if (policy === 'immediate' || policy === 'never') cronError.value = ''
}

async function validateArtifactCron() {
  if (mineruArtifact.cleanupPolicy === 'never' || mineruArtifact.cleanupPolicy === 'immediate') {
    cronError.value = ''
    return true
  }
  const cron = mineruArtifact.cleanupCron.trim()
  if (!cron) {
    cronError.value = '启用自动清理策略时，请填写 Cron 表达式。'
    return false
  }
  artifactCronValidating.value = true
  try {
    await withTimeout(() => App.ValidateCronExpression(cron), '校验 MinerU 清理 Cron', 15_000)
    cronError.value = ''
    return true
  } catch (e: unknown) {
    cronError.value = errorMessage(e, 'Cron 表达式无效')
    return false
  } finally {
    artifactCronValidating.value = false
  }
}

async function validateMinerUArtifactConfig() {
  if (mineru.enabled && !mineruArtifact.resultSaveDir.trim()) {
    throw new Error('启用 MinerU 时，产物保存目录不能为空。')
  }
  if (mineruArtifact.cleanupPolicy === 'after_duration' && mineruArtifact.cleanupAfterValue <= 0) {
    throw new Error('按时间清理时，保留时长必须大于 0。')
  }
  if (mineruArtifact.cleanupPolicy === 'keep_batches' && mineruArtifact.cleanupKeepBatches <= 0) {
    throw new Error('按批次清理时，保留批次数必须大于 0。')
  }
  if (!(await validateArtifactCron())) throw new Error(cronError.value)
}

function updateArtifactCleanupSummary(value: Record<string, unknown>) {
  artifactCleanupSummary.at = typeof value.at === 'string' ? value.at : new Date().toISOString()
  artifactCleanupSummary.status = typeof value.status === 'string' ? value.status : ''
  artifactCleanupSummary.deletedCount = toNonNegativeNumber(value.deletedCount ?? value.deletedFiles)
  artifactCleanupSummary.skippedCount = toNonNegativeNumber(value.skippedCount)
  artifactCleanupSummary.error = typeof value.error === 'string' ? value.error : ''
}

async function cleanupMinerUArtifacts() {
  try {
    await ElMessageBox.confirm(
      '立即清理会按当前策略删除 MinerU 产物目录中的可清理文件，正在执行的任务不受影响。是否继续？',
      '立即清理 MinerU 产物',
      { type: 'warning', confirmButtonText: '立即清理', cancelButtonText: '取消' },
    )
  } catch {
    return
  }

  saving.value = 'cleanup-mineru-artifacts'
  try {
    // Use the generated binding directly so the UI can consume the current
    // backend result shape (deletedCount/skippedCount) without changing the
    // shared store contract in this view-only task.
    const result = await withTimeout(() => App.CleanupMinerUArtifacts(), '立即清理 MinerU 产物', 15_000)
    const value = (typeof result === 'object' && result !== null ? result : {}) as Record<string, unknown>
    updateArtifactCleanupSummary(value)
    const deletedCount = artifactCleanupSummary.deletedCount
    const skippedCount = artifactCleanupSummary.skippedCount
    const cleanupMessage = `MinerU 产物清理${cleanupStatusLabel.value}，共删除 ${deletedCount} 个文件${skippedCount ? `，跳过 ${skippedCount} 个` : ''}`
    if (artifactCleanupSummary.status.toLowerCase() === 'success') notifySuccess(cleanupMessage)
    else if (artifactCleanupSummary.status.toLowerCase() === 'failed' || artifactCleanupSummary.error) notifyError(cleanupMessage)
    else notifyWarning(cleanupMessage)
    await loadArtifactCleanupSummary()
  } catch (e: unknown) {
    notifyError(errorMessage(e, '立即清理 MinerU 产物失败'))
  } finally {
    saving.value = ''
  }
}

function onMaxKBApiKeyFocus() {
  if (maxkbApiKeyMasked.value) {
    maxkb.apiKey = ''
    maxkbApiKeyMasked.value = false
  }
}

function onMaxKBApiKeyBlur() {
  if (!maxkb.apiKey.trim() && originalMaxKBApiKey.value) {
    maxkbApiKeyMasked.value = true
    maxkb.apiKey = '••••••••••••••••••••••••••••••••••••••'
  }
}

function onMinerUApiKeyFocus() {
  if (mineruApiKeyMasked.value) {
    mineru.apiKey = ''
    mineruApiKeyMasked.value = false
  }
}

function onMinerUApiKeyBlur() {
  if (!mineru.apiKey.trim() && originalMinerUApiKey.value) {
    mineruApiKeyMasked.value = true
    mineru.apiKey = '•••••••••••••••••••••••••••••••'
  }
}
</script>

<template>
  <div class="view-page settings-page">
    <header class="page-header"><div><h1>系统设置</h1><p class="muted settings-description">凭据仅通过系统凭据库管理，连接测试会真实调用对应服务。<ElTooltip placement="bottom-start" effect="light" :show-after="150"><template #content><div class="credentials-tooltip"><strong>凭据安全</strong><p>MaxKB API Key、在线 MinerU Token 和内网网关 Token 不写入 SQLite、日志或导出文件。系统凭据库不可用时不会降级为明文保存。</p></div></template><button type="button" class="settings-help" aria-label="查看凭据安全说明"><CircleHelp :size="15" /></button></ElTooltip></p></div></header>

    <el-tabs v-model="activeTab" class="settings-tabs">
      <el-tab-pane label="MaxKB 配置" name="maxkb">
        <section class="panel settings-panel settings-tab-panel">
          <div class="settings-panel-header"><div><h2>MaxKB 服务</h2><p>同步目标、工作空间和知识库都从这里连接。</p></div><div class="settings-icon"><Server :size="17" /></div></div>
          <el-form class="settings-form" label-position="top">
            <el-form-item label="MaxKB Base URL"><el-input v-model="maxkb.baseUrl" placeholder="https://maxkb.example.com" clearable /></el-form-item>
            <el-form-item label="User Key / API Key"><el-input v-model="maxkb.apiKey" type="password" placeholder="已保存凭据不会完整显示" @focus="onMaxKBApiKeyFocus" @blur="onMaxKBApiKeyBlur" /></el-form-item>
            <div class="settings-actions"><el-button plain :loading="saving === 'test-maxkb'" :disabled="saving !== '' && saving !== 'test-maxkb'" @click="testMaxKB"><PlugZap :size="15" /> 测试连接</el-button><el-button type="primary" :loading="saving === 'maxkb'" :disabled="saving !== '' && saving !== 'maxkb'" @click="saveMaxKB"><Save :size="15" /> 保存配置</el-button></div>
          </el-form>
        </section>
      </el-tab-pane>

      <el-tab-pane label="MinerU 配置" name="mineru">
        <section class="panel settings-panel settings-tab-panel mineru-settings-panel">
          <div class="mineru-global-control">
            <div class="mineru-global-control-copy">
              <div>
                <h2>MinerU 转换服务</h2>
                <p>开启后，可为不支持直接上传的文件使用 MinerU 转换后再同步到 MaxKB。</p>
              </div>
            </div>
            <div class="mineru-global-control-actions">
              <el-switch v-model="mineru.enabled" active-text="已启用" :disabled="saving !== ''" @change="onMinerUEnabledChange" />
            </div>
          </div>

          <div class="mineru-config-body" :class="{ 'is-disabled': !mineru.enabled }">
            <section class="settings-subsection">
              <div class="settings-subsection-header">
                <div>
                  <h3>服务连接</h3>
                  <p>配置 MinerU 的服务模式、地址和访问凭据。</p>
                </div>
                <Server :size="17" />
              </div>
              <div class="settings-form mineru-form">
                <div class="settings-field-row">
                  <div class="settings-field-label">服务模式</div>
                  <div class="settings-field-control">
                    <el-radio-group v-model="mineru.mode" class="mineru-mode-group" :disabled="mineruConfigDisabled">
                      <el-radio value="online">在线 MinerU</el-radio>
                      <el-radio value="internal">内网 MinerU</el-radio>
                    </el-radio-group>
                  </div>
                </div>
                <div class="settings-field-row">
                  <div class="settings-field-label">服务地址</div>
                  <div class="settings-field-control">
                    <el-input v-model="mineru.baseUrl" placeholder="https://mineru.net 或内网网关地址" clearable :disabled="mineruConfigDisabled" />
                  </div>
                </div>
                <div class="settings-field-row">
                  <div class="settings-field-label">访问 Token</div>
                  <div class="settings-field-control">
                    <el-input v-model="mineru.apiKey" type="password" show-password placeholder="已保存凭据不会完整显示" :disabled="mineruConfigDisabled" @focus="onMinerUApiKeyFocus" @blur="onMinerUApiKeyBlur" />
                  </div>
                </div>
                <div class="settings-actions">
                  <el-button plain :loading="saving === 'test-mineru'" :disabled="mineruConfigDisabled" @click="testMinerU"><PlugZap :size="15" /> 测试连接</el-button>
                </div>
              </div>
            </section>

            <section class="settings-subsection artifact-subsection">
              <div class="settings-subsection-header">
                <div>
                  <h3>产物保存与清理</h3>
                  <p>MinerU 返回的原始 ZIP 默认保存，不解压，完成后直接提交 MaxKB。</p>
                </div>
                <FolderOpen :size="17" />
              </div>
              <div class="settings-form mineru-form artifact-form">
                <div class="settings-field-row settings-field-row-top">
                  <div class="settings-field-label required-label">产物保存目录</div>
                  <div class="settings-field-control">
                    <div class="path-field">
                      <el-input v-model="mineruArtifact.resultSaveDir" placeholder="选择用于保存 MinerU 结果 ZIP 的目录" clearable :disabled="mineruConfigDisabled" />
                      <el-button plain type="primary" :loading="saving === 'select-mineru-artifact-dir'" :disabled="mineruConfigDisabled" @click="selectMinerUArtifactDirectory"><FolderOpen :size="15" /> 选择目录</el-button>
                    </div>
                    <span class="form-hint">MinerU 开启时必填；立即清理策略不会在此目录保留 ZIP。</span>
                  </div>
                </div>
                <div class="settings-field-row settings-field-row-top">
                  <div class="settings-field-label">清理策略</div>
                  <div class="settings-field-control cleanup-policy-control">
                    <div class="cleanup-policy-line">
                      <el-radio-group :model-value="artifactCleanupPolicy" class="cleanup-policy-group" :disabled="mineruConfigDisabled" @change="setArtifactCleanupPolicy">
                        <el-radio value="immediate">立即清理</el-radio>
                        <el-radio value="keep_batches">按批次清理</el-radio>
                        <el-radio value="after_duration">按时间清理</el-radio>
                        <el-radio value="never">不自动清理</el-radio>
                      </el-radio-group>
                      <span class="cleanup-policy-hint">选择产物的清理方式，节省存储空间。</span>
                    </div>
                    <span class="form-hint">立即清理：本次同步完成后删除 ZIP；按批次/按时间：按规则自动删除；不自动清理：仅手动清理。</span>
                  </div>
                </div>
                <div v-if="artifactCleanupPolicy === 'after_duration' || artifactCleanupPolicy === 'keep_batches'" class="cleanup-detail-row">
                  <div class="cleanup-detail-field settings-field-row settings-field-row-top">
                    <div v-if="artifactCleanupPolicy === 'after_duration'" class="settings-field-label">保留时长</div>
                    <div v-else class="settings-field-label">保留批次数</div>
                    <div class="settings-field-control">
                      <div v-if="artifactCleanupPolicy === 'after_duration'" class="duration-field">
                        <el-input-number v-model="mineruArtifact.cleanupAfterValue" :min="1" :max="1000000" controls-position="right" :disabled="mineruConfigDisabled" />
                        <el-select v-model="mineruArtifact.cleanupAfterUnit" :disabled="mineruConfigDisabled"><el-option label="小时" value="hour" /><el-option label="天" value="day" /></el-select>
                      </div>
                      <el-input-number v-else v-model="mineruArtifact.cleanupKeepBatches" :min="1" :max="100000" controls-position="right" :disabled="mineruConfigDisabled" />
                      <span v-if="artifactCleanupPolicy === 'after_duration'" class="form-hint">清理超过该时长且不再使用的批次。</span>
                      <span v-else class="form-hint">每个任务保留最近 N 个批次。</span>
                    </div>
                  </div>
                  <div class="cleanup-detail-field settings-field-row settings-field-row-top">
                    <div class="settings-field-label">清理 Cron</div>
                    <div class="settings-field-control">
                      <el-input v-model="mineruArtifact.cleanupCron" placeholder="0 3 * * *" :disabled="mineruConfigDisabled" @blur="validateArtifactCron" />
                      <span class="form-hint">标准 5 段 Cron，按操作系统时区执行。</span>
                      <span v-if="cronError" class="form-hint danger-text">{{ cronError }}</span>
                    </div>
                  </div>
                </div>

                <div v-if="!artifactConfigAvailable" class="form-hint warning-text">当前 app.go 尚未暴露产物配置绑定。界面已就绪；补充现有 Settings 绑定后，保存和清理按钮会自动启用。</div>
                <div v-else-if="!artifactCapabilities.cleanup" class="form-hint warning-text">当前 app.go 已支持读取/保存产物设置，但尚未暴露立即清理方法。</div>
                <div class="artifact-cleanup-summary" aria-live="polite">
                  <div class="artifact-summary-header"><strong>上次清理结果</strong><span v-if="!artifactHasCleanupResult" class="muted">暂无记录</span></div>
                  <div v-if="artifactHasCleanupResult" class="artifact-summary-grid">
                    <div><span>时间</span><strong>{{ formatCleanupTime(artifactCleanupSummary.at) }}</strong></div>
                    <div><span>状态</span><strong :class="cleanupStatusClass">{{ cleanupStatusLabel }}</strong></div>
                    <div><span>删除批次</span><strong>{{ artifactCleanupSummary.deletedCount }}</strong></div>
                    <div v-if="artifactCleanupSummary.skippedCount"><span>跳过批次</span><strong>{{ artifactCleanupSummary.skippedCount }}</strong></div>
                  </div>
                  <p v-if="artifactCleanupSummary.error" class="form-hint danger-text">{{ artifactCleanupSummary.error }}</p>
                </div>
                <div class="settings-actions">
                  <el-button plain type="danger" :loading="saving === 'cleanup-mineru-artifacts'" :disabled="artifactCleanupDisabled" @click="cleanupMinerUArtifacts"><Trash2 :size="15" /> 立即清理</el-button>
                  <el-button type="primary" :loading="saving === 'mineru-config'" :disabled="artifactSaveDisabled" @click="saveMinerUConfiguration"><Save :size="15" /> 保存配置</el-button>
                </div>
              </div>
            </section>
          </div>
        </section>
      </el-tab-pane>
    </el-tabs>

  </div>
</template>

<script lang="ts">
import { CircleHelp, FileCog, FolderOpen, PlugZap, Save, Server, Trash2 } from 'lucide-vue-next'
export default { components: { CircleHelp, FileCog, FolderOpen, PlugZap, Save, Server, Trash2 } }
</script>

<style scoped>
/* Settings are grouped by service while security details stay available from the page description. */
.settings-page { padding-bottom: 48px; }
.settings-description { display: flex; align-items: center; gap: 5px; }
.settings-description :deep(.el-tooltip__trigger) { display: inline-flex; align-items: center; }
.settings-help { display: inline-grid; width: 18px; height: 18px; padding: 0; place-items: center; color: var(--muted); background: transparent; border: 0; border-radius: 50%; transition: color .16s, background .16s; }
.settings-help:hover, .settings-help:focus-visible { color: var(--primary); background: #efeeff; outline: none; }
.credentials-tooltip { max-width: 330px; }
.credentials-tooltip strong { display: block; margin-bottom: 5px; color: var(--text-primary); font-size: 12px; }
.credentials-tooltip p { margin: 0; color: var(--text-secondary); font-size: 11px; line-height: 1.6; }

.settings-tabs { margin-top: -5px; }
.settings-tabs :deep(.el-tabs__header) { margin: 0 0 16px; }
.settings-tabs :deep(.el-tabs__nav-wrap::after) { background-color: var(--border); }
.settings-tabs :deep(.el-tabs__item) { height: 40px; color: var(--text-secondary); font-size: 12px; }
.settings-tabs :deep(.el-tabs__item.is-active) { color: var(--primary); font-weight: 650; }
.settings-tabs :deep(.el-tabs__active-bar) { height: 2px; border-radius: 2px; }
.settings-tab-panel { min-height: 300px; }

.mineru-settings-panel {
  padding: 24px 26px 26px;
  border-radius: 14px;
  box-shadow: 0 8px 24px rgba(15, 23, 42, .045);
}
.mineru-config-body { transition: opacity .16s ease; }
.mineru-config-body.is-disabled { opacity: .72; }
.mineru-config-body.is-disabled .settings-subsection-header { color: var(--muted); }
.mineru-global-control {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 0 0 20px;
  border-bottom: 1px solid var(--border);
}
.mineru-global-control-copy { display: flex; align-items: center; gap: 11px; min-width: 0; }
.mineru-global-control-copy h2 { margin: 0 0 6px; color: var(--text-primary); font-size: 22px; line-height: 1.25; letter-spacing: -.02em; }
.mineru-global-control-copy p { margin: 0; color: var(--text-secondary); font-size: 13px; line-height: 1.5; }
.mineru-global-control-actions { display: flex; align-items: center; justify-content: flex-end; gap: 10px; flex: 0 0 auto; }
.mineru-global-control-actions :deep(.el-switch) { --el-switch-on-color: var(--primary); }
.settings-subsection { padding-top: 21px; }
.settings-subsection + .settings-subsection { margin-top: 21px; padding-top: 21px; border-top: 1px solid var(--border); }
.settings-subsection-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 14px; color: var(--primary); }
.settings-subsection-header h3 { margin: 0 0 5px; color: var(--text-primary); font-size: 16px; line-height: 1.35; }
.settings-subsection-header p { margin: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.5; }
.settings-subsection-header > svg { flex: 0 0 auto; margin-top: 3px; color: var(--primary); }
.settings-subsection .settings-form { padding-top: 19px; }
.mineru-form { gap: 16px; }
.settings-field-row { display: grid; grid-template-columns: 166px minmax(0, 1fr); gap: 24px; align-items: center; min-width: 0; }
.settings-field-row-top { align-items: start; }
.settings-field-label { position: relative; padding-top: 10px; color: var(--text-primary); font-size: 13px; font-weight: 500; line-height: 1.35; }
.settings-field-label.required-label::before { content: '*'; position: absolute; top: 10px; left: -14px; color: var(--danger); }
.settings-field-control { min-width: 0; width: 100%; }
.settings-field-control > .el-input,
.settings-field-control > .el-select,
.settings-field-control > .el-input-number { width: 100%; }
.settings-field-control .form-hint { display: block; margin-top: 7px; }
.mineru-mode-group { display: flex; align-items: center; gap: 24px; width: 100%; flex-wrap: wrap; }
.mineru-mode-group :deep(.el-radio) { margin-right: 0; color: var(--text-secondary); }
.mineru-mode-group :deep(.el-radio__label) { padding-left: 7px; font-size: 13px; }
.settings-actions { display: flex; justify-content: flex-end; gap: 8px; padding-top: 3px; }
.artifact-form > .settings-actions { margin-top: 2px; padding-top: 18px; border-top: 1px solid var(--border); }
.path-field { display: flex; align-items: center; gap: 10px; width: 100%; }
.path-field .el-input { flex: 1; min-width: 0; }
.path-field > .el-button { flex: 0 0 auto; min-width: 132px; }
.cleanup-policy-line { display: flex; align-items: center; gap: 16px; min-width: 0; }
.cleanup-policy-group { display: flex; align-items: center; gap: 22px; flex: 0 0 auto; flex-wrap: wrap; }
.cleanup-policy-group :deep(.el-radio) { margin-right: 0; color: var(--text-secondary); }
.cleanup-policy-group :deep(.el-radio__label) { padding-left: 7px; font-size: 13px; }
.cleanup-policy-hint { min-width: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.5; }
.cleanup-detail-row { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 24px; min-width: 0; }
.cleanup-detail-field { grid-template-columns: 112px minmax(0, 1fr); gap: 14px; min-width: 0; }
.cleanup-detail-field .settings-field-label { white-space: nowrap; }
.duration-field { display: grid; grid-template-columns: minmax(0, 1fr) 112px; gap: 10px; width: 100%; }
.duration-field .el-input-number,
.duration-field .el-select { width: 100%; }
.artifact-cleanup-summary { margin-top: 1px; padding: 13px 14px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface-muted); }
.artifact-summary-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; color: var(--text-primary); font-size: 12px; }
.artifact-summary-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; margin-top: 10px; }
.artifact-summary-grid div { min-width: 0; }
.artifact-summary-grid span { display: block; color: var(--muted); font-size: 11px; }
.artifact-summary-grid strong { display: block; margin-top: 3px; color: var(--text-primary); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.status-success { color: var(--success) !important; }
.status-warning { color: var(--warning) !important; }
.status-danger { color: var(--danger) !important; }
@media (max-width: 900px) {
  .mineru-settings-panel { padding: 20px; }
  .settings-field-row { grid-template-columns: 128px minmax(0, 1fr); gap: 16px; }
  .cleanup-detail-row { gap: 16px; }
  .cleanup-detail-field { grid-template-columns: 96px minmax(0, 1fr); gap: 12px; }
  .cleanup-policy-line { align-items: flex-start; flex-direction: column; gap: 8px; }
  .cleanup-policy-group { width: 100%; gap: 14px 22px; }
  .artifact-summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 640px) {
  .mineru-global-control { align-items: flex-start; flex-direction: column; }
  .mineru-global-control-actions { justify-content: flex-start; }
  .settings-field-row { grid-template-columns: 1fr; gap: 7px; }
  .cleanup-detail-row { grid-template-columns: 1fr; gap: 16px; }
  .cleanup-detail-field { grid-template-columns: 1fr; gap: 7px; }
  .settings-field-label { padding-top: 0; }
  .settings-field-label.required-label::before { top: 0; left: -14px; }
  .path-field { align-items: stretch; flex-direction: column; }
  .path-field > .el-button { width: 100%; }
  .cleanup-policy-group { gap: 12px 18px; }
  .artifact-summary-grid { grid-template-columns: 1fr; }
}
</style>
