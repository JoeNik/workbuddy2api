package upstream

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// uuidV4Re 官方会话标识形态：36 字符带横线 UUID v4（第 13 位恒 4、variant 段首位 8/9/a/b）。
// issue #51：上游出站 X-Conversation-ID / prompt_cache_key 的会话段都是这个形态，
// 带项目前缀或明文 uid 的串一眼就能被筛出来。
var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// extractCacheKey 从改写后的 body 里取出 prompt_cache_key 字段值。
func extractCacheKey(t *testing.T, body []byte) string {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	v, _ := obj["prompt_cache_key"].(string)
	return v
}

// TestInjectPromptCacheKey_PreservesExisting 覆盖任务用例 1：
// 入站 body 已带 prompt_cache_key → 原值保留，不被网关覆盖。
func TestInjectPromptCacheKey_PreservesExisting(t *testing.T) {
	// Arrange
	uid := "user-abc-123"
	conv := "conv-xyz"
	in := `{"model":"glm-5.2","messages":[],"prompt_cache_key":"client-set-key"}`

	// Act
	out := InjectPromptCacheKey([]byte(in), uid, conv)

	// Assert
	if got := extractCacheKey(t, out); got != "client-set-key" {
		t.Fatalf("existing prompt_cache_key overwritten: got=%q want client-set-key", got)
	}
}

// TestInjectPromptCacheKey_UsesConversationID 覆盖任务用例 2：
// body 无 prompt_cache_key 但有 conversation_id → 用它做会话哈希源。
// 格式为**官方同形 UUID v4**（36 字符带横线）：conversation_id 不逐字出现，而是驱动
// 哈希段（同 id 同 key、不同 id 不同 key）；uid 只作为哈希输入参与隔离，**不明文出现**。
func TestInjectPromptCacheKey_UsesConversationID(t *testing.T) {
	uid := "user-abc-123"
	in := `{"model":"glm-5.2","messages":[],"conversation_id":"conv-42"}`

	out := InjectPromptCacheKey([]byte(in), uid, "")
	got := extractCacheKey(t, out)
	if !uuidV4Re.MatchString(got) {
		t.Fatalf("expected UUID v4 shape, got=%q", got)
	}
	if strings.Contains(got, "user-abc") {
		t.Fatalf("uid must not appear in plaintext, got=%q", got)
	}
	// 同 conversation_id 两次出站 → 同 key（稳定）。
	out2 := InjectPromptCacheKey([]byte(in), uid, "")
	if got2 := extractCacheKey(t, out2); got2 != got {
		t.Fatalf("non-deterministic key for same conversation_id: %q vs %q", got, got2)
	}
	// 不同 conversation_id → 不同 key（哈希段区分）。
	inB := `{"model":"glm-5.2","messages":[],"conversation_id":"conv-99"}`
	gotB := extractCacheKey(t, InjectPromptCacheKey([]byte(inB), uid, ""))
	if gotB == got {
		t.Fatalf("different conversation_id produced same key: %q", got)
	}
}

// TestInjectPromptCacheKey_GeneratesStableKey 覆盖任务用例 3：
// body 两者都无 → 网关生成稳定键，两次同输入同账号得到同 key。
func TestInjectPromptCacheKey_GeneratesStableKey(t *testing.T) {
	uid := "user-abc-123"
	conv := "conv-stable"
	in := `{"model":"glm-5.2","messages":[]}`

	a := InjectPromptCacheKey([]byte(in), uid, conv)
	b := InjectPromptCacheKey([]byte(in), uid, conv)
	ka, kb := extractCacheKey(t, a), extractCacheKey(t, b)
	if ka != kb {
		t.Fatalf("non-deterministic key for same input+uid: a=%q b=%q", ka, kb)
	}
}

// TestInjectPromptCacheKey_AccountIsolation 覆盖任务用例 4：
// 不同账号 → key 不同（隔离验证）。
func TestInjectPromptCacheKey_AccountIsolation(t *testing.T) {
	conv := "conv-shared"
	in := `{"model":"glm-5.2","messages":[]}`

	ka := extractCacheKey(t, InjectPromptCacheKey([]byte(in), "user-aaa-111", conv))
	kb := extractCacheKey(t, InjectPromptCacheKey([]byte(in), "user-bbb-222", conv))
	if ka == kb {
		t.Fatalf("cross-account key collision: both=%q", ka)
	}
	if !uuidV4Re.MatchString(ka) || !uuidV4Re.MatchString(kb) {
		t.Fatalf("keys must be UUID v4 shape: a=%q b=%q", ka, kb)
	}
}

// TestInjectPromptCacheKey_Format 覆盖任务用例 5：
// cache key 格式校验：官方同形 UUID v4，无项目前缀、无明文 uid（issue #51）。
func TestInjectPromptCacheKey_Format(t *testing.T) {
	uid := "1234567890abcdef"
	in := `{"model":"glm-5.2","messages":[]}`
	out := InjectPromptCacheKey([]byte(in), uid, "conv-fmt")
	got := extractCacheKey(t, out)
	if !uuidV4Re.MatchString(got) {
		t.Fatalf("expected UUID v4 shape, got=%q", got)
	}
	if strings.HasPrefix(got, "wb2a-") {
		t.Fatalf("key must not carry project prefix, got=%q", got)
	}
	if strings.Contains(got, "12345678") {
		t.Fatalf("uid must not appear in plaintext, got=%q", got)
	}
}

// TestInjectPromptCacheKey_EmptyConversation 覆盖任务用例 3 边界：
// 无 conversation_id 且无会话标识 → 生成键含 uid8 但 conversationHash 段为定值（不复用前缀）。
// 不应报错、不应空串（key 非空）。
func TestInjectPromptCacheKey_EmptyConversation(t *testing.T) {
	uid := "user-abc-123"
	in := `{"model":"glm-5.2","messages":[]}`
	out := InjectPromptCacheKey([]byte(in), uid, "")
	got := extractCacheKey(t, out)
	if got == "" {
		t.Fatalf("expected non-empty key when no conversation, got empty")
	}
	if !uuidV4Re.MatchString(got) {
		t.Fatalf("expected UUID v4 shape, got=%q", got)
	}
	if strings.Contains(got, "user-abc") {
		t.Fatalf("uid must not appear in plaintext, got=%q", got)
	}
}

// TestPrepareBodyOptNoCacheKeyInjection 覆盖任务用例 6：
// PrepareBodyOpt（sanitize=false 的旧入口）行为不变——不注入 cache key。
// 向后兼容：仅传 body 不给 cacheKey 上下文时，不得引入新字段。
func TestPrepareBodyOptNoCacheKeyInjection(t *testing.T) {
	in := `{"model":"glm-5.2","messages":[]}`
	out := PrepareBodyOpt([]byte(in), false)
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := obj["prompt_cache_key"]; present {
		t.Fatalf("PrepareBodyOpt must NOT inject prompt_cache_key, but got: %v", obj["prompt_cache_key"])
	}
}
