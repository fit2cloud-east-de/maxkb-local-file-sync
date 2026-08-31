package credential

import (
	"regexp"
	"strings"
)

// SanitizeError 脱敏错误信息中的敏感数据
func SanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return Sanitize(err.Error())
}

// Sanitize 脱敏字符串中的敏感数据
func Sanitize(text string) string {
	if text == "" {
		return ""
	}

	sanitized := text

	// 脱敏 API Key (Bearer token). Keep the scheme but never retain the value.
	bearerRegex := regexp.MustCompile(`(?i)Bearer\s+[^\s,;"']+`)
	sanitized = bearerRegex.ReplaceAllString(sanitized, "Bearer ***")

	// 脱敏 Authorization header（非 Bearer 类型）
	authRegex := regexp.MustCompile(`(?i)authorization\s*[:=]\s*(?:Token|Basic|Digest)\s+[^\s,;"']+`)
	sanitized = authRegex.ReplaceAllString(sanitized, "Authorization: ***")

	// 脱敏 token 参数（独立的 token 值，不在 URL 参数中）
	tokenValueRegex := regexp.MustCompile(`\btoken\s+[A-Za-z0-9_\-\.]+`)
	sanitized = tokenValueRegex.ReplaceAllString(sanitized, "token ***")

	// 脱敏 URL/表单中的 token 参数
	tokenRegex := regexp.MustCompile(`(?i)(access_token|refresh_token|user_key|api[-_]?key|token|secret|password)=([^&\s]+)`)
	sanitized = tokenRegex.ReplaceAllString(sanitized, "$1=***")

	// 脱敏 JSON 中的敏感字段
	jsonTokenRegex := regexp.MustCompile(`(?i)"(authorization|access_token|refresh_token|user_key|token|api[-_]?key|secret|password)"\s*:\s*"[^"]+"`)
	sanitized = jsonTokenRegex.ReplaceAllString(sanitized, `"$1":"***"`)

	// 脱敏预签名 URL 的签名参数
	signatureRegex := regexp.MustCompile(`(?i)(^|[?&])(signature|sig|sign|X-Amz-Signature)=[^&\s]+`)
	sanitized = signatureRegex.ReplaceAllString(sanitized, "$1$2=***")

	// 脱敏 AWS 凭证
	awsKeyRegex := regexp.MustCompile(`(?i)(AKIA|AWS)[A-Z0-9]{16,}`)
	sanitized = awsKeyRegex.ReplaceAllString(sanitized, "AWS_KEY_***")

	return sanitized
}

// SanitizeSecrets additionally removes exact secret values supplied by the
// caller. This is used at boundaries where a third-party error may echo a
// credential in an otherwise unstructured message.
func SanitizeSecrets(text string, secrets ...string) string {
	result := Sanitize(text)
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret != "" && !IsMasked(secret) {
			result = strings.ReplaceAll(result, secret, "***")
		}
	}
	return result
}

// SanitizeURL 脱敏 URL 中的敏感参数
func SanitizeURL(url string) string {
	if url == "" {
		return ""
	}

	// 保留协议、域名和路径，脱敏查询参数
	parts := strings.SplitN(url, "?", 2)
	if len(parts) < 2 {
		return url
	}

	baseURL := parts[0]
	queryString := parts[1]

	// 脱敏查询参数
	sanitizedQuery := Sanitize(queryString)

	return baseURL + "?" + sanitizedQuery
}

// SanitizeMap 脱敏 map 中的敏感字段
func SanitizeMap(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}

	result := make(map[string]interface{})
	sensitiveKeys := map[string]bool{
		"token":     true,
		"api_key":   true,
		"apikey":    true,
		"secret":    true,
		"password":  true,
		"signature": true,
		"sig":       true,
		"sign":      true,
	}

	for key, value := range data {
		lowerKey := strings.ToLower(key)
		if sensitiveKeys[lowerKey] {
			result[key] = "***"
		} else if strValue, ok := value.(string); ok {
			result[key] = Sanitize(strValue)
		} else if mapValue, ok := value.(map[string]interface{}); ok {
			result[key] = SanitizeMap(mapValue)
		} else {
			result[key] = value
		}
	}

	return result
}
