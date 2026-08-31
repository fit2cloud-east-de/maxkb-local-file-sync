<script lang="ts" setup>
import { computed, ref, watch, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useFoldersStore } from '../stores/folders'
import { useTasksStore } from '../stores/tasks'
import { useConfigStore } from '../stores/config'
import FolderCard from '../components/FolderCard.vue'
import type { CreateFolderRequest, WorkspaceDTO, KnowledgeBaseDTO, EmbeddingModelDTO, CreateKnowledgeBaseDTO, KnowledgeFolderDTO, PreviewMatchResult } from '../types'
import * as App from '../../wailsjs/go/main/App'
import { errorMessage, isNoPendingChangesError, withTimeout } from '../stores/store-helpers'
import { ElMessageBox } from 'element-plus'
import { notifyError, notifySuccess, notifyWarning } from '../utils/notify'

const router = useRouter()
const foldersStore = useFoldersStore()
const tasksStore = useTasksStore()
const configStore = useConfigStore()

const showModal = ref(false)
const showKbModal = ref(false)
const editingId = ref<string | null>(null)
const modalError = ref('')
const kbModalError = ref('')
const saving = ref(false)
const syncingId = ref<string | null>(null)

// 工作空间 / 知识库级联数据
const workspaces = ref<WorkspaceDTO[]>([])
const knowledgeBases = ref<KnowledgeBaseDTO[]>([])
const knowledgeFolders = ref<KnowledgeFolderDTO[]>([])
const embeddingModels = ref<EmbeddingModelDTO[]>([])
const loadingWs = ref(false)
const loadingKb = ref(false)
const loadingFolders = ref(false)
const loadingModels = ref(false)
const wsError = ref('')
const kbError = ref('')
const folderError = ref('')
const PAGE_CALL_TIMEOUT_MS = 15_000
const SCAN_CALL_TIMEOUT_MS = 60_000

const form = ref<CreateFolderRequest>({
  name: '', localPath: '', kbId: '', workspaceId: '', knowledgeFolderId: '', workspaceName: '', knowledgeName: '', maxkbBaseUrlSnapshot: '',
  enableMinerU: false,
  cronExpression: '0 * * * *', cronEnabled: false,
  syncDeleteLocalRemoved: false,
  mineruRetryCount: 3,
  mineruRequestTimeout: 60000,
  mineruTaskTimeout: 300000,
  mineruPollInterval: 2000,
  mineruSaveFullResult: false,
  mineruResultSaveDir: '',
  includePatterns: '',
  excludePatterns: '',
  mineruFileExtensions: ''
})

// 新建知识库表单
const kbForm = ref({
  folderId: '',
  name: '',
  description: '',
  embeddingModelId: ''
})

const cronError = ref('')
const showPreview = ref(false)
const previewLoading = ref(false)
const previewResult = ref<PreviewMatchResult | null>(null)
const previewError = ref('')
// 预览窗口必须和当前任务开关保持一致。即使旧版后端返回了历史
// MinerU 分类，关闭开关后也不能继续显示 MinerU 标签或计数。
const previewMineruFiles = computed(() => form.value.enableMinerU ? (previewResult.value?.mineruFiles ?? []) : [])

const mineruModeLabel = computed(() => configStore.minerUConfig.mode === 'internal' ? '内网 MinerU' : '在线 MinerU')
const mineruConfigHint = computed(() => configStore.minerUConfig.enabled
  ? `服务模式、服务地址和访问凭据统一使用系统设置（当前为${mineruModeLabel.value}）。`
  : '服务模式、服务地址和访问凭据统一使用系统设置；当前 MinerU 未启用。')

onMounted(() => {
  // App.vue 负责首屏初始化。这里仅在确实没有首屏请求时补一次，避免
  // Wails 重启/热更新时同一接口被两个请求同时占用，导致列表长期停留在 skeleton。
  const requests: Promise<unknown>[] = []
  if (!foldersStore.loading && foldersStore.folders.length === 0 && !foldersStore.error) {
    requests.push(foldersStore.fetchFolders())
  }
  if (!configStore.loading && !configStore.maxKBConfig.baseUrl && !configStore.minerUConfig.enabled && !configStore.error) {
    requests.push(configStore.loadConfigs())
  }
  if (requests.length) void Promise.allSettled(requests)
})

