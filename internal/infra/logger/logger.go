package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"maxkb-local-file-sync/internal/infra/credential"
)

// Level 日志级别
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[Level]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
}

// These patterns protect the log and diagnostic surfaces from exposing local
// filesystem locations. They intentionally target path-shaped values only; URLs
// and ordinary error text remain available for troubleshooting.
var (
	unixPathPattern         = regexp.MustCompile(`(^|[\s=(\["'])/(?:Users|home|tmp|private/tmp|var/folders|Volumes|mnt|root|opt)(?:/[^\s"'<>;,]+)*`)
	windowsPathPattern      = regexp.MustCompile(`(^|[\s=(\["'])[A-Za-z]:[\\/][^\s"'<>;,]+`)
	httpSecretHeaderPattern = regexp.MustCompile(`(?im)(^|[\s,])(Cookie|Set-Cookie|X-Api-Key|X-Auth-Token)\s*[:=]\s*[^\s,]+`)
)

// SanitizeDiagnostic returns an error/message suitable for logs or UI
// diagnostics. It removes credentials using the shared credential sanitizer and
// replaces absolute local paths with a stable placeholder.
func SanitizeDiagnostic(text string) string {
	if text == "" {
		return ""
	}
	text = credential.Sanitize(text)
	text = unixPathPattern.ReplaceAllString(text, `${1}<local-path>`)
	text = windowsPathPattern.ReplaceAllString(text, `${1}<local-path>`)
	// HTTP header values are never needed for a safe diagnostic. Redact them
	// even if a caller accidentally includes a captured header line. Raw HTTP
	// request/response bodies must still never be passed to the logger because
	// arbitrary document content cannot be safely identified by a regex.
	text = httpSecretHeaderPattern.ReplaceAllString(text, `${1}$2: <redacted-header>`)
	return text
}

// SanitizeError returns a safe diagnostic representation of err.
func SanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return SanitizeDiagnostic(err.Error())
}

// Logger 日志记录器
type Logger struct {
	level       Level
	logger      *log.Logger
	file        *os.File
	mu          sync.Mutex
	sanitize    bool
	maxFileSize int64
	maxBackups  int
	logPath     string
}

// Config 日志配置
type Config struct {
	Level       Level  // 日志级别
	LogDir      string // 日志目录
	LogFileName string // 日志文件名
	Sanitize    bool   // 是否脱敏
	MaxFileSize int64  // 单个日志文件最大大小（字节）
	MaxBackups  int    // 保留的旧日志文件数量
	Console     bool   // 是否同时输出到控制台
}

// New 创建日志记录器
func New(cfg Config) (*Logger, error) {
	if cfg.LogDir == "" {
		return nil, fmt.Errorf("log directory is required")
	}
	if cfg.LogFileName == "" {
		cfg.LogFileName = "maxkb_sync.log"
	}
	if cfg.MaxFileSize == 0 {
		cfg.MaxFileSize = 100 * 1024 * 1024 // 默认 100MB
	}
	if cfg.MaxBackups == 0 {
		cfg.MaxBackups = 5
	}

	// 确保日志目录存在
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	logPath := filepath.Join(cfg.LogDir, cfg.LogFileName)

	// 打开日志文件
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	var output io.Writer = file
	if cfg.Console {
		output = io.MultiWriter(file, os.Stdout)
	}

	logger := &Logger{
		level:       cfg.Level,
		logger:      log.New(output, "", 0),
		file:        file,
		sanitize:    cfg.Sanitize,
		maxFileSize: cfg.MaxFileSize,
		maxBackups:  cfg.MaxBackups,
		logPath:     logPath,
	}

	return logger, nil
}

// Close 关闭日志记录器
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// log 内部日志方法
func (l *Logger) log(level Level, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// 检查文件大小并轮转
	if err := l.rotateIfNeeded(); err != nil {
		// 轮转失败，继续写入当前文件
		fmt.Fprintf(os.Stderr, "failed to rotate log file: %s\n", SanitizeError(err))
	}

	// Logging is always sanitized. Config.Sanitize is retained for API
	// compatibility, but no caller should be able to accidentally disable
	// credential and local-path redaction.
	message := SanitizeDiagnostic(fmt.Sprintf(format, args...))

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	levelName := levelNames[level]
	logLine := fmt.Sprintf("[%s] [%s] %s\n", timestamp, levelName, message)

	l.logger.Print(logLine)
}

// Debug 记录调试日志
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LevelDebug, format, args...)
}

// Info 记录信息日志
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, format, args...)
}

// Warn 记录警告日志
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LevelWarn, format, args...)
}

// Error 记录错误日志
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, format, args...)
}

// ErrorWithErr 记录带错误对象的错误日志（自动脱敏）
func (l *Logger) ErrorWithErr(message string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = SanitizeError(err)
	}
	l.log(LevelError, "%s: %s", message, errMsg)
}

// rotateIfNeeded 检查并轮转日志文件
func (l *Logger) rotateIfNeeded() error {
	info, err := l.file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat log file: %w", err)
	}

	if info.Size() < l.maxFileSize {
		return nil
	}

	// 关闭当前文件
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("failed to close log file: %w", err)
	}

	// 轮转旧文件
	if err := l.rotateOldFiles(); err != nil {
		return fmt.Errorf("failed to rotate old files: %w", err)
	}

	// 重命名当前文件
	timestamp := time.Now().Format("20060102-150405")
	backupPath := fmt.Sprintf("%s.%s", l.logPath, timestamp)
	if err := os.Rename(l.logPath, backupPath); err != nil {
		return fmt.Errorf("failed to rename log file: %w", err)
	}

	// 打开新文件
	file, err := os.OpenFile(l.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open new log file: %w", err)
	}

	l.file = file
	l.logger.SetOutput(file)

	return nil
}

// rotateOldFiles 删除过期的备份文件
func (l *Logger) rotateOldFiles() error {
	dir := filepath.Dir(l.logPath)
	baseName := filepath.Base(l.logPath)

	// 查找所有备份文件
	pattern := fmt.Sprintf("%s.*", baseName)
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return fmt.Errorf("failed to list backup files: %w", err)
	}

	// 如果备份文件数量超过限制，删除最旧的
	if len(matches) >= l.maxBackups {
		// 按修改时间排序
		type fileInfo struct {
			path    string
			modTime time.Time
		}
		var files []fileInfo
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				continue
			}
			files = append(files, fileInfo{path: match, modTime: info.ModTime()})
		}

		// 按时间排序（旧到新）
		for i := 0; i < len(files); i++ {
			for j := i + 1; j < len(files); j++ {
				if files[i].modTime.After(files[j].modTime) {
					files[i], files[j] = files[j], files[i]
				}
			}
		}

		// 删除最旧的文件
		toDelete := len(files) - l.maxBackups + 1
		for i := 0; i < toDelete && i < len(files); i++ {
			if err := os.Remove(files[i].path); err != nil {
				fmt.Fprintf(os.Stderr, "failed to remove old log file: %s\n", SanitizeError(err))
			}
		}
	}

	return nil
}

// SetLevel 设置日志级别
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel 获取日志级别
func (l *Logger) GetLevel() Level {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.level
}
