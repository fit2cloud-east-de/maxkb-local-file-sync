<script lang="ts" setup>
import { CalendarClock, Database, FileText, FolderOpen, MoreHorizontal, Pencil, Play, Trash2 } from 'lucide-vue-next'
import { ElMessageBox, ElSwitch, ElTag, ElTooltip } from 'element-plus'
import type { FolderDTO } from '../types'

const props = defineProps<{ folder: FolderDTO; busy?: boolean }>()
const emit = defineEmits<{ (e: 'sync', folderId: string): void; (e: 'files', folderId: string): void; (e: 'edit', folderId: string): void; (e: 'delete', folderId: string): void; (e: 'toggle-enabled', folderId: string, currentEnabled: boolean): void }>()
function formatNextExecution(value?: string) { if (!value) return '未设置'; const date = new Date(value); return Number.isNaN(date.getTime()) ? '未设置' : date.toLocaleString([], { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) }
async function confirmDelete() { try { await ElMessageBox.confirm('只删除客户端本地任务、队列、映射和日志，不会删除 MaxKB 中的文档。', '删除同步任务', { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' }); emit('delete', props.folder.folderId) } catch { /* cancelled */ } }
</script>

<template>
  <article class="task-config-card" :class="{ disabled: !folder.enabled }">
    <div class="task-config-head">
      <div class="task-config-title"><div class="icon-tile purple"><FolderOpen :size="18" /></div><div><h3>{{ folder.name }}</h3><p>{{ folder.enabled ? '定时同步已启用' : '定时同步已关闭' }}</p></div></div>
      <div class="task-config-actions"><el-tooltip :content="folder.enabled ? '关闭定时调度' : '启用定时调度'"><el-switch :model-value="folder.enabled" @change="emit('toggle-enabled', folder.folderId, folder.enabled)" /></el-tooltip><el-tooltip content="更多操作"><el-dropdown trigger="click"><el-button text circle><MoreHorizontal :size="18" /></el-button><template #dropdown><el-dropdown-menu><el-dropdown-item @click="emit('edit', folder.folderId)"><Pencil :size="14" /> 编辑任务</el-dropdown-item><el-dropdown-item divided class="danger-item" @click="confirmDelete"><Trash2 :size="14" /> 删除任务</el-dropdown-item></el-dropdown-menu></template></el-dropdown></el-tooltip></div>
    </div>
    <div class="task-config-path" :title="folder.localPath"><FolderOpen :size="15" /> <span>{{ folder.localPath }}</span></div>
    <div class="task-destination">
      <div>
        <span class="meta-label">目标工作区</span>
        <el-tooltip v-if="folder.workspaceId" :content="`ID：${folder.workspaceId}`" placement="top" :show-after="150">
          <strong class="destination-value">{{ folder.workspaceName || folder.workspaceId }}</strong>
        </el-tooltip>
        <strong v-else class="destination-value">未配置</strong>
      </div>
      <Database :size="16" />
      <div>
        <span class="meta-label">知识库</span>
        <el-tooltip v-if="folder.kbId" :content="`ID：${folder.kbId}`" placement="top" :show-after="150">
          <strong class="destination-value">{{ folder.knowledgeName || folder.kbId }}</strong>
        </el-tooltip>
        <strong v-else class="destination-value">未配置</strong>
      </div>
    </div>
    <div class="task-config-footer"><div class="schedule"><CalendarClock :size="15" /><span>{{ folder.cronEnabled ? folder.cronExpression : '手动执行' }}</span><small v-if="folder.cronEnabled">下次 {{ formatNextExecution(folder.nextExecutionAt) }}</small></div><el-tag v-if="folder.enableMinerU" size="small" effect="plain" type="warning">MinerU</el-tag><div class="card-footer-actions"><el-tooltip content="立即扫描并同步"><el-button type="primary" plain size="small" :loading="busy" :disabled="!folder.enabled" @click="emit('sync', folder.folderId)"><Play :size="14" /> 立即同步</el-button></el-tooltip><el-button text size="small" @click="emit('files', folder.folderId)"><FileText :size="14" /> 文件状态</el-button></div></div>
  </article>
</template>