// 打开原生文件夹选择器
async function selectDirectory() {
  try {
    // 系统目录选择器可能需要用户停留较久，不能使用后端 RPC 的短超时。
    const path = await Promise.resolve().then(() => App.SelectDirectory())
    if (path) form.value.localPath = path
  } catch (e: any) {
    modalError.value = '选择目录失败: ' + errorMessage(e, '本地服务不可用')
  }
}

// 加载工作空间列表
async function loadWorkspaces() {
  loadingWs.value = true
  wsError.value = ''
  try {
    const result = await withTimeout(() => App.ListWorkspaces(), '读取工作空间', PAGE_CALL_TIMEOUT_MS)
    workspaces.value = (result ?? []) as WorkspaceDTO[]
  } catch (e: any) {
    wsError.value = errorMessage(e, '读取工作空间失败')
    workspaces.value = []
  } finally {
    loadingWs.value = false
  }
}

// 加载指定工作空间下的知识库目录树
async function loadKnowledgeFolders(workspaceId: string) {
  if (!workspaceId) { knowledgeFolders.value = []; return }
  loadingFolders.value = true
  folderError.value = ''
  try {
    knowledgeFolders.value = (await withTimeout(() => App.ListKnowledgeFolders(workspaceId), '读取知识库目录', PAGE_CALL_TIMEOUT_MS) ?? []) as KnowledgeFolderDTO[]
  } catch (e: any) {
    folderError.value = errorMessage(e, '读取知识库目录失败')
    knowledgeFolders.value = []
  } finally {
    loadingFolders.value = false
  }
}

function flattenKnowledgeFolders(items: KnowledgeFolderDTO[], depth = 0): Array<{ id: string; label: string }> {
  const result: Array<{ id: string; label: string }> = []
  for (const item of items) {
    result.push({ id: item.id, label: `${'　'.repeat(depth)}${item.name}` })
    if (item.children?.length) result.push(...flattenKnowledgeFolders(item.children, depth + 1))
  }
  return result
}

function applyKnowledgeBaseSelection(kbId: string) {
  const kb = knowledgeBases.value.find(item => item.id === kbId)
  if (!kb) { form.value.knowledgeFolderId = ''; form.value.knowledgeName = ''; return }
  form.value.knowledgeFolderId = kb.folderId || ''
  form.value.knowledgeName = kb.name
}

// 加载指定工作空间下的知识库
async function loadKnowledgeBases(workspaceId: string) {
  if (!workspaceId) {
    knowledgeBases.value = []
    knowledgeFolders.value = []
    form.value.kbId = ''
    form.value.knowledgeFolderId = ''
    form.value.knowledgeName = ''
    return
  }
  loadingKb.value = true
  kbError.value = ''
  try {
    const result = await withTimeout(() => App.ListKnowledgeBases(workspaceId), '读取知识库列表', PAGE_CALL_TIMEOUT_MS)
    knowledgeBases.value = (result ?? []) as KnowledgeBaseDTO[]
    // 若当前 kbId 不在新列表里则清空
    if (!knowledgeBases.value.find(kb => kb.id === form.value.kbId)) {
      form.value.kbId = ''
      form.value.knowledgeFolderId = ''
      form.value.knowledgeName = ''
    } else {
      applyKnowledgeBaseSelection(form.value.kbId)
    }
  } catch (e: any) {
    kbError.value = errorMessage(e, '读取知识库列表失败')
    knowledgeBases.value = []
    form.value.kbId = ''
    form.value.knowledgeFolderId = ''
    form.value.knowledgeName = ''
  } finally {
    loadingKb.value = false
  }
}

// 工作空间变化时自动加载知识库
watch(() => form.value.workspaceId, async (newId) => {
  const ws = workspaces.value.find(item => item.id === newId)
  form.value.workspaceName = ws?.name || ''
  // 先加载目录树，再加载知识库，避免级联请求使用尚未解析的工作空间目录。
  await loadKnowledgeFolders(newId)
  await loadKnowledgeBases(newId)
})

watch(() => form.value.kbId, (newId) => applyKnowledgeBaseSelection(newId))

