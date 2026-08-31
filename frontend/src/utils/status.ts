/**
 * User-facing Chinese labels for persisted task/file statuses and processing stages.
 * The English enum values remain unchanged in the backend and are only used for
 * filtering and state transitions.
 */
export const statusLabels: Record<string, string> = {
  ENABLED: '已启用',
  DISABLED: '已关闭',
  QUEUED: '排队中',
  RUNNING: '同步中',
  PAUSE_REQUESTED: '正在暂停',
  PAUSED: '已暂停',
  STOP_REQUESTED: '正在停止',
  STOPPED: '已停止',
  SUCCESS: '已完成',
  COMPLETED: '已完成',
  PARTIAL_SUCCESS: '部分完成',
  PARTIAL: '部分完成',
  FAILED: '失败',
  INTERRUPTED: '已中断',
  CANCELLED: '已取消',
  RECONCILE_REQUIRED: '待处理',
  PENDING: '待同步',
  SYNCED: '已同步',
  STALE_REMOTE_EXISTS: '远端已过期',
  NEEDS_DELETE: '待删除',
  DELETED: '已删除',
  DISCOVERED: '已发现',
  HASHING: '计算文件指纹',
  UNCHANGED: '未变化',
  MINERU_PENDING: '等待 MinerU 转换',
  MINERU_RUNNING: 'MinerU 转换中',
  MINERU_FAILED: 'MinerU 转换失败',
  UPLOADING: '上传中',
  MAXKB_SPLITTING: 'MaxKB 智能分段中',
  MAXKB_CREATING: '创建 MaxKB 文档中',
  MAXKB_PROCESSING: 'MaxKB 处理中',
  MAXKB_PROCESSING_FAILED: 'MaxKB 处理失败',
  MAXKB_DELETING: '删除 MaxKB 文档中',
  MAXKB_DELETE_COMPLETED: 'MaxKB 文档已删除',
  REMOTE_DELETE_PENDING: '等待删除远端文档',
  REMOTE_DELETED: '远端文档已删除',
  LOCAL_MISSING_REMOTE_KEPT: '本地文件缺失（保留远端）',
  STOPPED_FILE: '文件处理已停止',
  INIT: '初始化',
  DELETING: '删除中',
  DONE: '已完成',
}

export function statusLabel(status?: string, fallback = '未知状态') {
  if (!status) return fallback
  return statusLabels[status] || fallback
}

export function stageLabel(stage?: string, fallback = '等待执行') {
  if (!stage) return fallback
  return statusLabels[stage] || '未知阶段'
}
