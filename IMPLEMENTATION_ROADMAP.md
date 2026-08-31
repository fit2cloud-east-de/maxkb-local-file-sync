# 实施路线图 - 补齐关键功能

基于 GAP_ANALYSIS.md 的分析结果，本文档提供详细的实施计划。

---

## 阶段划分

### 🔴 Phase 6A: 任务控制核心 (P0 - 3-5天)
**目标**: 实现任务启用/关闭、暂停/继续/停止的完整状态机

### 🟡 Phase 6B: 增量同步完善 (P0 - 2-3天)  
**目标**: 完善文件改名识别和本地删除同步

### 🟡 Phase 6C: MinerU 和 Markdown (P1 - 2-3天)
**目标**: MinerU 高级配置和 Markdown 图片处理

### 🟢 Phase 7: 测试和构建 (P1-P2 - 3-5天)
**目标**: 补充测试、文档和构建验证

---

## Phase 6A: 任务控制核心 (P0)

### 6A.1 任务启用/关闭功能

#### 后端实现
**文件**: `app.go`

```go
// EnableTask 启用同步任务
func (a *App) EnableTask(folderId string) error {
    // 1. 更新 enabled = true
    // 2. 重新计算下次执行时间
    // 3. 恢复 Cron 调度
    // 4. 不补跑错过的调度
    // 5. 不自动触发立即同步
}

// DisableTask 关闭同步任务
func (a *App) DisableTask(folderId string) error {
    // 1. 更新 enabled = false, disabled_at = now
    // 2. 取消该任务所有 QUEUED 批次，标记为 CANCELLED
    // 3. 记录取消原因："同步任务已关闭"
    // 4. 禁止新的 Cron 批次入队
    // 5. 已经 RUNNING 的批次继续完成
    // 6. PAUSED 批次保持暂停
    // 7. 不删除任务、映射、日志或远端文档
}
```

**文件**: `internal/service/orchestrator.go`

```go
// 修改 Enqueue 方法
func (o *Orchestrator) Enqueue(taskID string, triggerType string) error {
    // 1. 查询任务的 enabled 状态
    // 2. 如果 enabled=false，拒绝入队并返回错误
    // 3. 检查是否已有该任务的活动批次（QUEUED/RUNNING/PAUSED）
    // 4. 如果有活动批次，拒绝入队
}

// 新增方法：取消任务的所有排队批次
func (o *Orchestrator) CancelQueuedBatches(taskID string, reason string) error {
    // 1. 查询该任务所有 status='QUEUED' 的批次
    // 2. 更新为 status='CANCELLED', control_reason=reason
    // 3. 不执行任何扫描或远端操作
}
```

**文件**: `internal/service/cron.go`

```go
// 修改 Cron 调度逻辑
func (cs *CronService) scheduleTask(task *Task) {
    // 1. 检查 task.Enabled 字段
    // 2. 如果 enabled=false，跳过调度
    // 3. 记录跳过原因到日志
}
```

#### 前端实现
**文件**: `frontend/src/views/FoldersView.vue`

```vue
<template>
  <FolderCard 
    :folder="f" 
    :stats="stats"
    @toggle-enabled="toggleEnabled"
  />
</template>

<script>
async function toggleEnabled(folderId: string, currentEnabled: boolean) {
  const action = currentEnabled ? '关闭' : '启用'
  
  if (!currentEnabled) {
    // 启用确认
    if (!confirm(`确定要启用此同步任务吗？\n\n启用后将：\n- 恢复定时调度\n- 重新计算下次执行时间\n- 不会补跑关闭期间错过的调度`)) {
      return
    }
    await App.EnableTask(folderId)
  } else {
    // 关闭确认
    const hasActive = await checkActiveRuns(folderId)
    let message = '确定要关闭此同步任务吗？\n\n关闭后将：\n- 停止定时调度\n- 取消尚未执行的排队任务'
    
    if (hasActive.running) {
      message += '\n- 当前正在执行的同步将继续完成'
    }
    if (hasActive.paused) {
      message += '\n- 已暂停的同步仍保持暂停'
    }
    message += '\n- 该操作不会删除 MaxKB 中的文档'
    
    if (!confirm(message)) return
    await App.DisableTask(folderId)
  }
  
  await foldersStore.fetchFolders()
}
</script>
```