function openCreate() {
  editingId.value = null
  form.value = {
    name: '', localPath: '', kbId: '', workspaceId: '', knowledgeFolderId: '', workspaceName: '', knowledgeName: '', maxkbBaseUrlSnapshot: '',
    enableMinerU: false,
    cronExpression: '0 * * * *', cronEnabled: false,
    syncDeleteLocalRemoved: false,
    mineruRetryCount: 3,
    mineruRequestTimeout: 60000,
    mineruTaskTimeout: 300000,
    mineruPollInterval: 2000,
    mineruSaveFullResult: false,
    mineruResultSaveDir: '',
    includePatterns: '',
    excludePatterns: '',
    mineruFileExtensions: ''
  }
  modalError.value = ''
  knowledgeBases.value = []
  knowledgeFolders.value = []
  showModal.value = true
  loadWorkspaces()
}

function openEdit(folderId: string) {
  const f = foldersStore.folders.find(x => x.folderId === folderId)
  if (!f) return
  editingId.value = folderId
  form.value = {
    name: f.name, localPath: f.localPath, kbId: f.kbId,
    workspaceId: f.workspaceId, knowledgeFolderId: f.knowledgeFolderId || '', workspaceName: f.workspaceName || '', knowledgeName: f.knowledgeName || '', maxkbBaseUrlSnapshot: f.maxkbBaseUrlSnapshot || '', enableMinerU: f.enableMinerU,
    cronExpression: f.cronExpression, cronEnabled: f.cronEnabled,
    syncDeleteLocalRemoved: f.syncDeleteLocalRemoved || false,
    mineruRetryCount: f.mineruRetryCount || 3,
    mineruRequestTimeout: f.mineruRequestTimeout || 60000,
    mineruTaskTimeout: f.mineruTaskTimeout || 300000,
    mineruPollInterval: f.mineruPollInterval || 2000,
    mineruSaveFullResult: f.mineruSaveFullResult || false,
    mineruResultSaveDir: f.mineruResultSaveDir || '',
    includePatterns: f.includePatterns || '',
    excludePatterns: f.excludePatterns || '',
    mineruFileExtensions: f.mineruFileExtensions || ''
  }
  modalError.value = ''
  knowledgeBases.value = []
  showModal.value = true
  loadWorkspaces().then(() => {
    if (f.workspaceId) { loadKnowledgeBases(f.workspaceId); loadKnowledgeFolders(f.workspaceId) }
  })
}

async function validateCron() {
  if (!form.value.cronEnabled || !form.value.cronExpression) {
    cronError.value = ''
    return true
  }
  try {
    await withTimeout(() => App.ValidateCronExpression(form.value.cronExpression), '校验 Cron 表达式', PAGE_CALL_TIMEOUT_MS)
    cronError.value = ''
    return true
  } catch (e: any) {
    cronError.value = errorMessage(e, 'Cron 表达式无效')
    return false
  }
}

function validateRequiredFields() {
  if (!form.value.name.trim()) {
    modalError.value = '请输入任务名称'
    return false
  }
  if (!form.value.localPath.trim()) {
    modalError.value = '请选择本地文件夹'
    return false
  }
  if (!form.value.workspaceId.trim()) {
    modalError.value = '请选择目标工作区'
    return false
  }
  if (!form.value.kbId.trim()) {
    modalError.value = '请选择知识库'
    return false
  }
  return true
}

async function saveFolder() {
  modalError.value = ''
  if (!validateRequiredFields()) return
  if (!(await validateCron())) return
  saving.value = true
  modalError.value = ''
  try {
    if (editingId.value) {
      await foldersStore.updateFolder(editingId.value, form.value)
    } else {
      await foldersStore.createFolder(form.value)
    }
    showModal.value = false
    await foldersStore.fetchFolders()
  } catch (e: any) {
    modalError.value = e?.message ?? String(e)
  } finally {
    saving.value = false
  }
}

async function deleteFolder(folderId: string) {
  try {
    await foldersStore.deleteFolder(folderId)
    notifySuccess('同步任务已删除')
  } catch (e: any) {
    notifyError('删除失败：' + errorMessage(e, '本地服务不可用'))
  }
}

