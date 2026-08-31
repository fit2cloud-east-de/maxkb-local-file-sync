package credential

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bearer token",
			input:    "Authorization: Bearer abc123def456",
			expected: "Authorization: Bearer ***",
		},
		{
			name:     "Authorization header",
			input:    "Authorization: Token xyz789",
			expected: "Authorization: ***",
		},
		{
			name:     "URL token parameter",
			input:    "https://api.example.com/v1/upload?token=secret123&file=test.pdf",
			expected: "https://api.example.com/v1/upload?token=***&file=test.pdf",
		},
		{
			name:     "JSON token field",
			input:    `{"token":"abc123","name":"test"}`,
			expected: `{"token":"***","name":"test"}`,
		},
		{
			name:     "Multiple sensitive fields",
			input:    `{"api_key":"key123","password":"pass456","user":"john"}`,
			expected: `{"api_key":"***","password":"***","user":"john"}`,
		},
		{
			name:     "AWS signature",
			input:    "url?X-Amz-Signature=abcd1234&bucket=test",
			expected: "url?X-Amz-Signature=***&bucket=test",
		},
		{
			name:     "AWS access key",
			input:    "AWS_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE",
			expected: "AWS_ACCESS_KEY=AWS_KEY_***",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "No sensitive data",
			input:    "This is a normal log message",
			expected: "This is a normal log message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Sanitize(tt.input)
			if result != tt.expected {
				t.Errorf("Sanitize() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSanitizeError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "Error with token",
			err:      errors.New("failed to authenticate with token abc123"),
			expected: "failed to authenticate with token ***",
		},
		{
			name:     "Nil error",
			err:      nil,
			expected: "",
		},
		{
			name:     "Error without sensitive data",
			err:      errors.New("file not found"),
			expected: "file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeError(tt.err)
			if result != tt.expected {
				t.Errorf("SanitizeError() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "URL with token",
			url:      "https://api.example.com/upload?token=secret123&file=test.pdf",
			expected: "https://api.example.com/upload?token=***&file=test.pdf",
		},
		{
			name:     "URL with signature",
			url:      "https://s3.amazonaws.com/bucket/file?X-Amz-Signature=abc123",
			expected: "https://s3.amazonaws.com/bucket/file?X-Amz-Signature=***",
		},
		{
			name:     "URL without query params",
			url:      "https://api.example.com/upload",
			expected: "https://api.example.com/upload",
		},
		{
			name:     "Empty URL",
			url:      "",
			expected: "",
		},
		{
			name:     "URL without sensitive params",
			url:      "https://api.example.com/list?page=1&limit=10",
			expected: "https://api.example.com/list?page=1&limit=10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeURL(tt.url)
			if result != tt.expected {
				t.Errorf("SanitizeURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSanitizeMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "Map with token",
			input: map[string]interface{}{
				"token": "abc123",
				"user":  "john",
			},
			expected: map[string]interface{}{
				"token": "***",
				"user":  "john",
			},
		},
		{
			name: "Nested map",
			input: map[string]interface{}{
				"config": map[string]interface{}{
					"api_key": "secret123",
					"timeout": 30,
				},
				"name": "test",
			},
			expected: map[string]interface{}{
				"config": map[string]interface{}{
					"api_key": "***",
					"timeout": 30,
				},
				"name": "test",
			},
		},
		{
			name:     "Nil map",
			input:    nil,
			expected: nil,
		},
		{
			name:     "Empty map",
			input:    map[string]interface{}{},
			expected: map[string]interface{}{},
		},
		{
			name: "String value with token",
			input: map[string]interface{}{
				"error": "Authentication failed with token=xyz789",
			},
			expected: map[string]interface{}{
				"error": "Authentication failed with token=***",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeMap(tt.input)
			if !mapsEqual(result, tt.expected) {
				t.Errorf("SanitizeMap() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func mapsEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for key, aVal := range a {
		bVal, ok := b[key]
		if !ok {
			return false
		}
		if aMap, ok := aVal.(map[string]interface{}); ok {
			if bMap, ok := bVal.(map[string]interface{}); ok {
				if !mapsEqual(aMap, bMap) {
					return false
				}
			} else {
				return false
			}
		} else if aVal != bVal {
			return false
		}
	}
	return true
}

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"trim trailing slash", " https://example.test/// ", "https://example.test", true},
		{"http allowed", "http://127.0.0.1:8080/api", "http://127.0.0.1:8080/api", true},
		{"missing scheme", "example.test", "", false},
		{"unsupported scheme", "file:///tmp/x", "", false},
		{"userinfo rejected", "https://user:pass@example.test", "", false},
		{"query rejected", "https://example.test/?token=fake", "", false},
		{"fragment rejected", "https://example.test/#frag", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateBaseURL(tt.in)
			if (err == nil) != tt.ok || got != tt.want {
				t.Fatalf("ValidateBaseURL(%q) = %q, %v; want %q, ok=%v", tt.in, got, err, tt.want, tt.ok)
			}
		})
	}
}

func TestSanitizeSecrets(t *testing.T) {
	secret := "fake-maxkb-token-123"
	got := SanitizeSecrets("request failed: "+secret+" Authorization: Bearer "+secret, secret)
	if strings.Contains(got, secret) {
		t.Fatalf("secret leaked: %q", got)
	}
}

func TestMask(t *testing.T) {
	if got := Mask("fake"); got != MaskedValue || !IsMasked(got) || IsMasked("fake") {
		t.Fatalf("mask contract failed")
	}
}
