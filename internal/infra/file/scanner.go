package file

import (
	"os"
	"path/filepath"
	"strings"
)

// ScanDirectory 扫描目录获取所有文件的相对路径
func ScanDirectory(rootPath string) ([]string, error) {
	var files []string

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录
		if info.IsDir() {
			return nil
		}

		// 计算相对路径
		relPath, err := filepath.Rel(rootPath, path)
		if err != nil {
			return err
		}

		// 规范化相对路径
		relPath = NormalizeRelativePath(relPath)

		// 跳过隐藏文件和系统文件
		if shouldSkipFile(relPath) {
			return nil
		}

		files = append(files, relPath)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}

// shouldSkipFile 判断是否应跳过文件
func shouldSkipFile(relPath string) bool {
	// 跳过隐藏文件
	if strings.HasPrefix(filepath.Base(relPath), ".") {
		return true
	}

	// 跳过系统文件
	systemFiles := []string{
		"Thumbs.db",
		"Desktop.ini",
		".DS_Store",
	}
	baseName := filepath.Base(relPath)
	for _, sysFile := range systemFiles {
		if baseName == sysFile {
			return true
		}
	}

	// 跳过临时文件
	if strings.HasSuffix(baseName, "~") || strings.HasSuffix(baseName, ".tmp") {
		return true
	}

	return false
}

// MaxKBDirectUploadExtensions 是 MaxKB 智能分段接口允许直接上传的
// 文件扩展名。扩展名统一使用小写并带前导点。需要通过 MinerU 转换的
// 文件不应加入此列表，而应由上层根据任务配置决定是否转换。
var maxKBDirectUploadExtensions = map[string]struct{}{
	".txt":      {},
	".md":       {},
	".markdown": {},
	".pdf":      {},
	".docx":     {},
	".html":     {},
	".xls":      {},
	".xlsx":     {},
	".csv":      {},
	".zip":      {},
}

// IsMaxKBDirectUploadSupported 判断文件是否属于 MaxKB 支持直接上传的格式。
func IsMaxKBDirectUploadSupported(filePath string) bool {
	_, ok := maxKBDirectUploadExtensions[strings.ToLower(filepath.Ext(filePath))]
	return ok
}

// MatchExtension 检查文件是否匹配扩展名列表
func MatchExtension(filePath string, extensions []string) bool {
	if len(extensions) == 0 {
		return true // 空列表表示匹配所有文件
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	for _, e := range extensions {
		if ext == strings.ToLower(e) {
			return true
		}
	}
	return false
}