async function syncFolder(folderId: string) {
  syncingId.value = folderId
  try {
    const result = await foldersStore.scanFolder(folderId)
    await tasksStore.createTask(folderId, 'manual')

    const renamedCount = Object.keys(result.renamedFiles || {}).length
    const parts = [
      result.newFiles.length > 0 ? `+${result.newFiles.length} 新增` : '',
      result.updatedFiles.length > 0 ? `~${result.updatedFiles.length} 更新` : '',
      renamedCount > 0 ? `↻${renamedCount} 重命名` : '',
      result.deletedFiles.length > 0 ? `-${result.deletedFiles.length} 删除` : ''
    ].filter(p => p)
    notifySuccess(`扫描完成：${parts.join('，') || '无变化'}，批次已加入全局队列。`)
    await foldersStore.fetchFolders()
  } catch (e: any) {
    if (isNoPendingChangesError(e)) {
      notifyWarning('该目录下文件无变化，无需同步')
    } else {
      notifyError('同步出错：' + errorMessage(e, '本地服务不可用'))
    }
  } finally {
    syncingId.value = null
  }
}

function viewFiles(folderId: string) {
  router.push({ name: 'FolderFiles', params: { folderId } })
}

// 打开新建知识库对话框
function openCreateKb() {
  kbForm.value = { folderId: '', name: '', description: '', embeddingModelId: '' }
  kbModalError.value = ''
  embeddingModels.value = []
  showKbModal.value = true
  loadEmbeddingModels()
  loadKnowledgeFolders(form.value.workspaceId)
}

// 加载向量模型列表
async function loadEmbeddingModels() {
  if (!form.value.workspaceId) return
  loadingModels.value = true
  try {
    const result = await withTimeout(() => App.ListEmbeddingModels(form.value.workspaceId), '读取向量模型', PAGE_CALL_TIMEOUT_MS)
    embeddingModels.value = (result ?? []) as EmbeddingModelDTO[]
    // 自动选择第一个模型
    if (embeddingModels.value.length > 0 && !kbForm.value.embeddingModelId) {
      kbForm.value.embeddingModelId = embeddingModels.value[0].id
    }
  } catch (e: any) {
    kbModalError.value = '加载向量模型失败: ' + errorMessage(e, '本地服务不可用')
    embeddingModels.value = []
  } finally {
    loadingModels.value = false
  }
}