**文件**: `frontend/src/components/FolderCard.vue`

```vue
<template>
  <div class="folder-card">
    <div class="card-header">
      <div class="card-title">
        <span class="folder-icon">📁</span>
        <span class="folder-name">{{ folder.name }}</span>
        <label class="enable-switch">
          <input 
            type="checkbox" 
            :checked="folder.enabled"
            @change="$emit('toggle-enabled', folder.folderId, folder.enabled)"
          />
          <span class="slider"></span>
        </label>
      </div>
      <span class="folder-status">{{ folder.enableMinerU ? 'MinerU' : 'Direct' }}</span>
    </div>
    <!-- 其他内容 -->
  </div>
</template>
```

#### 数据库更新
**文件**: 新建 `migrations/000002_add_enabled_column.up.sql`

```sql
-- 如果 enabled 列不存在，添加它
ALTER TABLE folders ADD COLUMN enabled INTEGER DEFAULT 1 NOT NULL;
ALTER TABLE folders ADD COLUMN disabled_at TEXT;

-- 为现有任务设置默认值
UPDATE folders SET enabled = 1 WHERE enabled IS NULL;
```

---

### 6A.2 安全检查点机制

#### 后端实现
**文件**: `internal/service/sync_executor.go`

```go
type Checkpoint struct {
    Version           int                 `json:"version"`
    CurrentFileIndex  int                 `json:"current_file_index"`
    CompletedFiles    []string            `json:"completed_files"`
    FailedFiles       map[string]string   `json:"failed_files"` // path -> error
    PendingDeletes    []string            `json:"pending_deletes"`
    CompletedDeletes  []string            `json:"completed_deletes"`
    MinerUTasks       map[string]string   `json:"mineru_tasks"` // path -> task_id
}

// 在每个安全检查点保存状态
func (se *SyncExecutor) saveCheckpoint(runID string, cp *Checkpoint) error {
    data, _ := json.Marshal(cp)
    _, err := se.db.Exec(`
        UPDATE tasks 
        SET checkpoint_version = ?, checkpoint_data = ? 
        WHERE id = ?
    `, cp.Version, string(data), runID)
    return err
}

// 检查是否请求暂停或停止
func (se *SyncExecutor) shouldPause(runID string) (bool, error) {
    var status string
    err := se.db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, runID).Scan(&status)
    return status == "PAUSE_REQUESTED", err
}

func (se *SyncExecutor) shouldStop(runID string) (bool, error) {
    var status string
    err := se.db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, runID).Scan(&status)
    return status == "STOP_REQUESTED", err
}

// 主循环增强
func (se *SyncExecutor) processFiles(runID string, files []FileChange) error {
    cp := &Checkpoint{Version: 1, CompletedFiles: []string{}}
    
    for i, file := range files {
        cp.CurrentFileIndex = i
        
        // 检查点：文件开始前
        if pause, _ := se.shouldPause(runID); pause {
            se.saveCheckpoint(runID, cp)
            se.updateStatus(runID, "PAUSED")
            return nil // 优雅退出
        }
        
        if stop, _ := se.shouldStop(runID); stop {
            se.saveCheckpoint(runID, cp)
            se.updateStatus(runID, "STOPPED")
            return nil
        }
        
        // 处理文件（原子操作）
        err := se.processFile(runID, file)
        if err == nil {
            cp.CompletedFiles = append(cp.CompletedFiles, file.Path)
        } else {
            cp.FailedFiles[file.Path] = err.Error()
        }
        
        // 检查点：文件完成后
        se.saveCheckpoint(runID, cp)
    }
    
    return nil
}

// 恢复执行
func (se *SyncExecutor) resumeFromCheckpoint(runID string) error {
    var cpData string
    err := se.db.QueryRow(`
        SELECT checkpoint_data FROM tasks WHERE id = ?
    `, runID).Scan(&cpData)
    
    if err != nil || cpData == "" {
        return errors.New("no checkpoint found")
    }
    
    var cp Checkpoint
    json.Unmarshal([]byte(cpData), &cp)
    
    // 获取所有文件
    allFiles := se.loadFilesForRun(runID)
    
    // 过滤已完成的文件
    remainingFiles := filterCompleted(allFiles, cp.CompletedFiles)
    
    // 继续处理剩余文件
    return se.processFiles(runID, remainingFiles)
}
```

