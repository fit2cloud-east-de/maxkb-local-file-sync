package file

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PatternMatcher 文件模式匹配器
type PatternMatcher struct {
	includePatterns []string
	excludePatterns []string
}

// NewPatternMatcher 创建新的模式匹配器
// includePatterns: 逗号分隔的包含模式列表，支持 glob 语法（*, **）
// excludePatterns: 逗号分隔的排除模式列表，支持 glob 语法（*, **）
// 空的 includePatterns 表示匹配所有文件
// excludePatterns 优先级高于 includePatterns
func NewPatternMatcher(includePatterns, excludePatterns string) (*PatternMatcher, error) {
	matcher := &PatternMatcher{
		includePatterns: parsePatterns(includePatterns),
		excludePatterns: parsePatterns(excludePatterns),
	}
	return matcher, nil
}

// parsePatterns 解析逗号分隔的模式列表
func parsePatterns(patterns string) []string {
	if patterns == "" {
		return nil
	}

	parts := strings.Split(patterns, ",")
	result := make([]string, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			// 规范化路径分隔符为 /
			p = filepath.ToSlash(p)
			result = append(result, p)
		}
	}

	return result
}

// Match 判断文件路径是否匹配规则
// relPath: 相对路径（已规范化为 / 分隔符）
func (m *PatternMatcher) Match(relPath string) bool {
	// 规范化路径为 / 分隔符
	relPath = filepath.ToSlash(relPath)

	// 1. 检查排除规则（优先级最高）
	for _, pattern := range m.excludePatterns {
		if matchPattern(pattern, relPath) {
			return false
		}
	}

	// 2. 检查包含规则
	// 如果没有包含规则，默认匹配所有
	if len(m.includePatterns) == 0 {
		return true
	}

	// 如果有包含规则，必须至少匹配一个
	for _, pattern := range m.includePatterns {
		if matchPattern(pattern, relPath) {
			return true
		}
	}

	return false
}

// matchPattern 匹配单个 glob 模式
// 支持:
//   - * : 匹配单个路径段中的任意字符（不跨越目录分隔符）
//   - ** : 匹配任意深度的目录结构
//   - 普通字符串: 精确匹配
func matchPattern(pattern, path string) bool {
	// 处理 ** 通配符
	if strings.Contains(pattern, "**") {
		return matchDoubleStarPattern(pattern, path)
	}

	// 使用 filepath.Match 处理单个 * 通配符
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		// 如果模式无效，返回 false
		return false
	}

	// 如果没有直接匹配，尝试匹配路径的任意部分（用于目录匹配）
	if !matched && strings.Contains(pattern, "*") {
		// 尝试匹配路径的开头部分（例如 "docs/*" 匹配 "docs/file.txt"）
		parts := strings.Split(path, "/")
		for i := range parts {
			subPath := strings.Join(parts[:i+1], "/")
			matched, _ = filepath.Match(pattern, subPath)
			if matched {
				return true
			}
		}
	}

	return matched
}

// matchDoubleStarPattern 处理包含 ** 的模式
func matchDoubleStarPattern(pattern, path string) bool {
	// 将模式分割为 ** 前后的部分
	parts := strings.Split(pattern, "**")
	if len(parts) > 2 {
		// 多个 ** 的情况，递归处理
		// 简化实现：将多个 ** 合并为一个
		pattern = strings.ReplaceAll(pattern, "**/", "")
		pattern = strings.ReplaceAll(pattern, "**", "*")
		return matchPattern(pattern, path)
	}

	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := strings.TrimPrefix(parts[1], "/")

	// 检查前缀
	if prefix != "" {
		if !strings.HasPrefix(path, prefix) && prefix != "*" {
			prefixMatched, _ := filepath.Match(prefix, path)
			if !prefixMatched {
				// 尝试匹配路径的开头部分
				pathParts := strings.Split(path, "/")
				matched := false
				for i := range pathParts {
					subPath := strings.Join(pathParts[:i+1], "/")
					m, _ := filepath.Match(prefix, subPath)
					if m {
						matched = true
						break
					}
				}
				if !matched {
					return false
				}
			}
		}
	}

	// 检查后缀
	if suffix != "" {
		if suffix == "*" {
			return true
		}
		suffixMatched, _ := filepath.Match(suffix, path)
		if !suffixMatched {
			// 尝试匹配路径的末尾部分
			pathParts := strings.Split(path, "/")
			matched := false
			for i := range pathParts {
				subPath := strings.Join(pathParts[i:], "/")
				m, _ := filepath.Match(suffix, subPath)
				if m {
					matched = true
					break
				}
			}
			return matched
		}
	}

	return true
}

// ParseExtensions 解析逗号分隔的文件扩展名列表
// 返回规范化的扩展名列表（小写，带前导 .）
func ParseExtensions(extensions string) []string {
	if extensions == "" {
		return nil
	}

	parts := strings.Split(extensions, ",")
	result := make([]string, 0, len(parts))

	for _, ext := range parts {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}

		// 确保扩展名有前导 .
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}

		// 转换为小写
		ext = strings.ToLower(ext)

		result = append(result, ext)
	}

	return result
}

// ValidatePattern 验证模式语法是否有效
func ValidatePattern(pattern string) error {
	if pattern == "" {
		return nil
	}

	// 检查是否包含非法字符
	if strings.ContainsAny(pattern, "\x00") {
		return fmt.Errorf("pattern contains null character")
	}

	// 尝试解析模式
	parts := parsePatterns(pattern)
	for _, p := range parts {
		// 移除 ** 后验证
		testPattern := strings.ReplaceAll(p, "**", "*")
		_, err := filepath.Match(testPattern, "test")
		if err != nil {
			return fmt.Errorf("invalid pattern '%s': %w", p, err)
		}
	}

	return nil
}