// 创建知识库
async function createKnowledgeBase() {
  if (!kbForm.value.folderId) {
    kbModalError.value = '请选择知识库目录'
    return
  }
  if (!kbForm.value.name.trim()) {
    kbModalError.value = '请输入知识库名称'
    return
  }
  if (!kbForm.value.embeddingModelId) {
    kbModalError.value = '请选择向量模型'
    return
  }

  saving.value = true
  kbModalError.value = ''
  try {
    const result = await withTimeout(() => App.CreateKnowledgeBase({
      workspaceId: form.value.workspaceId,
      folderId: kbForm.value.folderId,
      name: kbForm.value.name,
      description: kbForm.value.description,
      embeddingModelId: kbForm.value.embeddingModelId
    }), '创建知识库', PAGE_CALL_TIMEOUT_MS)
    // 创建成功后重新加载知识库列表并选中新创建的
    await loadKnowledgeBases(form.value.workspaceId)
    form.value.kbId = result.id
    form.value.knowledgeFolderId = result.folderId || kbForm.value.folderId
    form.value.knowledgeName = result.name
    showKbModal.value = false
  } catch (e: any) {
    kbModalError.value = errorMessage(e, '创建知识库失败')
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(folderId: string, currentEnabled: boolean) {
  const action = currentEnabled ? '关闭' : '启用'

  if (!currentEnabled) {
    // 启用确认
    try {
      await ElMessageBox.confirm(
        '启用后将恢复定时调度并重新计算下次执行时间，不会补跑关闭期间错过的调度。',
        '启用同步任务',
        { type: 'warning', confirmButtonText: '确认启用', cancelButtonText: '取消' },
      )
    } catch {
      return
    }
    try {
      await withTimeout(() => App.EnableTask(folderId), '启用同步任务', PAGE_CALL_TIMEOUT_MS)
      await foldersStore.fetchFolders()
      notifySuccess('同步任务已启用')
    } catch (e: any) {
      notifyError(`启用失败：${errorMessage(e, '本地服务不可用')}`)
    }
  } else {
    // 关闭确认
    try {
      await ElMessageBox.confirm(
        '关闭后将停止定时调度，并取消尚未执行的排队任务。当前正在执行的同步将继续完成，已暂停批次保持暂停。该操作不会删除 MaxKB 中的文档。',
        '关闭同步任务',
        { type: 'warning', confirmButtonText: '确认关闭', cancelButtonText: '取消' },
      )
    } catch {
      return
    }

    try {
      await withTimeout(() => App.DisableTask(folderId), '关闭同步任务', PAGE_CALL_TIMEOUT_MS)
      await foldersStore.fetchFolders()
      notifySuccess('同步任务已关闭')
    } catch (e: any) {
      notifyError(`关闭失败：${errorMessage(e, '本地服务不可用')}`)
    }
  }
}

function exclusionReason(file: string): string {
  const reason = previewResult.value?.exclusionReasons?.[file]
  if (reason === 'excluded_by_exclude_pattern') return '命中 Exclude 规则'
  if (reason === 'not_matched_by_include_pattern') return '未命中 Include 规则'
  if (reason === 'unsupported_by_maxkb') return 'MaxKB 不支持直接上传，且未启用对应的 MinerU 转换'
  return reason ?? '未匹配筛选规则'
}

async function previewFileMatch() {
  if (!form.value.localPath) {
    previewError.value = '请先选择本地路径'
    return
  }

  previewLoading.value = true
  previewError.value = ''
  previewResult.value = null

  try {
    const result = await withTimeout(() => App.PreviewMatch({
      localPath: form.value.localPath,
      includePatterns: form.value.includePatterns,
      excludePatterns: form.value.excludePatterns,
      enableMinerU: form.value.enableMinerU,
      mineruFileExtensions: form.value.mineruFileExtensions
    }), '预览文件匹配结果', SCAN_CALL_TIMEOUT_MS)
    previewResult.value = result
    showPreview.value = true
  } catch (e: any) {
    previewError.value = errorMessage(e, '预览文件匹配失败')
  } finally {
    previewLoading.value = false
  }
}

</script>
<template>
  <div class="view-page">
    <header class="page-header">
      <div><p class="eyebrow">工作区</p><h1>同步任务</h1><p class="muted">选择本地文件夹，递归、增量地同步到指定 MaxKB 知识库。</p></div>
      <el-button type="primary" @click="openCreate"><FolderPlus :size="16" /> 新建</el-button>
    </header>

    <div v-if="foldersStore.loading && foldersStore.folders.length === 0" class="loading"><el-skeleton :rows="4" animated /></div>
    <div v-else-if="foldersStore.error" class="error-msg">{{ foldersStore.error }}</div>
    <div v-else-if="foldersStore.folders.length === 0" class="empty-state"><FolderOpen :size="34" /><h3>还没有同步任务</h3><p>创建第一个任务，开始把本地资料同步到 MaxKB。</p><el-button type="primary" @click="openCreate"><FolderPlus :size="15" /> 创建同步任务</el-button></div>
    <div v-else class="folders-grid">
      <FolderCard v-for="folder in foldersStore.folders" :key="folder.folderId" :folder="folder" :busy="syncingId === folder.folderId" @sync="syncFolder" @files="viewFiles" @edit="openEdit" @delete="deleteFolder" @toggle-enabled="toggleEnabled" />
    </div>

    <el-dialog v-model="showModal" :title="editingId ? '编辑同步任务' : '新建'" width="720px" destroy-on-close>
      <div v-if="modalError" class="notice warning modal-error"><AlertTriangle :size="16" /> {{ modalError }}</div>
      <form class="folder-modal-form" @submit.prevent="saveFolder">
        <section class="form-section"><h3 class="form-section-title">基础信息</h3><div class="form-grid"><div><div class="field-label">任务名称</div><el-input v-model="form.name" placeholder="例如：产品文档同步" required /></div><div><div class="field-label"><span>本地文件夹 <b class="required-mark" aria-hidden="true">*</b></span></div><div class="path-field"><el-input v-model="form.localPath" placeholder="选择本地目录" readonly required :aria-required="true" /><el-button plain type="primary" @click="selectDirectory">选择目录</el-button></div></div><div><div class="field-label"><span>目标工作区 <b class="required-mark" aria-hidden="true">*</b></span><small v-if="loadingWs">加载中…</small></div><el-select v-model="form.workspaceId" placeholder="选择目标工作区" filterable :loading="loadingWs" :aria-required="true" style="width:100%"><el-option v-for="ws in workspaces" :key="ws.id" :label="ws.name" :value="ws.id" /></el-select><span v-if="wsError" class="form-hint danger-text">{{ wsError }}</span></div><div><div class="field-label"><span>知识库 <b class="required-mark" aria-hidden="true">*</b></span><small v-if="loadingKb">加载中…</small></div><div class="path-field"><el-select v-model="form.kbId" placeholder="选择知识库" filterable :loading="loadingKb" :disabled="!form.workspaceId" :aria-required="true" style="width:100%"><el-option v-for="kb in knowledgeBases" :key="kb.id" :label="kb.name" :value="kb.id" /></el-select><el-button plain :disabled="!form.workspaceId" @click="openCreateKb">新建</el-button></div><span v-if="kbError" class="form-hint danger-text">{{ kbError }}</span><span v-if="form.knowledgeFolderId" class="form-hint">目录 ID：{{ form.knowledgeFolderId }}</span></div></div></section>
        <section class="form-section"><h3 class="form-section-title">调度与删除策略</h3><div class="form-grid"><div><div class="field-label">定时同步</div><el-switch v-model="form.cronEnabled" active-text="启用 Cron" /></div><div v-if="form.cronEnabled"><div class="field-label">Cron 表达式 <small>标准 5 段格式</small></div><el-input v-model="form.cronExpression" placeholder="0 * * * *" @blur="validateCron" /><span v-if="cronError" class="form-hint danger-text">{{ cronError }}</span></div><div class="wide"><el-checkbox v-model="form.syncDeleteLocalRemoved">同步删除本地已删除文件</el-checkbox><span class="form-hint">关闭时远端文档保留，本地文件重新出现且指纹相同不会重复上传。</span></div></div></section>
        <section class="form-section"><h3 class="form-section-title">文件筛选</h3><div class="form-grid"><div><div class="field-label">Include 正则 <small>每行一个，留空表示全部</small></div><el-input v-model="form.includePatterns" type="textarea" :rows="3" placeholder="^docs/&#10;\\.md$" /></div><div><div class="field-label">Exclude 正则 <small>排除优先级更高</small></div><el-input v-model="form.excludePatterns" type="textarea" :rows="3" placeholder="^tmp/&#10;\\.log$" /></div><div class="wide"><el-button plain @click="previewFileMatch" :loading="previewLoading" :disabled="!form.localPath"><Search :size="15" /> 预览匹配结果</el-button><span v-if="previewError" class="form-hint danger-text">{{ previewError }}</span></div></div></section>
        <section class="form-section"><div class="settings-inline-title"><div><h3 class="form-section-title">MinerU 文档转换</h3><p class="form-hint">对选定扩展名先通过 MinerU 转换，并将结果 ZIP 直接提交 MaxKB。</p></div><el-switch v-model="form.enableMinerU" /></div><div v-if="form.enableMinerU" class="mineru-config"><div class="form-hint">{{ mineruConfigHint }} MaxKB 支持 TXT、Markdown、PDF、DOCX、HTML、XLS、XLSX、CSV、ZIP 直接上传；其他格式可先交给 MinerU 转换。</div><div class="mineru-fields-grid"><div><div class="field-label">MinerU 转换范围 <small>逗号分隔</small></div><el-input v-model="form.mineruFileExtensions" placeholder="例如：.pptx, .png, .doc" /><span class="form-hint">留空时，原生格式直接上传，其他格式自动显示为 MinerU；填写后仅转换命中的扩展名。</span></div><div><div class="field-label">失败重试次数</div><el-input-number v-model="form.mineruRetryCount" :min="0" :max="10" controls-position="right" /></div><div><div class="field-label">轮询间隔（毫秒）</div><el-input-number v-model="form.mineruPollInterval" :min="500" :max="60000" :step="500" controls-position="right" /></div></div></div></section>
        <div class="dialog-footer-actions"><el-button @click="showModal = false">取消</el-button><el-button type="primary" native-type="submit" :loading="saving">{{ editingId ? '保存修改' : '创建任务' }}</el-button></div>
      </form>
    </el-dialog>

    <el-dialog v-model="showKbModal" title="新建知识库" width="500px" destroy-on-close>
      <div v-if="kbModalError" class="notice warning modal-error"><AlertTriangle :size="16" /> {{ kbModalError }}</div>
      <el-form label-position="top"><el-form-item label="知识库目录"><el-select v-model="kbForm.folderId" placeholder="选择知识库目录" :loading="loadingFolders" style="width:100%"><el-option v-for="folder in flattenKnowledgeFolders(knowledgeFolders)" :key="folder.id" :label="folder.label" :value="folder.id" /></el-select><span v-if="folderError" class="form-hint danger-text">{{ folderError }}</span></el-form-item><el-form-item label="知识库名称"><el-input v-model="kbForm.name" placeholder="请输入知识库名称" /></el-form-item><el-form-item label="描述"><el-input v-model="kbForm.description" type="textarea" :rows="3" placeholder="可选" /></el-form-item><el-form-item label="向量模型"><el-select v-model="kbForm.embeddingModelId" placeholder="选择可用的 Embedding 模型" :loading="loadingModels" style="width:100%"><el-option v-for="model in embeddingModels" :key="model.id" :label="`${model.name} · ${model.provider}`" :value="model.id" /></el-select></el-form-item></el-form>
      <template #footer><div class="dialog-footer-actions"><el-button @click="showKbModal = false">取消</el-button><el-button type="primary" :loading="saving" @click="createKnowledgeBase">创建知识库</el-button></div></template>
    </el-dialog>

    <el-dialog v-model="showPreview" title="文件匹配预览" width="720px"><div v-if="previewResult" class="preview-content"><div class="queue-summary"><div class="summary-card"><div><span>扫描文件</span><strong>{{ previewResult.totalFiles }}</strong></div></div><div class="summary-card"><div><span>将同步</span><strong class="success-text">{{ previewResult.matchedFiles.length }}</strong></div></div><div class="summary-card"><div><span>将排除</span><strong class="warning-text">{{ previewResult.excludedFiles.length }}</strong></div></div><div class="summary-card"><div><span>MinerU</span><strong>{{ previewMineruFiles.length }}</strong></div></div></div><div class="preview-lists"><details open class="file-list-section"><summary class="list-title">匹配文件（{{ previewResult.matchedFiles.length }}）</summary><div class="file-list"><div v-for="file in previewResult.matchedFiles.slice(0,100)" :key="file" class="file-item"><FileText :size="14" /><span>{{ file }}</span><el-tag v-if="previewMineruFiles.includes(file)" size="small" type="warning">MinerU</el-tag></div><div v-if="previewResult.matchedFiles.length > 100" class="form-hint">仅显示前 100 个文件</div></div></details><details class="file-list-section"><summary class="list-title">排除文件（{{ previewResult.excludedFiles.length }}）</summary><div class="file-list"><div v-for="file in previewResult.excludedFiles.slice(0,100)" :key="file" class="file-item"><CircleMinus :size="14" /><span>{{ file }}</span><el-tag size="small" type="info">{{ exclusionReason(file) }}</el-tag></div></div></details></div></div></el-dialog>
  </div>
</template>

<script lang="ts">
import { AlertTriangle, CircleMinus, FileText, FolderOpen, FolderPlus, Search } from 'lucide-vue-next'
export default { components: { AlertTriangle, CircleMinus, FileText, FolderOpen, FolderPlus, Search } }
</script>

<style scoped>
.empty-state { display: flex; flex-direction: column; align-items: center; gap: 10px; }
.empty-state h3 { margin: 4px 0 0; font-size: 15px; }
.empty-state p { margin: 0 0 5px; color: var(--muted); font-size: 12px; }
.settings-inline-title { display:flex; align-items:flex-start; justify-content:space-between; gap:16px; }
.settings-inline-title .form-section-title { margin-bottom: 4px; }
.preview-content { max-height: 62vh; overflow: auto; }
.preview-lists { display: flex; flex-direction: column; gap: 10px; }
.file-list-section { overflow: hidden; border: 1px solid var(--border); border-radius: 8px; }
.list-title { padding: 10px 12px; color: var(--text-secondary); background: var(--surface-muted); cursor: pointer; font-size: 12px; }
.file-list { display: flex; flex-direction: column; gap: 3px; max-height: 280px; overflow: auto; padding: 8px; }
.file-item { display: flex; align-items: center; gap: 8px; padding: 6px; color: var(--text-secondary); border-radius: 5px; font-size: 11px; }
.file-item:hover { background: #f6f7fb; }
.file-item span { flex: 1; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-all; }
</style>