---

### 6A.3 暂停/继续/停止实现

#### 后端实现
**文件**: `app.go`

```go
func (a *App) PauseTask(taskId string) error {
    // 1. 检查当前状态是否为 RUNNING
    // 2. 更新状态为 PAUSE_REQUESTED
    // 3. 记录 pause_requested_at
    // 4. 执行器在下一个检查点会检测到并暂停
}

func (a *App) ResumeTask(taskId string) error {
    // 1. 检查当前状态是否为 PAUSED
    // 2. 更新状态为 QUEUED
    // 3. 记录 resumed_at
    // 4. orchestrator 会重新调度
}

func (a *App) StopTask(taskId string) error {
    var status string
    a.db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, taskId).Scan(&status)
    
    switch status {
    case "QUEUED":
        // 取消排队：直接标记为 CANCELLED
        return a.cancelQueuedTask(taskId)
    case "RUNNING":
        // 停止运行：标记为 STOP_REQUESTED
        return a.requestStop(taskId)
    case "PAUSED":
        // 停止暂停：直接标记为 STOPPED
        return a.stopPausedTask(taskId)
    default:
        return errors.New("cannot stop task in state: " + status)
    }
}

func (a *App) cancelQueuedTask(taskId string) error {
    _, err := a.db.Exec(`
        UPDATE tasks 
        SET status = 'CANCELLED', 
            control_reason = '用户取消排队',
            cancelled_at = datetime('now')
        WHERE id = ? AND status = 'QUEUED'
    `, taskId)
    return err
}

func (a *App) requestStop(taskId string) error {
    _, err := a.db.Exec(`
        UPDATE tasks 
        SET status = 'STOP_REQUESTED',
            stop_requested_at = datetime('now')
        WHERE id = ? AND status = 'RUNNING'
    `, taskId)
    return err
}

func (a *App) stopPausedTask(taskId string) error {
    _, err := a.db.Exec(`
        UPDATE tasks 
        SET status = 'STOPPED',
            control_reason = '用户停止任务',
            stopped_at = datetime('now')
        WHERE id = ? AND status = 'PAUSED'
    `, taskId)
    return err
}
```

#### 前端实现
**文件**: `frontend/src/views/TasksView.vue`

```vue
<template>
  <div v-for="task in tasks" :key="task.id">
    <!-- 根据状态显示不同按钮 -->
    <div v-if="task.status === 'QUEUED'">
      <button @click="cancelTask(task.id)">取消排队</button>
    </div>
    
    <div v-else-if="task.status === 'RUNNING'">
      <button @click="pauseTask(task.id)">暂停</button>
      <button @click="stopTask(task.id)" class="btn-danger">停止</button>
    </div>
    
    <div v-else-if="task.status === 'PAUSE_REQUESTED'">
      <span>正在暂停...</span>
      <button @click="stopTask(task.id)" class="btn-danger">停止</button>
    </div>
    
    <div v-else-if="task.status === 'PAUSED'">
      <button @click="resumeTask(task.id)">继续</button>
      <button @click="stopTask(task.id)" class="btn-danger">停止</button>
    </div>
    
    <div v-else-if="task.status === 'STOP_REQUESTED'">
      <span>正在停止...</span>
    </div>
    
    <div v-else>
      <!-- SUCCESS, FAILED, STOPPED, CANCELLED -->
      <button @click="retryTask(task.folderId)">重新同步</button>
    </div>
  </div>
</template>

<script setup lang="ts">
async function pauseTask(taskId: string) {
  if (!confirm('暂停后，当前文件将在安全检查点后停止。\n已完成的操作不会回滚。\n是否继续？')) {
    return
  }
  await tasksStore.pauseTask(taskId)
}

async function stopTask(taskId: string) {
  const msg = `停止后将不再处理本批次剩余文件。
