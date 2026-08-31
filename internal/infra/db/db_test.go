package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "Valid config",
			cfg: Config{
				DataDir: tmpDir,
				DBName:  "test.db",
			},
			wantErr: false,
		},
		{
			name: "Missing data directory",
			cfg: Config{
				DataDir: "",
				DBName:  "test.db",
			},
			wantErr: true,
		},
		{
			name: "Default DB name",
			cfg: Config{
				DataDir: tmpDir,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if db != nil {
				defer db.Close()
				// 验证连接可用
				if err := db.Conn().Ping(); err != nil {
					t.Errorf("Failed to ping database: %v", err)
				}
			}
		})
	}
}

func TestDB_BeginTx(t *testing.T) {
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
	_, err = db.Exec(`CREATE TABLE test_table (id INTEGER PRIMARY KEY, value TEXT)`)
	if err != nil {
		t.Fatalf("Failed to create test table: %v", err)
	}

	// 测试事务
	tx, err := db.BeginTx()
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	_, err = tx.Exec(`INSERT INTO test_table (id, value) VALUES (1, 'test')`)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to insert in transaction: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Failed to commit transaction: %v", err)
	}

	// 验证数据已提交
	var value string
	err = db.QueryRow(`SELECT value FROM test_table WHERE id = 1`).Scan(&value)
	if err != nil {
		t.Fatalf("Failed to query after commit: %v", err)
	}
	if value != "test" {
		t.Errorf("Expected value 'test', got '%s'", value)
	}
}

func TestDB_WithRetry(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(Config{
		DataDir: tmpDir,
		DBName:  "test.db",
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 测试成功的操作
	callCount := 0
	err = db.WithRetry(3, func() error {
		callCount++
		return nil
	})
	if err != nil {
		t.Errorf("WithRetry() should succeed, got error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}

	// 测试非可重试错误
	callCount = 0
	err = db.WithRetry(3, func() error {
		callCount++
		return os.ErrNotExist
	})
	if err == nil {
		t.Error("WithRetry() should fail for non-retryable error")
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call for non-retryable error, got %d", callCount)
	}
}

func TestDB_Path(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "test.db"
	db, err := New(Config{
		DataDir: tmpDir,
		DBName:  dbName,
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	expectedPath := filepath.Join(tmpDir, dbName)
	if db.Path() != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, db.Path())
	}
}

func TestDB_ExecQueryPrepare(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(Config{
		DataDir: tmpDir,
		DBName:  "test.db",
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 测试 Exec
	_, err = db.Exec(`CREATE TABLE test_users (id INTEGER PRIMARY KEY, name TEXT)`)
	if err != nil {
		t.Fatalf("Exec() failed: %v", err)
	}

	// 测试 Prepare
	stmt, err := db.Prepare(`INSERT INTO test_users (id, name) VALUES (?, ?)`)
	if err != nil {
		t.Fatalf("Prepare() failed: %v", err)
	}
	defer stmt.Close()

	_, err = stmt.Exec(1, "Alice")
	if err != nil {
		t.Fatalf("Stmt.Exec() failed: %v", err)
	}

	// 测试 QueryRow
	var name string
	err = db.QueryRow(`SELECT name FROM test_users WHERE id = ?`, 1).Scan(&name)
	if err != nil {
		t.Fatalf("QueryRow() failed: %v", err)
	}
	if name != "Alice" {
		t.Errorf("Expected name 'Alice', got '%s'", name)
	}

	// 测试 Query
	rows, err := db.Query(`SELECT id, name FROM test_users`)
	if err != nil {
		t.Fatalf("Query() failed: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("Scan() failed: %v", err)
		}
	}
	if count != 1 {
		t.Errorf("Expected 1 row, got %d", count)
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "Database locked",
			err:  &mockError{msg: "database is locked"},
			want: true,
		},
		{
			name: "Database busy",
			err:  &mockError{msg: "database is busy"},
			want: true,
		},
		{
			name: "SQLITE_BUSY",
			err:  &mockError{msg: "SQLITE_BUSY"},
			want: true,
		},
		{
			name: "SQLITE_LOCKED",
			err:  &mockError{msg: "SQLITE_LOCKED"},
			want: true,
		},
		{
			name: "Other error",
			err:  &mockError{msg: "constraint violation"},
			want: false,
		},
		{
			name: "Nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err)
			if got != tt.want {
				t.Errorf("isRetryableError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// mockError 用于测试的错误类型
type mockError struct {
	msg string
}

func (e *mockError) Error() string {
	return e.msg
}
