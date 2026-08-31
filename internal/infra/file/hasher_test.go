package file

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCalculateMD5(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建测试文件
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hello, World!")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// 计算 MD5
	md5sum, err := CalculateMD5(testFile)
	if err != nil {
		t.Fatalf("CalculateMD5() failed: %v", err)
	}

	// 验证 MD5 格式（32 个十六进制字符）
	if len(md5sum) != 32 {
		t.Errorf("MD5 length = %d, want 32", len(md5sum))
	}

	// 预期的 MD5（"Hello, World!" 的 MD5）
	expected := "65a8e27d8879283831b664bd8b7f0ad4"
	if md5sum != expected {
		t.Errorf("MD5 = %s, want %s", md5sum, expected)
	}

	// 测试相同内容的文件生成相同的 MD5
	testFile2 := filepath.Join(tmpDir, "test2.txt")
	if err := os.WriteFile(testFile2, content, 0644); err != nil {
		t.Fatalf("Failed to create test file 2: %v", err)
	}

	md5sum2, err := CalculateMD5(testFile2)
	if err != nil {
		t.Fatalf("CalculateMD5() failed for file 2: %v", err)
	}

	if md5sum != md5sum2 {
		t.Errorf("Same content should have same MD5: %s != %s", md5sum, md5sum2)
	}

	// 测试不存在的文件
	_, err = CalculateMD5(filepath.Join(tmpDir, "nonexistent.txt"))
	if err == nil {
		t.Error("CalculateMD5() should fail for nonexistent file")
	}
}

func TestCalculateMD5Reader(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "Empty content",
			content:  "",
			expected: "d41d8cd98f00b204e9800998ecf8427e",
		},
		{
			name:     "Simple text",
			content:  "Hello, World!",
			expected: "65a8e27d8879283831b664bd8b7f0ad4",
		},
		{
			name:     "Multiline text",
			content:  "Line 1\nLine 2\nLine 3",
			expected: "040be657ecde8cf992ef02b970eda5f8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.txt")
			if err := os.WriteFile(testFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			file, err := os.Open(testFile)
			if err != nil {
				t.Fatalf("Failed to open test file: %v", err)
			}
			defer file.Close()

			md5sum, err := CalculateMD5Reader(file)
			if err != nil {
				t.Fatalf("CalculateMD5Reader() failed: %v", err)
			}

			if md5sum != tt.expected {
				t.Errorf("MD5 = %s, want %s", md5sum, tt.expected)
			}
		})
	}
}

func TestCreateSnapshot(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建测试文件
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Test content for snapshot")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// 创建快照
	snapshot, err := CreateSnapshot(testFile)
	if err != nil {
		t.Fatalf("CreateSnapshot() failed: %v", err)
	}

	// 验证快照字段
	if snapshot.Path != testFile {
		t.Errorf("Snapshot.Path = %s, want %s", snapshot.Path, testFile)
	}

	expectedSize := int64(len(content))
	if snapshot.Size != expectedSize {
		t.Errorf("Snapshot.Size = %d, want %d", snapshot.Size, expectedSize)
	}

	if snapshot.MD5 == "" {
		t.Error("Snapshot.MD5 should not be empty")
	}

	if snapshot.ModifiedAt == 0 {
		t.Error("Snapshot.ModifiedAt should not be zero")
	}

	// 验证 MD5 正确性
	expectedMD5, _ := CalculateMD5(testFile)
	if snapshot.MD5 != expectedMD5 {
		t.Errorf("Snapshot.MD5 = %s, want %s", snapshot.MD5, expectedMD5)
	}

	// 测试不存在的文件
	_, err = CreateSnapshot(filepath.Join(tmpDir, "nonexistent.txt"))
	if err == nil {
		t.Error("CreateSnapshot() should fail for nonexistent file")
	}
}

func TestFileSnapshot_Validate(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建测试文件
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Original content")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// 创建快照
	snapshot, err := CreateSnapshot(testFile)
	if err != nil {
		t.Fatalf("CreateSnapshot() failed: %v", err)
	}

	// 测试未修改的文件
	valid, err := snapshot.Validate()
	if err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if !valid {
		t.Error("Validate() should return true for unmodified file")
	}

	// 修改文件内容
	time.Sleep(10 * time.Millisecond) // 确保时间戳变化
	newContent := []byte("Modified content")
	if err := os.WriteFile(testFile, newContent, 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// 测试修改后的文件
	valid, err = snapshot.Validate()
	if err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if valid {
		t.Error("Validate() should return false for modified file")
	}

	// 测试删除的文件
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("Failed to remove test file: %v", err)
	}

	valid, err = snapshot.Validate()
	if err == nil {
		t.Error("Validate() should return error for deleted file")
	}
}

func TestFileSnapshot_ValidateSizeChange(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建测试文件
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Short")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// 创建快照
	snapshot, err := CreateSnapshot(testFile)
	if err != nil {
		t.Fatalf("CreateSnapshot() failed: %v", err)
	}

	// 修改文件大小但保持相同的修改时间（手动构造场景）
	longerContent := []byte("Much longer content")
	if err := os.WriteFile(testFile, longerContent, 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// 验证应该失败（大小改变）
	valid, err := snapshot.Validate()
	if err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if valid {
		t.Error("Validate() should return false when file size changes")
	}
}

func TestFileSnapshot_ValidateMD5Only(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建测试文件
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Test")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// 创建快照
	snapshot, err := CreateSnapshot(testFile)
	if err != nil {
		t.Fatalf("CreateSnapshot() failed: %v", err)
	}

	// 使用相同大小的不同内容覆盖文件
	sameSize := []byte("Diff") // 相同长度，不同内容
	if err := os.WriteFile(testFile, sameSize, 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// 如果大小和修改时间相同，但内容不同，Validate 应该通过快速检查
	// 然后在 MD5 检查时失败
	valid, err := snapshot.Validate()
	if err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if valid {
		t.Error("Validate() should detect content change via MD5")
	}
}

func TestCalculateMD5_LargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large file test in short mode")
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "large.bin")

	// 创建 10MB 的测试文件
	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create large file: %v", err)
	}

	// 写入重复数据
	chunk := make([]byte, 1024*1024) // 1MB
	for i := 0; i < len(chunk); i++ {
		chunk[i] = byte(i % 256)
	}
	for i := 0; i < 10; i++ {
		if _, err := file.Write(chunk); err != nil {
			file.Close()
			t.Fatalf("Failed to write large file: %v", err)
		}
	}
	file.Close()

	// 计算 MD5
	md5sum, err := CalculateMD5(testFile)
	if err != nil {
		t.Fatalf("CalculateMD5() failed for large file: %v", err)
	}

	if len(md5sum) != 32 {
		t.Errorf("MD5 length = %d, want 32", len(md5sum))
	}
}