当前正在处理的文件将在到达安全检查点后停止。
已经完成的上传、更新或删除不会回滚。
同步任务本身仍保持当前启用状态。
是否确定停止？`
  
  if (!confirm(msg)) return
  await tasksStore.stopTask(taskId)
}

async function cancelTask(taskId: string) {
  if (!confirm('确定要取消此排队任务吗？\n取消后将不会执行扫描和同步操作。')) {
    return
  }
  await tasksStore.stopTask(taskId) // 后端会根据状态处理
}

async function resumeTask(taskId: string) {
  await tasksStore.resumeTask(taskId)
}
</script>
```

---

### 6A.4 数据库 Schema 更新

**文件**: 新建 `migrations/000003_add_control_fields.up.sql`

```sql
-- 添加任务控制字段
ALTER TABLE tasks ADD COLUMN pause_requested_at TEXT;
ALTER TABLE tasks ADD COLUMN paused_at TEXT;
ALTER TABLE tasks ADD COLUMN resumed_at TEXT;
ALTER TABLE tasks ADD COLUMN stop_requested_at TEXT;
ALTER TABLE tasks ADD COLUMN stopped_at TEXT;
ALTER TABLE tasks ADD COLUMN cancelled_at TEXT;
ALTER TABLE tasks ADD COLUMN control_reason TEXT;

-- 确保 checkpoint 字段存在
ALTER TABLE tasks ADD COLUMN checkpoint_version INTEGER DEFAULT 0;
ALTER TABLE tasks ADD COLUMN checkpoint_data TEXT;

-- 添加索引
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_folder_status ON tasks(folder_id, status);
```

---

## Phase 6B: 增量同步完善 (P0)

### 6B.1 本地删除同步功能

#### 数据库更新
**文件**: 新建 `migrations/000004_add_delete_sync_option.up.sql`

```sql
-- 为 folders 表添加删除同步配置
ALTER TABLE folders ADD COLUMN delete_remote_on_local_delete INTEGER DEFAULT 0 NOT NULL;

-- 为 files 表添加状态字段
ALTER TABLE files ADD COLUMN is_local_missing INTEGER DEFAULT 0 NOT NULL;
```

#### 后端实现
**文件**: `internal/service/sync_executor.go`

