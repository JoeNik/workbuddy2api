package upstream

import (
	"regexp"
	"testing"

	"workbuddy2api/internal/auth"
)

// hex32Re / hex16Re 32 位与 16 位 hex 合法格式断言（B3 与消息级 ID 的合规性）。
var hex32Re = regexp.MustCompile(`^[0-9a-f]{32}$`)
var hex16Re = regexp.MustCompile(`^[0-9a-f]{16}$`)

// TestChatHeadersConversationFullMeta 全量 meta 出站头族：
// 聚合主键 X-Conversation-Request-ID 必发且等于传入值，消息级双头同值 32 hex，
// X-Root-Request-ID 回落轮级值（调用方没给会话根时），Trace 透传入站值，
// B3 跟随 trace（不跟随轮级键），Span/ParentSpan 各自独立随机。
func TestChatHeadersConversationFullMeta(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "u1"}
	req := mustRequest(t)
	c := &Client{}
	// 入站 trace 必须是合法 B3 形态（16/32 hex）才会被透传，否则按设计回落消息级 ID。
	const inboundTrace = "00112233445566778899aabbccddeeff"
	meta := ChatMeta{
		ConversationID:        "conv-1",
		ConversationRequestID: "0123456789abcdef0123456789abcdef",
		TraceID:               inboundTrace,
	}
	c.ChatHeaders(req, a, "", meta)

	if got := req.Header.Get("X-Conversation-ID"); got != "conv-1" {
		t.Errorf("X-Conversation-ID = %q want conv-1", got)
	}
	if got := req.Header.Get("X-Conversation-Request-ID"); got != meta.ConversationRequestID {
		t.Errorf("X-Conversation-Request-ID = %q want %q", got, meta.ConversationRequestID)
	}
	msgID := req.Header.Get("X-Conversation-Message-ID")
	if msgID != req.Header.Get("X-Request-ID") {
		t.Errorf("X-Conversation-Message-ID(%q) != X-Request-ID(%q)", msgID, req.Header.Get("X-Request-ID"))
	}
	if !hex32Re.MatchString(msgID) {
		t.Errorf("message ID %q not 32 hex", msgID)
	}
	if got := req.Header.Get("X-Root-Request-ID"); got != meta.ConversationRequestID {
		t.Errorf("X-Root-Request-ID = %q want %q (回落轮级值)", got, meta.ConversationRequestID)
	}
	if got := req.Header.Get("X-Trace-ID"); got != inboundTrace {
		t.Errorf("X-Trace-ID = %q want %q", got, inboundTrace)
	}
	// B3 跟着 trace 走（官方 X-B3-TraceId == X-Trace-ID），而不是 request id。
	if got := req.Header.Get("X-B3-TraceId"); got != inboundTrace {
		t.Errorf("X-B3-TraceId = %q want %q", got, inboundTrace)
	}
	// SpanId 必须独立随机：官方 span 与 trace/request 均无关系，拿 msgID[:16] 会把
	// messageID 的 12 hex 毫秒前缀带出来。
	if got := req.Header.Get("X-B3-SpanId"); got == msgID[:16] {
		t.Errorf("X-B3-SpanId must be independent from messageID")
	}
	if !hex16Re.MatchString(req.Header.Get("X-B3-SpanId")) {
		t.Errorf("X-B3-SpanId = %q not 16 hex", req.Header.Get("X-B3-SpanId"))
	}
	if got := req.Header.Get("X-B3-Sampled"); got != "1" {
		t.Errorf("X-B3-Sampled = %q want 1", got)
	}
	// 既有头零回归：common + chat 账号头仍旧在。
	if got := req.Header.Get("Authorization"); got != "Bearer at" {
		t.Errorf("Authorization = %q (regression)", got)
	}
}

// TestChatHeadersConversationRootDiffersFromTurn 会话根与会话轮级键必须**不同值**：
// 真机语义分层（根 vs 轮），旧实现让两族恒等——「恒等」本身就是网关自造的痕迹。
func TestChatHeadersConversationRootDiffersFromTurn(t *testing.T) {
	req := mustRequest(t)
	c := &Client{}
	meta := ChatMeta{
		ConversationRequestID: "0123456789abcdef0123456789abcdef",
		RootRequestID:         "fedcba9876543210fedcba9876543210",
	}
	c.ChatHeaders(req, &auth.Auth{AccessToken: "at", UID: "u1"}, "", meta)

	if got := req.Header.Get("X-Root-Request-ID"); got != meta.RootRequestID {
		t.Errorf("X-Root-Request-ID = %q want %q", got, meta.RootRequestID)
	}
	if got := req.Header.Get("X-Conversation-Request-ID"); got != meta.ConversationRequestID {
		t.Errorf("X-Conversation-Request-ID = %q want %q", got, meta.ConversationRequestID)
	}
}

