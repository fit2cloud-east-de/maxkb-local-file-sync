package file

import (
	"testing"
)

func TestParsePatterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "single pattern",
			input:    "*.pdf",
			expected: []string{"*.pdf"},
		},
		{
			name:     "multiple patterns",
			input:    "*.pdf, *.docx, *.txt",
			expected: []string{"*.pdf", "*.docx", "*.txt"},
		},
		{
			name:     "patterns with whitespace",
			input:    " *.pdf , *.docx , *.txt ",
			expected: []string{"*.pdf", "*.docx", "*.txt"},
		},
		{
			name:     "patterns with forward slash",
			input:    "docs/*.txt",
			expected: []string{"docs/*.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parsePatterns(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d patterns, got %d", len(tt.expected), len(result))
				return
			}
			for i, exp := range tt.expected {
				if result[i] != exp {
					t.Errorf("pattern[%d]: expected %q, got %q", i, exp, result[i])
				}
			}
		})
	}
}

func TestParseExtensions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "single extension with dot",
			input:    ".pdf",
			expected: []string{".pdf"},
		},
		{
			name:     "single extension without dot",
			input:    "pdf",
			expected: []string{".pdf"},
		},
		{
			name:     "multiple extensions",
			input:    ".pdf, .docx, txt",
			expected: []string{".pdf", ".docx", ".txt"},
		},
		{
			name:     "uppercase extensions",
			input:    ".PDF, .DOCX, TXT",
			expected: []string{".pdf", ".docx", ".txt"},
		},
		{
			name:     "extensions with whitespace",
			input:    " .pdf , docx , .txt ",
			expected: []string{".pdf", ".docx", ".txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseExtensions(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d extensions, got %d", len(tt.expected), len(result))
				return
			}
			for i, exp := range tt.expected {
				if result[i] != exp {
					t.Errorf("extension[%d]: expected %q, got %q", i, exp, result[i])
				}
			}
		})
	}
}

func TestPatternMatcher_Match(t *testing.T) {
	tests := []struct {
		name            string
		includePatterns string
		excludePatterns string
		path            string
		expected        bool
	}{
		// 基础测试
		{
			name:            "empty patterns match all",
			includePatterns: "",
			excludePatterns: "",
			path:            "file.txt",
			expected:        true,
		},
		{
			name:            "simple wildcard",
			includePatterns: "*.txt",
			excludePatterns: "",
			path:            "file.txt",
			expected:        true,
		},
		{
			name:            "simple wildcard no match",
			includePatterns: "*.txt",
			excludePatterns: "",
			path:            "file.pdf",
			expected:        false,
		},
		// 排除规则优先级
		{
			name:            "exclude takes precedence",
			includePatterns: "*.txt",
			excludePatterns: "test.txt",
			path:            "test.txt",
			expected:        false,
		},
		{
			name:            "exclude pattern with wildcard",
			includePatterns: "*.txt",
			excludePatterns: "test*",
			path:            "test_file.txt",
			expected:        false,
		},
		// 目录匹配
		{
			name:            "match file in directory",
			includePatterns: "docs/*.txt",
			excludePatterns: "",
			path:            "docs/readme.txt",
			expected:        true,
		},
		{
			name:            "exclude directory",
			includePatterns: "*.txt",
			excludePatterns: "temp/*",
			path:            "temp/file.txt",
			expected:        false,
		},
		// ** 通配符测试
		{
			name:            "double star matches nested",
			includePatterns: "docs/**/*.txt",
			excludePatterns: "",
			path:            "docs/sub/deep/file.txt",
			expected:        true,
		},
		{
			name:            "exclude nested directory",
			includePatterns: "*.txt",
			excludePatterns: "**/temp/*.txt",
			path:            "project/temp/file.txt",
			expected:        false,
		},
		// 多个模式
		{
			name:            "multiple include patterns",
			includePatterns: "*.txt, *.pdf",
			excludePatterns: "",
			path:            "file.pdf",
			expected:        true,
		},
		{
			name:            "multiple exclude patterns",
			includePatterns: "*.txt",
			excludePatterns: "test*, debug*",
			path:            "debug_log.txt",
			expected:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := NewPatternMatcher(tt.includePatterns, tt.excludePatterns)
			if err != nil {
				t.Fatalf("failed to create matcher: %v", err)
			}

			result := matcher.Match(tt.path)
			if result != tt.expected {
				t.Errorf("Match(%q) = %v, expected %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestValidatePattern(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		wantError bool
	}{
		{
			name:      "empty pattern",
			pattern:   "",
			wantError: false,
		},
		{
			name:      "valid wildcard",
			pattern:   "*.txt",
			wantError: false,
		},
		{
			name:      "valid double star",
			pattern:   "docs/**/*.pdf",
			wantError: false,
		},
		{
			name:      "multiple patterns",
			pattern:   "*.txt, *.pdf, docs/*",
			wantError: false,
		},
		{
			name:      "invalid bracket",
			pattern:   "[invalid",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePattern(tt.pattern)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidatePattern(%q) error = %v, wantError %v", tt.pattern, err, tt.wantError)
			}
		})
	}
}