```go
type ChangeType string

const (
    ChangeTypeNew        ChangeType = "NEW"
    ChangeTypeModified   ChangeType = "MODIFIED"
    ChangeTypeUnchanged  ChangeType = "UNCHANGED"
    ChangeTypeDeleted    ChangeType = "DELETED"
    ChangeTypeRenamed    ChangeType = "RENAMED"
)

type FileChange struct {
    Path            string
    ChangeType      ChangeType
    CurrentMD5      string
    OldMD5          string
    OldPath         string // 用于改名
    DocumentID      string
    SourceFileID    string
}

// 增强的差异计算
func (se *SyncExecutor) computeChanges(taskID string, scannedFiles map[string]string) ([]FileChange, error) {
    // 1. 从数据库加载该任务的所有文件映射
    dbFiles := se.loadTaskFiles(taskID)
    
    // 2. 识别新增文件
    var changes []FileChange
    for path, md5 := range scannedFiles {
        if dbFile, exists := dbFiles[path]; !exists {
            changes = append(changes, FileChange{
                Path: path, ChangeType: ChangeTypeNew, CurrentMD5: md5,
            })
        } else if dbFile.MD5 != md5 {
            changes = append(changes, FileChange{
                Path: path, ChangeType: ChangeTypeModified,
                CurrentMD5: md5, OldMD5: dbFile.MD5,
                DocumentID: dbFile.DocumentID,
            })
        } else {
            changes = append(changes, FileChange{
                Path: path, ChangeType: ChangeTypeUnchanged,
                CurrentMD5: md5, DocumentID: dbFile.DocumentID,
            })
        }
    }
    
    // 3. 识别删除和改名
    for path, dbFile := range dbFiles {
        if _, exists := scannedFiles[path]; !exists {
            // 文件在数据库中但不在扫描结果中 -> 可能删除或改名
            renamed := se.detectRename(dbFile.MD5, scannedFiles, dbFiles)
            if renamed != "" {
                changes = append(changes, FileChange{
                    Path: renamed, ChangeType: ChangeTypeRenamed,
                    CurrentMD5: dbFile.MD5, OldPath: path,
                    DocumentID: dbFile.DocumentID,
                })
            } else {
                changes = append(changes, FileChange{
                    Path: path, ChangeType: ChangeTypeDeleted,
                    DocumentID: dbFile.DocumentID,
                })
            }
        }
    }
    
    return changes, nil
}

// 改名检测：找到唯一匹配的新文件
func (se *SyncExecutor) detectRename(md5 string, scannedFiles map[string]string, dbFiles map[string]DBFile) string {
    var candidates []string
    for path, fileMD5 := range scannedFiles {
        if fileMD5 == md5 {
            if _, inDB := dbFiles[path]; !inDB {
                candidates = append(candidates, path)
            }
        }
    }
    
    // 只有唯一候选时才判断为改名
    if len(candidates) == 1 {
        return candidates[0]
    }
    return ""
}

// 处理删除文件
func (se *SyncExecutor) handleDeletedFile(runID string, change FileChange, config TaskConfig) error {
    if !config.DeleteRemoteOnLocalDelete {
        // 标记为 LOCAL_MISSING_REMOTE_KEPT
        return se.markFileMissing(runID, change.Path)
    }
    
    // 删除远端文档
    err := se.maxkbAdapter.DeleteDocument(config.WorkspaceID, config.KnowledgeID, change.DocumentID)
    if err != nil {
        if isNotFoundError(err) {
            // 404 视为成功
            return se.deleteFileMapping(runID, change.Path)
        }
        return err
    }
    
    // 删除本地映射
    return se.deleteFileMapping(runID, change.Path)
}

// 处理改名文件
func (se *SyncExecutor) handleRenamedFile(runID string, change FileChange) error {
    // 更新数据库映射的路径，保留 document_id
    _, err := se.db.Exec(`
        UPDATE files 
        SET relative_path = ?, 
            normalized_path = ?,
            updated_at = datetime('now')
        WHERE task_id = ? AND normalized_path = ?
    `, change.Path, normalizePath(change.Path), runID, normalizePath(change.OldPath))
    
    return err
}
```

#### 前端实现
**文件**: `frontend/src/views/FoldersView.vue`

```vue
<template>
  <Modal :show="showModal" @close="showModal = false">
    <!-- 其他表单字段 -->
    
    <label class="checkbox-label">
      <input 
        type="checkbox" 
        v-model="form.deleteRemoteOnLocalDelete"
      />
      <span>同步删除：本地文件删除后同步删除 MaxKB 文档</span>
      <p class="help-text">
        开启后，本地删除的文件会同步删除 MaxKB 中对应的文档。
        关闭时，本地删除不影响远端，文件重新出现时会跳过上传。
      </p>
    </label>
  </Modal>
</template>

<script setup lang="ts">
const form = ref({
  // ...其他字段
  deleteRemoteOnLocalDelete: false,
})
</script>
```

---

