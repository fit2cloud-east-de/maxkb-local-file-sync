package db

import (
	"testing"
)

func TestInitSchema(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(Config{
		DataDir: tmpDir,
		DBName:  "test.db",
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 初始化数据库结构
	if err := db.InitSchema(); err != nil {
		t.Fatalf("InitSchema() failed: %v", err)
	}

	// 验证所有表都已创建
	expectedTables := []string{
		"sync_folders", "sync_files", "sync_tasks", "sync_runs", "run_files",
		"file_attempts", "job_queue", "active_task_locks", "system_settings", "operation_history",
	}

	for _, tableName := range expectedTables {
		exists, err := db.TableExists(tableName)
		if err != nil {
			t.Fatalf("Failed to check table %s: %v", tableName, err)
		}
		if !exists {
			t.Errorf("Table %s should exist", tableName)
		}
	}

	// 验证外键完整性
	if err := db.CheckForeignKeys(); err != nil {
		t.Errorf("Foreign key check failed: %v", err)
	}
}

func TestGetTableNames(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(Config{
		DataDir: tmpDir,
		DBName:  "test.db",
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 初始化数据库结构
	if err := db.InitSchema(); err != nil {
		t.Fatalf("InitSchema() failed: %v", err)
	}

	// 获取所有表名
	tables, err := db.GetTableNames()
	if err != nil {
		t.Fatalf("GetTableNames() failed: %v", err)
	}

	// 验证表数量
	expectedCount := 10
	if len(tables) != expectedCount {
		t.Errorf("Expected %d tables, got %d", expectedCount, len(tables))
	}

	// 验证表名按字母排序
	for i := 1; i < len(tables); i++ {
		if tables[i-1] >= tables[i] {
			t.Errorf("Tables not sorted: %s >= %s", tables[i-1], tables[i])
		}
	}
}

func TestTableExists(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(Config{
		DataDir: tmpDir,
		DBName:  "test.db",
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 创建测试表
	_, err = db.Exec(`CREATE TABLE test_table (id INTEGER PRIMARY KEY)`)
	if err != nil {
		t.Fatalf("Failed to create test table: %v", err)
	}

	// 测试存在的表
	exists, err := db.TableExists("test_table")
	if err != nil {
		t.Fatalf("TableExists() failed: %v", err)
	}
	if !exists {
		t.Error("test_table should exist")
	}

	// 测试不存在的表
	exists, err = db.TableExists("nonexistent_table")
	if err != nil {
		t.Fatalf("TableExists() failed: %v", err)
	}
	if exists {
		t.Error("nonexistent_table should not exist")
	}
}

func TestDropAllTables(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(Config{
		DataDir: tmpDir,
		DBName:  "test.db",
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 初始化数据库结构
	if err := db.InitSchema(); err != nil {
		t.Fatalf("InitSchema() failed: %v", err)
	}

	// 验证表已创建
	tables, err := db.GetTableNames()
	if err != nil {
		t.Fatalf("GetTableNames() failed: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("Expected tables to exist before drop")
	}

	// 删除所有表
	if err := db.DropAllTables(); err != nil {
		t.Fatalf("DropAllTables() failed: %v", err)
	}

	// 验证所有表已删除
	tables, err = db.GetTableNames()
	if err != nil {
		t.Fatalf("GetTableNames() failed: %v", err)
	}
	if len(tables) != 0 {
		t.Errorf("Expected 0 tables after drop, got %d: %v", len(tables), tables)
	}
}

func TestCheckForeignKeys(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(Config{
		DataDir: tmpDir,
		DBName:  "test.db",
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 创建带外键的测试表
	_, err = db.Exec(`
		CREATE TABLE parent (id INTEGER PRIMARY KEY);
		CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER,
			FOREIGN KEY (parent_id) REFERENCES parent(id));
	`)
	if err != nil {
		t.Fatalf("Failed to create test tables: %v", err)
	}

	// 插入有效数据
	_, err = db.Exec(`INSERT INTO parent (id) VALUES (1)`)
	if err != nil {
		t.Fatalf("Failed to insert parent: %v", err)
	}
	_, err = db.Exec(`INSERT INTO child (id, parent_id) VALUES (1, 1)`)
	if err != nil {
		t.Fatalf("Failed to insert child: %v", err)
	}

	// 检查外键（应该通过）
	if err := db.CheckForeignKeys(); err != nil {
		t.Errorf("CheckForeignKeys() should pass for valid data: %v", err)
	}

	// 禁用外键检查，插入无效数据
	_, err = db.Exec(`PRAGMA foreign_keys = OFF`)
	if err != nil {
		t.Fatalf("Failed to disable foreign keys: %v", err)
	}
	_, err = db.Exec(`INSERT INTO child (id, parent_id) VALUES (2, 999)`)
	if err != nil {
		t.Fatalf("Failed to insert invalid child: %v", err)
	}

	// 检查外键（应该失败）
	if err := db.CheckForeignKeys(); err == nil {
		t.Error("CheckForeignKeys() should fail for invalid data")
	}
}

func TestInitSchemaIdempotency(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(Config{
		DataDir: tmpDir,
		DBName:  "test.db",
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 第一次初始化
	if err := db.InitSchema(); err != nil {
		t.Fatalf("First InitSchema() failed: %v", err)
	}

	// 第二次初始化（应该幂等）
	if err := db.InitSchema(); err != nil {
		t.Fatalf("Second InitSchema() failed: %v", err)
	}

	// 验证表结构仍然正确
	expectedTables := []string{
		"sync_folders", "sync_files", "sync_tasks", "sync_runs", "run_files",
		"file_attempts", "job_queue", "active_task_locks", "system_settings", "operation_history",
	}

	for _, tableName := range expectedTables {
		exists, err := db.TableExists(tableName)
		if err != nil {
			t.Fatalf("Failed to check table %s: %v", tableName, err)
		}
		if !exists {
			t.Errorf("Table %s should exist after idempotent init", tableName)
		}
	}
}
