package file

import (
	"fmt"
	"path/filepath"
	"strings"
)

// NormalizePath 标准化路径
// - 转换为绝对路径
// - 统一使用正斜杠
// - 清理 .. 和 .
func NormalizePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	// 转换为绝对路径
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	// 清理路径
	cleanPath := filepath.Clean(absPath)

	// 统一使用正斜杠
	normalizedPath := filepath.ToSlash(cleanPath)

	return normalizedPath, nil
}

// NormalizeRelativePath 标准化相对路径
// - 统一使用正斜杠
// - 清理 .. 和 .
// - 去除前导 ./ 和 /
func NormalizeRelativePath(path string) string {
	// 手动替换反斜杠为正斜杠（跨平台兼容）
	path = strings.ReplaceAll(path, "\\", "/")

	// 清理路径
	cleanPath := filepath.Clean(path)

	// 再次统一使用正斜杠（filepath.Clean 可能引入平台分隔符）
	normalizedPath := strings.ReplaceAll(cleanPath, "\\", "/")

	// 去除前导 ./
	normalizedPath = strings.TrimPrefix(normalizedPath, "./")

	// 去除前导 /
	normalizedPath = strings.TrimPrefix(normalizedPath, "/")

	return normalizedPath
}

// IsSubPath 检查 child 是否是 parent 的子路径
func IsSubPath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)

	// 确保 parent 以路径分隔符结尾
	if !strings.HasSuffix(parent, string(filepath.Separator)) {
		parent += string(filepath.Separator)
	}

	return strings.HasPrefix(child, parent)
}

// PreventPathTraversal 防止路径穿越攻击
// 检查 path 是否试图逃出 baseDir
func PreventPathTraversal(baseDir, path string) bool {
	// 转换为绝对路径
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}

	// 如果 path 已经是绝对路径，直接使用
	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		// 相对路径则基于 baseDir 计算
		absPath, err = filepath.Abs(filepath.Join(baseDir, path))
		if err != nil {
			return false
		}
	}

	// 检查是否是子路径
	return IsSubPath(absBase, absPath)
}