### 6B.2 文件状态完整性

**文件**: 新建 `migrations/000005_add_file_status.up.sql`

```sql
-- 为 files 表添加详细状态
ALTER TABLE files ADD COLUMN change_type TEXT; -- NEW, MODIFIED, UNCHANGED, DELETED, RENAMED
ALTER TABLE files ADD COLUMN reconcile_required INTEGER DEFAULT 0;
ALTER TABLE files ADD COLUMN reconcile_reason TEXT;

-- 添加索引
CREATE INDEX IF NOT EXISTS idx_files_change_type ON files(change_type);
CREATE INDEX IF NOT EXISTS idx_files_reconcile ON files(reconcile_required) WHERE reconcile_required = 1;
```

---

## Phase 6C: MinerU 和 Markdown (P1)

### 6C.1 MinerU 高级配置

**文件**: `frontend/src/views/SettingsView.vue`

```vue
<template>
  <section class="panel form-panel">
    <h2>MinerU 高级设置</h2>
    
    <label>
      失败重试次数
      <input v-model.number="mineru.retryCount" type="number" min="0" max="10" />
    </label>
    
    <label>
      请求超时（秒）
      <input v-model.number="mineru.requestTimeout" type="number" min="10" max="300" />
    </label>
    
    <label>
      任务总超时（分钟）
      <input v-model.number="mineru.taskTimeout" type="number" min="1" max="180" />
    </label>
    
    <label>
      状态轮询间隔（秒）
      <input v-model.number="mineru.pollInterval" type="number" min="1" max="60" />
    </label>
    
    <label class="checkbox-label">
      <input type="checkbox" v-model="mineru.saveResults" />
      保存完整转换结果到本地
    </label>
    
    <label v-if="mineru.saveResults">
      保存根目录
      <input v-model="mineru.resultsDir" placeholder="/path/to/mineru-results" />
      <button type="button" class="btn-secondary btn-sm" @click="selectDir">浏览...</button>
    </label>
    
    <!-- 内网 MinerU 高级参数 -->
    <div v-if="mineru.mode === 'internal'">
      <h3>内网 MinerU 参数</h3>
      
      <label>
        后端引擎
        <select v-model="mineru.backend">
          <option value="hybrid-engine">Hybrid Engine</option>
          <option value="simple-engine">Simple Engine</option>
          <option value="hybrid-engine-http-client">Hybrid Engine (HTTP Client)</option>
        </select>
      </label>
      
      <label v-if="mineru.backend.includes('http-client')">
        服务器 URL
        <input v-model="mineru.serverUrl" placeholder="http://your-mineru-server:8080" />
      </label>
      
      <label>
        处理精度
        <select v-model="mineru.effort">
          <option value="low">低</option>
          <option value="medium">中</option>
          <option value="high">高</option>
        </select>
      </label>
      
      <label>
        解析方法
        <select v-model="mineru.parseMethod">
          <option value="auto">自动</option>
          <option value="txt">纯文本</option>
          <option value="ocr">OCR</option>
        </select>
      </label>
      
      <label>
        语言
        <select v-model="mineru.language">
          <option value="ch">中文</option>
          <option value="en">英文</option>
        </select>
      </label>
      
      <div class="checkbox-group">
        <label class="checkbox-label">
          <input type="checkbox" v-model="mineru.formulaEnable" />
          公式识别
        </label>
        <label class="checkbox-label">
          <input type="checkbox" v-model="mineru.tableEnable" />
          表格识别
        </label>
        <label class="checkbox-label">
          <input type="checkbox" v-model="mineru.imageAnalysis" />
          图片分析
        </label>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
const mineru = reactive({
  // ...现有字段
  retryCount: 3,
  requestTimeout: 30,
  taskTimeout: 60,
  pollInterval: 2,
  saveResults: false,
  resultsDir: '',
  // 内网高级参数
  backend: 'hybrid-engine',
  serverUrl: '',
  effort: 'medium',
  parseMethod: 'auto',
  language: 'ch',
  formulaEnable: true,
  tableEnable: true,
  imageAnalysis: true,
})

async function selectDir() {
  const dir = await App.SelectDirectory()
  if (dir) mineru.resultsDir = dir
}
</script>
```

