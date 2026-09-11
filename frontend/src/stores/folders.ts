import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { FolderDTO, CreateFolderRequest, ScanResultDTO } from '../types'
import * as App from '../../wailsjs/go/main/App'

import { errorMessage, requireArray, withTimeout } from './store-helpers'

const LIST_TIMEOUT_MS = 15_000
const SCAN_TIMEOUT_MS = 60_000
const DELETE_TIMEOUT_MS = 120_000

export const useFoldersStore = defineStore('folders', () => {
  const folders = ref<FolderDTO[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  let latestFetchID = 0

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
    } catch (e: unknown) {
      if (fetchID === latestFetchID) error.value = errorMessage(e, '读取同步任务列表失败')
    } finally {
      if (fetchID === latestFetchID) loading.value = false
    }
  }

  async function createFolder(req: CreateFolderRequest): Promise<FolderDTO> {
    try {
      const folder = await withTimeout(() => App.CreateFolder(req), '创建同步任务', LIST_TIMEOUT_MS)
      const idx = folders.value.findIndex(item => item.folderId === folder.folderId)
      if (idx === -1) folders.value.push(folder)
      else folders.value[idx] = folder
      return folder
    } catch (e: unknown) {
      throw new Error(errorMessage(e, '创建同步任务失败'))
    }
  }

  async function updateFolder(folderId: string, req: CreateFolderRequest): Promise<FolderDTO> {
    try {
      const updated = await withTimeout(() => App.UpdateFolder(folderId, req), '更新同步任务', LIST_TIMEOUT_MS)
      const idx = folders.value.findIndex(folder => folder.folderId === folderId)
      if (idx !== -1) folders.value[idx] = updated
      return updated
    } catch (e: unknown) {
      throw new Error(errorMessage(e, '更新同步任务失败'))
    }
  }

  async function deleteFolder(folderId: string) {
    try {
      await withTimeout(() => App.DeleteFolder(folderId), '删除同步任务', DELETE_TIMEOUT_MS)
      folders.value = folders.value.filter(folder => folder.folderId !== folderId)
    } catch (e: unknown) {
      throw new Error(errorMessage(e, '删除同步任务失败'))
    }
  }

  async function scanFolder(folderId: string): Promise<ScanResultDTO> {
    try {
      return await withTimeout(() => App.ScanFolder(folderId), '扫描本地文件夹', SCAN_TIMEOUT_MS)
    } catch (e: unknown) {
      throw new Error(errorMessage(e, '扫描本地文件夹失败'))
    }
  }

  return {
    folders, loading, error,
    fetchFolders, createFolder, updateFolder, deleteFolder, scanFolder,
  }
})
