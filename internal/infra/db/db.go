package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Config 数据库配置
type Config struct {
	DataDir string // 数据目录
	DBName  string // 数据库文件名
}

// DB 数据库连接封装
type DB struct {
	conn *sql.DB
	path string
}

// New 创建数据库连接
func New(cfg Config) (*DB, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if cfg.DBName == "" {
		cfg.DBName = "maxkb_sync.db"
	}

	dbPath := filepath.Join(cfg.DataDir, cfg.DBName)

	// SQLite 连接参数
	// - WAL mode: 提高并发读写性能
	// - busy_timeout: 避免 SQLITE_BUSY 错误
	// - foreign_keys: 启用外键约束
	// - journal_mode=WAL: Write-Ahead Logging 模式
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath)

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 配置连接池
	conn.SetMaxOpenConns(1) // SQLite 单写入者，避免 SQLITE_BUSY
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0) // 连接不过期

	// 验证连接
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db := &DB{
		conn: conn,
		path: dbPath,
	}

	return db, nil
}

// ImmediateTx is a SQLite transaction started with BEGIN IMMEDIATE.  It uses a
// dedicated connection so that the write reservation is acquired before any
// state is read.  This avoids the deferred-transaction upgrade race that is
// especially dangerous for queue claiming and run control operations.
type ImmediateTx struct {
	conn *sql.Conn
	done bool
}

// BeginImmediate starts a SQLite BEGIN IMMEDIATE transaction.
func (db *DB) BeginImmediate(ctx context.Context) (*ImmediateTx, error) {
	conn, err := db.conn.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to reserve sqlite connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to begin immediate transaction: %w", err)
	}
	return &ImmediateTx{conn: conn}, nil
}

func (tx *ImmediateTx) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return tx.conn.ExecContext(ctx, query, args...)
}

func (tx *ImmediateTx) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return tx.conn.QueryContext(ctx, query, args...)
}

func (tx *ImmediateTx) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return tx.conn.QueryRowContext(ctx, query, args...)
}

func (tx *ImmediateTx) Commit(ctx context.Context) error {
	if tx == nil || tx.done {
		return nil
	}
	_, err := tx.conn.ExecContext(ctx, "COMMIT")
	tx.done = true
	closeErr := tx.conn.Close()
	if err != nil {
		return fmt.Errorf("failed to commit immediate transaction: %w", err)
	}
	return closeErr
}

func (tx *ImmediateTx) Rollback(ctx context.Context) error {
	if tx == nil || tx.done {
		return nil
	}
	_, err := tx.conn.ExecContext(ctx, "ROLLBACK")
	tx.done = true
	closeErr := tx.conn.Close()
	if err != nil {
		return fmt.Errorf("failed to rollback immediate transaction: %w", err)
	}
	return closeErr
}

// Close 关闭数据库连接
func (db *DB) Close() error {
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// Conn 获取原始连接
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// Path 获取数据库文件路径
func (db *DB) Path() string {
	return db.path
}

// BeginTx 开启事务
// 使用 BEGIN IMMEDIATE 避免升级锁时的竞争
func (db *DB) BeginTx() (*sql.Tx, error) {
	// 直接使用 sql.DB.Begin() 开启标准事务
	// modernc.org/sqlite 会自动处理事务类型
	tx, err := db.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	return tx, nil
}

// Exec 执行 SQL 语句
func (db *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return db.conn.Exec(query, args...)
}

// Query 查询多行
func (db *DB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return db.conn.Query(query, args...)
}

// QueryRow 查询单行
func (db *DB) QueryRow(query string, args ...interface{}) *sql.Row {
	return db.conn.QueryRow(query, args...)
}

// Prepare 准备语句
func (db *DB) Prepare(query string) (*sql.Stmt, error) {
	return db.conn.Prepare(query)
}

// WithRetry 使用指数退避重试执行函数
// 用于处理 SQLITE_BUSY 等瞬态错误
func (db *DB) WithRetry(maxRetries int, fn func() error) error {
	var err error
	backoff := 100 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		err = fn()
		if err == nil {
			return nil
		}

		// 检查是否是可重试错误
		if !isRetryableError(err) {
			return err
		}

		if i < maxRetries-1 {
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
		}
	}

	return fmt.Errorf("max retries exceeded: %w", err)
}

// isRetryableError 判断是否是可重试错误
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// SQLite 的 BUSY、LOCKED 错误可以重试
	errMsg := err.Error()
	return contains(errMsg, "database is locked") ||
		contains(errMsg, "database is busy") ||
		contains(errMsg, "SQLITE_BUSY") ||
		contains(errMsg, "SQLITE_LOCKED")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
