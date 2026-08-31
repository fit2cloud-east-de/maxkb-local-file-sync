package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
				Level:       LevelInfo,
				LogDir:      tmpDir,
				LogFileName: "test.log",
				Sanitize:    true,
				MaxFileSize: 1024,
				MaxBackups:  3,
			},
			wantErr: false,
		},
		{
			name: "Missing log directory",
			cfg: Config{
				Level:       LevelInfo,
				LogDir:      "",
				LogFileName: "test.log",
			},
			wantErr: true,
		},
		{
			name: "Default values",
			cfg: Config{
				Level:  LevelDebug,
				LogDir: tmpDir,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if logger != nil {
				defer logger.Close()
			}
		})
	}
}

func TestLogger_Levels(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(Config{
		Level:       LevelWarn,
		LogDir:      tmpDir,
		LogFileName: "test.log",
		Sanitize:    false,
	})
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// 低于阈值的日志不应写入
	logger.Debug("debug message")
	logger.Info("info message")

	// 达到阈值的日志应写入
	logger.Warn("warn message")
	logger.Error("error message")

	// 等待写入完成
	time.Sleep(100 * time.Millisecond)

	// 读取日志内容
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)

	// 验证低级别日志未写入
	if strings.Contains(logContent, "debug message") {
		t.Error("Debug message should not be logged")
	}
	if strings.Contains(logContent, "info message") {
		t.Error("Info message should not be logged")
	}

	// 验证高级别日志已写入
	if !strings.Contains(logContent, "warn message") {
		t.Error("Warn message should be logged")
	}
	if !strings.Contains(logContent, "error message") {
		t.Error("Error message should be logged")
	}
}

func TestLogger_Sanitize(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(Config{
		Level:       LevelInfo,
		LogDir:      tmpDir,
		LogFileName: "test.log",
		Sanitize:    true,
	})
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// 记录包含敏感信息的日志
	logger.Info("Authorization: Bearer abc123def456")
	logger.Info("token=secret123&file=test.pdf")

	// 等待写入完成
	time.Sleep(100 * time.Millisecond)

	// 读取日志内容
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)

	// 验证敏感信息已脱敏
	if strings.Contains(logContent, "abc123def456") {
		t.Error("Token should be sanitized")
	}
	if strings.Contains(logContent, "secret123") {
		t.Error("Token should be sanitized")
	}
	if !strings.Contains(logContent, "***") {
		t.Error("Sanitized placeholder should be present")
	}
}

func TestLogger_SanitizesEvenWhenConfigFlagIsFalse(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(Config{
		Level:       LevelInfo,
		LogDir:      tmpDir,
		LogFileName: "test.log",
		Sanitize:    false,
	})
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	logger.Info("request failed at /Users/example/private.txt with token=real-token")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	logContent := string(content)
	if strings.Contains(logContent, "/Users/example/private.txt") || strings.Contains(logContent, "real-token") {
		t.Fatalf("unsafe data leaked when Sanitize=false: %q", logContent)
	}
}

func TestSanitizeDiagnostic_RedactsLocalPathsAndCredentials(t *testing.T) {
	input := `open /Users/example/Documents/secret.pdf: Authorization: Bearer real-token`
	output := SanitizeDiagnostic(input)

	if strings.Contains(output, "/Users/example/Documents/secret.pdf") {
		t.Fatalf("local path leaked in diagnostic: %q", output)
	}
	if strings.Contains(output, "real-token") {
		t.Fatalf("credential leaked in diagnostic: %q", output)
	}
	if !strings.Contains(output, "<local-path>") || !strings.Contains(output, "Bearer ***") {
		t.Fatalf("diagnostic was not safely redacted: %q", output)
	}
}

func TestSanitizeDiagnostic_RedactsHTTPHeaderValues(t *testing.T) {
	input := "MaxKB response: Authorization: Bearer fake-maxkb-token Cookie: session=fake-cookie Set-Cookie: refresh=fake-refresh X-Api-Key=fake-api-key"
	output := SanitizeDiagnostic(input)
	for _, secret := range []string{"fake-maxkb-token", "fake-cookie", "fake-refresh", "fake-api-key"} {
		if strings.Contains(output, secret) {
			t.Fatalf("HTTP header secret leaked: %q in %q", secret, output)
		}
	}
	if strings.Count(output, "<redacted-header>") != 3 {
		t.Fatalf("redacted header count = %d, output=%q", strings.Count(output, "<redacted-header>"), output)
	}
}

func TestSanitizeDiagnostic_PreservesRemoteEndpointPaths(t *testing.T) {
	output := SanitizeDiagnostic("request failed for endpoint=/admin/api/profile")
	if !strings.Contains(output, "/admin/api/profile") {
		t.Fatalf("remote endpoint path was unnecessarily redacted: %q", output)
	}
}

func TestSanitizeDiagnostic_RedactsWindowsPaths(t *testing.T) {
	output := SanitizeDiagnostic(`open C:\Users\example\secret.pdf: permission denied`)
	if strings.Contains(output, `C:\Users\example\secret.pdf`) {
		t.Fatalf("Windows path leaked in diagnostic: %q", output)
	}
	if !strings.Contains(output, "<local-path>") {
		t.Fatalf("Windows path placeholder missing: %q", output)
	}
}

func TestLogger_ErrorWithErr(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(Config{
		Level:       LevelInfo,
		LogDir:      tmpDir,
		LogFileName: "test.log",
		Sanitize:    true,
	})
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// 记录包含敏感信息的错误
	testErr := &testError{msg: "failed to auth with token abc123"}
	logger.ErrorWithErr("operation failed", testErr)

	// 等待写入完成
	time.Sleep(100 * time.Millisecond)

	// 读取日志内容
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)

	// 验证错误信息已记录且脱敏
	if !strings.Contains(logContent, "operation failed") {
		t.Error("Error message should be logged")
	}
	if strings.Contains(logContent, "abc123") {
		t.Error("Token in error should be sanitized")
	}
}

func TestLogger_SetLevel(t *testing.T) {
	tmpDir := t.TempDir()

	logger, err := New(Config{
		Level:       LevelWarn,
		LogDir:      tmpDir,
		LogFileName: "test.log",
	})
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// 验证初始级别
	if logger.GetLevel() != LevelWarn {
		t.Errorf("Initial level = %v, want %v", logger.GetLevel(), LevelWarn)
	}

	// 修改级别
	logger.SetLevel(LevelDebug)
	if logger.GetLevel() != LevelDebug {
		t.Errorf("Updated level = %v, want %v", logger.GetLevel(), LevelDebug)
	}
}

func TestLogger_Rotate(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建小文件大小的日志记录器以触发轮转
	logger, err := New(Config{
		Level:       LevelInfo,
		LogDir:      tmpDir,
		LogFileName: "test.log",
		MaxFileSize: 100, // 100 字节
		MaxBackups:  2,
	})
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// 写入足够多的日志触发轮转
	for i := 0; i < 50; i++ {
		logger.Info("This is a test log message that will trigger rotation")
		time.Sleep(10 * time.Millisecond)
	}

	// 检查是否生成了备份文件
	matches, err := filepath.Glob(filepath.Join(tmpDir, "test.log.*"))
	if err != nil {
		t.Fatalf("Failed to list backup files: %v", err)
	}

	if len(matches) == 0 {
		t.Error("Expected backup files to be created")
	}

	// 验证备份文件数量不超过 MaxBackups
	if len(matches) > 2 {
		t.Errorf("Expected at most 2 backup files, got %d", len(matches))
	}
}

// testError 用于测试的错误类型
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
