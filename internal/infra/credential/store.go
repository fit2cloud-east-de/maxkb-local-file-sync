package credential

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/zalando/go-keyring"
)

const serviceName = "maxkb-local-file-sync"

// Credential keys are deliberately kept in one place so callers cannot
// accidentally persist a secret under a setting/database key.
const (
	MaxKBAPIKey         = "maxkb_api_key"
	MinerUAPIKey        = "mineru_api_key"
	legacyMaxKBBaseURL  = "maxkb_base_url"
	legacyMinerUBaseURL = "mineru_base_url"
	legacyMinerUMode    = "mineru_mode"
)

// MaskedValue is the only representation of a stored credential that may be
// returned to the renderer. It is intentionally not the real value and can be
// sent back by the UI to mean “keep the existing credential”.
const MaskedValue = "••••••••••••••••"

func Mask(_ string) string { return MaskedValue }

func IsMasked(value string) bool {
	return strings.TrimSpace(value) == MaskedValue || strings.HasPrefix(strings.TrimSpace(value), "•••")
}

// ValidateBaseURL validates and normalizes a user-provided service URL. Query
// strings, fragments and embedded userinfo are rejected because they are a
// common way for credentials to accidentally leak into logs or snapshots.
func ValidateBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("base URL is required")
	}
	for _, r := range raw {
		if unicode.IsSpace(r) && r != ' ' {
			return "", fmt.Errorf("base URL contains control whitespace")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid base URL")
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("base URL must use HTTP or HTTPS")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("base URL must not contain userinfo, query, or fragment")
	}
	return strings.TrimRight(raw, "/"), nil
}

// Store 凭据存储接口
type Store interface {
	// Set 保存凭据
	Set(key, value string) error
	// Get 读取凭据
	Get(key string) (string, error)
	// Delete 删除凭据
	Delete(key string) error
}

// keyringStore 使用系统密钥链的凭据存储
type keyringStore struct{}

// NewStore 创建凭据存储实例（使用系统密钥链：macOS Keychain / Windows Credential Manager / Linux Secret Service）
func NewStore() (Store, error) {
	return &keyringStore{}, nil
}

func (s *keyringStore) Set(key, value string) error {
	if err := keyring.Set(serviceName, key, value); err != nil {
		return fmt.Errorf("failed to set credential %q: %w", key, err)
	}
	return nil
}

func (s *keyringStore) Get(key string) (string, error) {
	val, err := keyring.Get(serviceName, key)
	if err != nil {
		if err == keyring.ErrNotFound {
			return "", nil
		}
		return "", fmt.Errorf("failed to get credential %q: %w", key, err)
	}
	return val, nil
}

func (s *keyringStore) Delete(key string) error {
	if err := keyring.Delete(serviceName, key); err != nil {
		if err == keyring.ErrNotFound {
			return nil
		}
		return fmt.Errorf("failed to delete credential %q: %w", key, err)
	}
	return nil
}
