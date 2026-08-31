<script lang="ts" setup>
const props = defineProps<{ value: number; total: number; color?: string }>()
const pct = props.total > 0 ? Math.min(100, Math.round((props.value / props.total) * 100)) : 0
</script>

<template>
  <div class="progress-wrap">
    <div class="progress-bar" :style="{ width: pct + '%', background: color ?? 'var(--primary)' }"></div>
  </div>
  <span class="progress-label">{{ value }} / {{ total }} ({{ pct }}%)</span>
</template>

<style scoped>
.progress-wrap {
  height: 6px;
  border-radius: 3px;
  background: #2a2a3e;
  overflow: hidden;
  width: 100%;
}
.progress-bar {
  height: 100%;
  border-radius: 3px;
  transition: width 0.4s ease;
}
.progress-label {
  font-size: 11px;
  color: var(--text-secondary);
  margin-top: 4px;
  display: block;
}
</style>
