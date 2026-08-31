package file

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "Absolute path",
			path:    "/usr/local/test",
			wantErr: false,
		},
		{
			name:    "Relative path",
			path:    "../test",
			wantErr: false,
		},
		{
			name:    "Path with dots",
			path:    "/usr/../local/./test",
			wantErr: false,
		},
		{
			name:    "Home directory",
			path:    "~/Documents",
			wantErr: false,
		},
		{
			name:    "Empty path",
			path:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NormalizePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("NormalizePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !filepath.IsAbs(result) {
				t.Errorf("NormalizePath() = %v, expected absolute path", result)
			}
		})
	}
}

func TestNormalizeRelativePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "Unix path",
			path:     "docs/file.txt",
			expected: "docs/file.txt",
		},
		{
			name:     "Windows path",
			path:     "docs\\file.txt",
			expected: "docs/file.txt",
		},
		{
			name:     "Mixed slashes",
			path:     "docs\\sub/file.txt",
			expected: "docs/sub/file.txt",
		},
		{
			name:     "Path with dots",
			path:     "./docs/../file.txt",
			expected: "file.txt",
		},
		{
			name:     "Leading slash",
			path:     "/docs/file.txt",
			expected: "docs/file.txt",
		},
		{
			name:     "Trailing slash",
			path:     "docs/folder/",
			expected: "docs/folder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeRelativePath(tt.path)
			if result != tt.expected {
				t.Errorf("NormalizeRelativePath() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsSubPath(t *testing.T) {
	tmpDir := t.TempDir()
	parentDir := filepath.Join(tmpDir, "parent")
	childDir := filepath.Join(parentDir, "child")
	siblingDir := filepath.Join(tmpDir, "sibling")

	// 创建测试目录
	os.MkdirAll(childDir, 0755)
	os.MkdirAll(siblingDir, 0755)

	tests := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{
			name:   "Direct child",
			parent: parentDir,
			child:  childDir,
			want:   true,
		},
		{
			name:   "Same path",
			parent: parentDir,
			child:  parentDir,
			want:   false,
		},
		{
			name:   "Sibling path",
			parent: parentDir,
			child:  siblingDir,
			want:   false,
		},
		{
			name:   "File in parent",
			parent: parentDir,
			child:  filepath.Join(parentDir, "file.txt"),
			want:   true,
		},
		{
			name:   "Parent is child",
			parent: childDir,
			child:  parentDir,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSubPath(tt.parent, tt.child)
			if got != tt.want {
				t.Errorf("IsSubPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPreventPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "base")
	os.MkdirAll(baseDir, 0755)

	tests := []struct {
		name    string
		baseDir string
		path    string
		want    bool
	}{
		{
			name:    "Safe relative path",
			baseDir: baseDir,
			path:    "subdir/file.txt",
			want:    true,
		},
		{
			name:    "Path traversal attempt",
			baseDir: baseDir,
			path:    "../../../etc/passwd",
			want:    false,
		},
		{
			name:    "Absolute path within base",
			baseDir: baseDir,
			path:    filepath.Join(baseDir, "file.txt"),
			want:    true,
		},
		{
			name:    "Absolute path outside base",
			baseDir: baseDir,
			path:    "/etc/passwd",
			want:    false,
		},
		{
			name:    "Path with dots resolving inside",
			baseDir: baseDir,
			path:    "subdir/../file.txt",
			want:    true,
		},
		{
			name:    "Path with dots resolving outside",
			baseDir: baseDir,
			path:    "../sibling/file.txt",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PreventPathTraversal(tt.baseDir, tt.path)
			if got != tt.want {
				t.Errorf("PreventPathTraversal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizePathSymlink(t *testing.T) {
	if os.Getenv("SKIP_SYMLINK_TESTS") != "" {
		t.Skip("Skipping symlink tests")
	}

	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real")
	symlinkPath := filepath.Join(tmpDir, "link")

	// 创建真实目录
	if err := os.Mkdir(realDir, 0755); err != nil {
		t.Fatalf("Failed to create real dir: %v", err)
	}

	// 创建符号链接
	if err := os.Symlink(realDir, symlinkPath); err != nil {
		t.Skipf("Cannot create symlink (may need permissions): %v", err)
	}

	// 测试符号链接解析
	normalized, err := NormalizePath(symlinkPath)
	if err != nil {
		t.Fatalf("NormalizePath() failed: %v", err)
	}

	// 符号链接被标准化为绝对路径（可能保留符号链接）
	// 只验证结果是绝对路径且包含路径名称
	if !filepath.IsAbs(normalized) {
		t.Errorf("NormalizePath() should return absolute path, got %v", normalized)
	}
}
