import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { FolderDTO, CreateFolderRequest, FileStatsDTO, ScanResultDTO } from '../types'
import * as App from '../../wailsjs/go/main/App'

import { errorMessage, requireArray, withTimeout } from './store-helpers'

const LIST_TIMEOUT_MS = 15_000
const SCAN_TIMEOUT_MS = 60_000

export const useFoldersStore = defineStore('folders', () => {
  const folders = ref<FolderDTO[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const statsError = ref<string | null>(null)
  const statsMap = ref<Record<string, FileStatsDTO>>({})
  let latestFetchID = 0
  let latestStatsFetchID = 0

  async function fetchFolders() {
    const fetchID = ++latestFetchID
    loading.value = true
    error.value = null
    try {
      const result = requireArray<FolderDTO>(
        await withTimeout(() => App.ListFolders(), '读取同步任务列表', LIST_TIMEOUT_MS),
        '同步任务列表',
      )
      if (fetchID !== latestFetchID) return
      folders.value = result
      const currentIDs = new Set(result.map(folder => folder.folderId))
      for (const folderId of Object.keys(statsMap.value)) {
        if (!currentIDs.has(folderId)) delete statsMap.value[folderId]
      }
      // 统计是非阻塞的；即使单个统计接口卡住，也不能让任务列表保持 loading。
      loading.value = false
      void fetchAllStats()
    } catch (e: unknown) {
      if (fetchID === latestFetchID) error.value = errorMessage(e, '读取同步任务列表失败')
    } finally {
      if (fetchID === latestFetchID) loading.value = false
    }
  }

  async function fetchAllStats() {
    const statsFetchID = ++latestStatsFetchID
    const snapshot = folders.value.map(folder => folder.folderId)
    statsError.value = null

    for (const folderId of snapshot) {
      try {
        const stats = await withTimeout(() => App.GetFileStats(folderId), '读取文件统计', LIST_TIMEOUT_MS)
        if (statsFetchID !== latestStatsFetchID) return
        const extendedStats = stats as typeof stats & { failed?: number }
        statsMap.value[folderId] = { ...stats, stale: stats.stale ?? 0, failed: extendedStats.failed ?? 0 }
      } catch (e: unknown) {
        if (statsFetchID !== latestStatsFetchID) return
        // 统计失败不覆盖已经成功的任务列表；单独暴露状态供需要细化提示的调用方使用。
        statsError.value = errorMessage(e, '读取文件统计失败')
      }
    }
  }

  async function createFolder(req: CreateFolderRequest): Promise<FolderDTO> {
    error.value = null
    try {
      const folder = await withTimeout(() => App.CreateFolder(req), '创建同步任务', LIST_TIMEOUT_MS)
      const idx = folders.value.findIndex(item => item.folderId === folder.folderId)
      if (idx === -1) folders.value.push(folder)
      else folders.value[idx] = folder
      return folder
    } catch (e: unknown) {
      error.value = errorMessage(e, '创建同步任务失败')
      throw new Error(error.value)
    }
  }

  async function updateFolder(folderId: string, req: CreateFolderRequest): Promise<FolderDTO> {
    error.value = null
    try {
      const updated = await withTimeout(() => App.UpdateFolder(folderId, req), '更新同步任务', LIST_TIMEOUT_MS)
      const idx = folders.value.findIndex(folder => folder.folderId === folderId)
      if (idx !== -1) folders.value[idx] = updated
      return updated
    } catch (e: unknown) {
      error.value = errorMessage(e, '更新同步任务失败')
      throw new Error(error.value)
    }
  }

  async function deleteFolder(folderId: string) {
    error.value = null
    try {
      await withTimeout(() => App.DeleteFolder(folderId), '删除同步任务', LIST_TIMEOUT_MS)
      folders.value = folders.value.filter(folder => folder.folderId !== folderId)
      delete statsMap.value[folderId]
    } catch (e: unknown) {
      error.value = errorMessage(e, '删除同步任务失败')
      throw new Error(error.value)
    }
  }

  async function scanFolder(folderId: string): Promise<ScanResultDTO> {
    error.value = null
    try {
      return await withTimeout(() => App.ScanFolder(folderId), '扫描本地文件夹', SCAN_TIMEOUT_MS)
    } catch (e: unknown) {
      error.value = errorMessage(e, '扫描本地文件夹失败')
      throw new Error(error.value)
    }
  }

  function getStats(folderId: string): FileStatsDTO {
    return statsMap.value[folderId] ?? { total: 0, synced: 0, pending: 0, stale: 0, failed: 0, needsDelete: 0 }
  }

  return {
    folders, loading, error, statsError, statsMap,
    fetchFolders, fetchAllStats, createFolder, updateFolder, deleteFolder, scanFolder, getStats,
  }
})