// TestChatHeadersConversationNoConvID meta.ConversationID 为空且无 CacheKeyID →
// X-Conversation-ID 现造客户端同形态 v7 UUID（**不留空**：上游按三件套是否齐全做
// 渠道校验，缺任一头即报 conversation id missing）；X-Trace-ID 独立、不回落轮级键。
func TestChatHeadersConversationNoConvID(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "u1"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{ConversationRequestID: "convreq-1"})

	if got := req.Header.Get("X-Conversation-ID"); !uuidV7Re.MatchString(got) {
		t.Errorf("X-Conversation-ID = %q want UUID v7 form", got)
	}
	if got := req.Header.Get("X-Conversation-Request-ID"); got != "convreq-1" {
		t.Errorf("X-Conversation-Request-ID = %q want convreq-1", got)
	}
	if got := req.Header.Get("X-Trace-ID"); got == "convreq-1" {
		t.Errorf("X-Trace-ID must NOT fall back to convReqID")
	}
	if !hex32Re.MatchString(req.Header.Get("X-Trace-ID")) {
		t.Errorf("X-Trace-ID = %q want 32 hex", req.Header.Get("X-Trace-ID"))
	}
}

// TestChatHeadersConversationCacheKeySource 客户端没声明会话、但有网关派生的会话键 →
// X-Conversation-ID 按 (uid, 会话键) 稳定派生 v7：同会话多轮同值、跨账号不同值。
func TestChatHeadersConversationCacheKeySource(t *testing.T) {
	c := &Client{}
	meta := ChatMeta{CacheKeyID: "sess-1"}

	req1 := mustRequest(t)
	c.ChatHeaders(req1, &auth.Auth{AccessToken: "at", UID: "u1"}, "", meta)
	req2 := mustRequest(t)
	c.ChatHeaders(req2, &auth.Auth{AccessToken: "at", UID: "u1"}, "", meta)
	req3 := mustRequest(t)
	c.ChatHeaders(req3, &auth.Auth{AccessToken: "at", UID: "u2"}, "", meta)

	id1 := req1.Header.Get("X-Conversation-ID")
	if !uuidV7Re.MatchString(id1) {
		t.Fatalf("X-Conversation-ID = %q want UUID v7 form", id1)
	}
	if id2 := req2.Header.Get("X-Conversation-ID"); id2 != id1 {
		t.Errorf("same session+uid produced different IDs: %q vs %q", id1, id2)
	}
	if id3 := req3.Header.Get("X-Conversation-ID"); id3 == id1 {
		t.Errorf("cross-account IDs must differ (cache slot isolation)")
	}
	// acp-connection-id 同会话稳定、但必须与 X-Conversation-ID 不同值。
	acp1 := req1.Header.Get("acp-connection-id")
	if !uuidV7Re.MatchString(acp1) {
		t.Errorf("acp-connection-id = %q want UUID v7 form", acp1)
	}
	if acp1 == id1 {
		t.Errorf("acp-connection-id must differ from X-Conversation-ID")
	}
	if acp2 := req2.Header.Get("acp-connection-id"); acp2 != acp1 {
		t.Errorf("acp-connection-id unstable within session: %q vs %q", acp1, acp2)
	}
}

// TestChatHeadersConversationEmptyMeta 零值 meta（测试/直接调用方）也必发全套头族：
// 轮级键内部补 32 hex、会话 ID 现造 v7、trace 独立生成。
func TestChatHeadersConversationEmptyMeta(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "u1"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	convReqID := req.Header.Get("X-Conversation-Request-ID")
	if !hex32Re.MatchString(convReqID) {
		t.Errorf("X-Conversation-Request-ID = %q want 32 hex (必发)", convReqID)
	}
	if got := req.Header.Get("X-Root-Request-ID"); got != convReqID {
		t.Errorf("X-Root-Request-ID = %q want %q", got, convReqID)
	}
	trace := req.Header.Get("X-Trace-ID")
	if !hex32Re.MatchString(trace) {
		t.Errorf("X-Trace-ID = %q want 32 hex", trace)
	}
	if got := req.Header.Get("X-B3-TraceId"); got != trace {
		t.Errorf("X-B3-TraceId = %q want %q (跟随 trace)", got, trace)
	}
	if got := req.Header.Get("X-Conversation-ID"); !uuidV7Re.MatchString(got) {
		t.Errorf("X-Conversation-ID = %q want UUID v7 form (不留空)", got)
	}
}

// TestChatHeadersConversationInvalidInboundTrace 入站透传的 trace 非法 B3 TraceId
// （含横线/长度非 16/32）→ B3 TraceId 回落消息级 messageID（恒 32 hex）。
func TestChatHeadersConversationInvalidInboundTrace(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "u1"}
	req := mustRequest(t)
	c := &Client{}
	bad := "convreq-abc-123" // 非 hex：长度 13、含横线
	c.ChatHeaders(req, a, "", ChatMeta{ConversationRequestID: bad, TraceID: bad})

	if got := req.Header.Get("X-Conversation-Request-ID"); got != bad {
		t.Errorf("X-Conversation-Request-ID = %q want %q (透传不改)", got, bad)
	}
	if got := req.Header.Get("X-Root-Request-ID"); got != bad {
		t.Errorf("X-Root-Request-ID = %q want %q (Root 回落轮级值)", got, bad)
	}
	trace := req.Header.Get("X-B3-TraceId")
	if trace == bad {
		t.Errorf("X-B3-TraceId = %q must NOT be invalid trace id", trace)
	}
	if !hex32Re.MatchString(trace) {
		t.Errorf("X-B3-TraceId = %q want 32 hex fallback", trace)
	}
	if got := req.Header.Get("X-B3-SpanId"); !hex16Re.MatchString(got) {
		t.Errorf("X-B3-SpanId = %q not 16 hex", got)
	}
}
