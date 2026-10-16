package cache

import (
	"testing"
)

func TestKey(t *testing.T) {
	tests := []struct {
		parts []string
		want  string
	}{
		{[]string{"user", "123", "profile"}, "user:123:profile"},
		{[]string{"single"}, "single"},
		{[]string{"a", "b", "c", "d"}, "a:b:c:d"},
		{[]string{}, ""},
	}

	for _, tt := range tests {
		got := Key(tt.parts...)
		if got != tt.want {
			t.Errorf("Key(%v) = %q, want %q", tt.parts, got, tt.want)
		}
	}
}

func TestHashKey(t *testing.T) {
	// 同样的输入应该产生同样的 hash
	k1 := HashKey("search", "keyword", 1, 20)
	k2 := HashKey("search", "keyword", 1, 20)
	if k1 != k2 {
		t.Errorf("HashKey should be deterministic: %q != %q", k1, k2)
	}

	// 不同的输入应该产生不同的 hash
	k3 := HashKey("search", "different", 1, 20)
	if k1 == k3 {
		t.Error("HashKey should produce different results for different inputs")
	}

	// 应该包含前缀
	if len(k1) < 7 || k1[:6] != "search" {
		t.Errorf("HashKey should start with prefix, got %q", k1)
	}

	// 应该包含冒号分隔符
	if k1[6] != ':' {
		t.Errorf("HashKey should have ':' after prefix, got %q", k1)
	}
}

func TestUserKey(t *testing.T) {
	tests := []struct {
		userID int64
		suffix string
		want   string
	}{
		{123, "profile", "user:123:profile"},
		{1, "settings", "user:1:settings"},
		{0, "test", "user:0:test"},
	}

	for _, tt := range tests {
		got := UserKey(tt.userID, tt.suffix)
		if got != tt.want {
			t.Errorf("UserKey(%d, %q) = %q, want %q", tt.userID, tt.suffix, got, tt.want)
		}
	}
}

func TestTenantKey(t *testing.T) {
	tests := []struct {
		tenantID string
		suffix   string
		want     string
	}{
		{"tenant1", "config", "tenant:tenant1:config"},
		{"abc", "data", "tenant:abc:data"},
	}

	for _, tt := range tests {
		got := TenantKey(tt.tenantID, tt.suffix)
		if got != tt.want {
			t.Errorf("TenantKey(%q, %q) = %q, want %q", tt.tenantID, tt.suffix, got, tt.want)
		}
	}
}