---

### 6C.2 Markdown 图片处理

**文件**: `internal/service/markdown_processor.go` (新建)

```go
package service

import (
    "fmt"
    "path/filepath"
    "strings"
    "github.com/yuin/goldmark"
    "github.com/yuin/goldmark/ast"
    "github.com/yuin/goldmark/text"
)

type MarkdownProcessor struct {
    maxkbAdapter MaxKBAdapter
    workspaceID  string
    knowledgeID  string
}

// ProcessImages 处理 Markdown 中的图片引用
func (mp *MarkdownProcessor) ProcessImages(mdContent string, mdFilePath string) (string, error) {
    md := goldmark.New()
    reader := text.NewReader([]byte(mdContent))
    doc := md.Parser().Parse(reader)
    
    var replacements []ImageReplacement
    
    // 遍历 AST 查找图片节点
    ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
        if !entering {
            return ast.WalkContinue, nil
        }
        
        if img, ok := n.(*ast.Image); ok {
            dest := string(img.Destination)
            
            // 跳过已经是 OSS 地址的图片
            if strings.Contains(dest, "/oss/file/") {
                return ast.WalkContinue, nil
            }
            
            // 跳过 HTTP/HTTPS 图片
            if strings.HasPrefix(dest, "http://") || strings.HasPrefix(dest, "https://") {
                return ast.WalkContinue, nil
            }
            
            // 处理相对路径图片
            absPath := mp.resolveImagePath(dest, mdFilePath)
            if !fileExists(absPath) {
                return ast.WalkContinue, fmt.Errorf("image not found: %s", dest)
            }
            
            // 上传到 MaxKB OSS
            ossURL, err := mp.uploadImageToOSS(absPath)
            if err != nil {
                return ast.WalkContinue, fmt.Errorf("upload image failed: %w", err)
            }
            
            replacements = append(replacements, ImageReplacement{
                Original: dest,
                OSS:      ossURL,
            })
        }
        
        return ast.WalkContinue, nil
    })
    
    // 执行替换
    result := mdContent
    for _, r := range replacements {
        result = strings.ReplaceAll(result, r.Original, r.OSS)
    }
    
    return result, nil
}

// 解析图片路径（兼容 Windows 和 URL 编码）
func (mp *MarkdownProcessor) resolveImagePath(imgPath string, mdFilePath string) string {
    // URL 解码
    decoded, _ := url.QueryUnescape(imgPath)
    
    // 转换为系统路径分隔符
    decoded = filepath.FromSlash(decoded)
    
    // 相对于 Markdown 文件的路径
    mdDir := filepath.Dir(mdFilePath)
    return filepath.Join(mdDir, decoded)
}

// 上传图片到 MaxKB OSS
func (mp *MarkdownProcessor) uploadImageToOSS(imagePath string) (string, error) {
    return mp.maxkbAdapter.UploadFileToOSS(imagePath)
}
```

**依赖**: 添加到 `go.mod`
```
github.com/yuin/goldmark v1.6.0
```

---

## Phase 7: 测试和构建 (P1-P2)

### 7.1 单元测试补充

**优先测试模块**:
1. ✅ 数据库迁移测试（已有）
2. 路径标准化测试
3. Include/Exclude 正则测试
4. MD5 流式计算测试
5. 文件变更检测测试（NEW/MODIFIED/DELETED/RENAMED）
6. Checkpoint 序列化/反序列化测试
7. 状态机转换测试

**示例**: `internal/service/file_scanner_test.go`

