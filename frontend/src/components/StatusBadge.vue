<script lang="ts" setup>
import { computed } from 'vue'
import { ElTag } from 'element-plus'
import { statusLabel } from '../utils/status'

type Props = { status?: string; type?: 'file' | 'task' }
const props = withDefaults(defineProps<Props>(), { status: '', type: 'task' })


const tones: Record<string, 'success' | 'warning' | 'danger' | 'info' | 'primary'> = {
  SUCCESS: 'success', ENABLED: 'success', SYNCED: 'success', REMOTE_DELETED: 'success', RUNNING: 'primary', MAXKB_PROCESSING: 'primary', MINERU_RUNNING: 'primary', HASHING: 'primary', MAXKB_SPLITTING: 'primary', MAXKB_CREATING: 'primary', QUEUED: 'info', DISCOVERED: 'info', UNCHANGED: 'info', PAUSED: 'warning', PAUSE_REQUESTED: 'warning', STOP_REQUESTED: 'warning', PARTIAL_SUCCESS: 'warning', RECONCILE_REQUIRED: 'warning', REMOTE_DELETE_PENDING: 'warning', FAILED: 'danger', MINERU_FAILED: 'danger', STOPPED: 'info', INTERRUPTED: 'warning', CANCELLED: 'info', DISABLED: 'info', LOCAL_MISSING_REMOTE_KEPT: 'warning',
}
const label = computed(() => statusLabel(props.status))
const tagType = computed(() => tones[props.status] ?? 'info')
</script>

<template><el-tag class="status-tag" :type="tagType" effect="light" size="small">{{ label }}</el-tag></template>