```go
package service

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestDetectRename(t *testing.T) {
    scanned := map[string]string{
        "new_name.txt": "abc123",
        "other.txt":    "def456",
    }
    
    dbFiles := map[string]DBFile{
        "old_name.txt": {MD5: "abc123"},
    }
    
    se := &SyncExecutor{}
    renamed := se.detectRename("abc123", scanned, dbFiles)
    
    assert.Equal(t, "new_name.txt", renamed)
}

func TestDetectRename_MultipleMatches(t *testing.T) {
    scanned := map[string]string{
        "copy1.txt": "abc123",
        "copy2.txt": "abc123",
    }
    
    dbFiles := map[string]DBFile{
        "original.txt": {MD5: "abc123"},
    }
    
    se := &SyncExecutor{}
    renamed := se.detectRename("abc123", scanned, dbFiles)
    
    // 多个候选时不判断为改名
    assert.Empty(t, renamed)
}
```

---

### 7.2 集成测试

**测试场景**:
1. 端到端同步流程（本地文件 → MaxKB）
2. 暂停后继续，验证不重复处理
3. 停止后重新同步，验证增量恢复
4. 关闭任务取消排队批次
5. 文件改名识别
6. 本地删除同步（开关开启/关闭）

---

### 7.3 构建验证

**macOS**:
```bash
wails build -platform darwin/amd64
wails build -platform darwin/arm64
# 验证启动和基础功能
```

**Windows**:
```bash
wails build -platform windows/amd64
# 验证启动和 Credential Manager
```

---

## 实施时间线

| 阶段 | 任务 | 预计时间 | 依赖 |
|-----|------|---------|-----|
| 6A.1 | 任务启用/关闭 | 1天 | 无 |
| 6A.2 | 安全检查点机制 | 2天 | 无 |
| 6A.3 | 暂停/继续/停止 | 1.5天 | 6A.2 |
| 6A.4 | 数据库迁移 | 0.5天 | 6A.1, 6A.2, 6A.3 |
| 6B.1 | 本地删除同步 | 1.5天 | 无 |
| 6B.2 | 文件状态完整性 | 0.5天 | 6B.1 |
| 6C.1 | MinerU 高级配置 | 1天 | 无 |
| 6C.2 | Markdown 图片处理 | 1.5天 | 无 |
| 7.1 | 单元测试补充 | 2天 | 6A, 6B |
| 7.2 | 集成测试 | 2天 | 6A, 6B, 6C |
| 7.3 | 构建验证 | 1天 | 7.1, 7.2 |

**总计**: 约 14-16 个工作日

---

## 验收标准

### Phase 6A 完成标准
- [ ] 前端可以启用/关闭同步任务
- [ ] 关闭任务自动取消排队批次
- [ ] 关闭状态下 Cron 不触发新批次
- [ ] 暂停按钮在安全检查点后生效
- [ ] 暂停后应用重启仍保持暂停
- [ ] 继续后从上次位置恢复，不重复处理
- [ ] 停止排队批次不执行任何远端操作
- [ ] 停止运行批次保留已完成映射
- [ ] checkpoint_data 正确序列化和恢复

### Phase 6B 完成标准
- [ ] 文件改名被正确识别为 RENAMED
- [ ] 多个相同 MD5 时不误判改名
- [ ] 删除同步开关正常工作
- [ ] 关闭删除同步时标记 LOCAL_MISSING_REMOTE_KEPT
- [ ] 删除文件重新出现时正确处理

### Phase 6C 完成标准
- [ ] MinerU 高级参数可配置
- [ ] Markdown 相对路径图片上传 OSS
- [ ] HTTP/HTTPS 图片不重复上传
- [ ] 已是 OSS 地址的图片不处理
- [ ] Windows 路径正确解析

### Phase 7 完成标准
- [ ] 核心模块单元测试覆盖率 > 70%
- [ ] 至少 5 个端到端集成测试通过
- [ ] macOS 构建可启动和基础功能正常
- [ ] Windows 构建可启动和凭据存储正常

---

**文档版本**: 1.0  
**最后更新**: 2026-08-18
