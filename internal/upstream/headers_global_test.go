package upstream

import (
	"net/http"
	"strings"
	"testing"

	"workbuddy2api/internal/auth"
)

// globalUAString global realm 默认出站 UA：第二段 platform 品牌为官方国际版
// `WorkBuddy AI`（intl 项目逆向证据：WorkBuddy/5.5.2 WorkBuddy AI/5.5.2 CLI/5.5.2），
// 版本段保持现有 clientVersion/cliVersion（本仓库 5.5.4/2.137.1），只切品牌段。
const globalUAString = "WorkBuddy/5.5.4 WorkBuddy AI/5.5.4 CLI/2.137.1"

// TestChatHeadersGlobalRealm global 账号的 chat 请求头对齐 intl 三件套：
//  1. UA 含 `WorkBuddy AI/<v>` 平台段（非 CN 的 `WorkBuddy/<v>`）；
//  2. X-No-Enterprise-Id: 1（个人账号无企业 ID，显式声明）；
//  3. X-Domain: www.workbuddy.ai（显式声明国际版域）。
//
// 与 CN 的差异是修复 global 403 code 11140 "request illegal" 风控的关键（
// ANALYSIS-global-chat-solutions.md）。
func TestChatHeadersGlobalRealm(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "g1", Domain: "www.workbuddy.ai"} // global 账号
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	if got := req.Header.Get("User-Agent"); got != globalUAString {
		t.Errorf("global UA = %q want %q", got, globalUAString)
	}
	if got := req.Header.Get("X-No-Enterprise-Id"); got != "1" {
		t.Errorf("global X-No-Enterprise-Id = %q want %q", got, "1")
	}
	if got := req.Header.Get("X-Domain"); got != "www.workbuddy.ai" {
		t.Errorf("global X-Domain = %q want %q", got, "www.workbuddy.ai")
	}
	// global 账号不落 CN 侧的 X-Enterprise-Id（企业头无意义，只发 No-* 声明）。
	if got := req.Header.Get("X-Enterprise-Id"); got != "" {
		t.Errorf("global X-Enterprise-Id = %q want empty", got)
	}
}

// TestChatHeadersCNRealm CN 账号头零回归：UA 不带 `WorkBuddy AI` 平台段、不注入
// global 专属的 X-No-Enterprise-Id / X-Domain: www.workbuddy.ai；EnterpriseID 非空
// 走原有 X-Enterprise-Id 分支，Domain 空走原有 X-No-Department-Info 分支。
func TestChatHeadersCNRealm(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "c1", EnterpriseID: "e1"} // CN 企业账号 + 无 domain
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	if got := req.Header.Get("User-Agent"); strings.Contains(got, "WorkBuddy AI") {
		t.Errorf("CN UA = %q must NOT contain WorkBuddy AI (want %q)", got, defaultUAString)
	}
	if got := req.Header.Get("User-Agent"); got != defaultUAString {
		t.Errorf("CN UA = %q want %q", got, defaultUAString)
	}
	if got := req.Header.Get("X-Enterprise-Id"); got != "e1" {
		t.Errorf("CN X-Enterprise-Id = %q want e1", got)
	}
	if got := req.Header.Get("X-No-Enterprise-Id"); got != "" {
		t.Errorf("CN X-No-Enterprise-Id = %q want empty (enterprise set)", got)
	}
	if got := req.Header.Get("X-Domain"); got != "" {
		t.Errorf("CN X-Domain = %q want empty (domain empty → X-No-Department-Info)", got)
	}
	if got := req.Header.Get("X-No-Department-Info"); got != "1" {
		t.Errorf("CN X-No-Department-Info = %q want 1 (zero regression)", got)
	}
}

// TestChatHeadersGlobalStrongOverride global 账号（domain 标记 global）即便带
// EnterpriseID（异常数据）也发国际客户端形态头：X-No-Enterprise-Id=1 +
// X-Domain=www.workbuddy.ai，不落 CN 侧企业头（覆盖注入顺序：global 分支先于
// 既有分支的强覆盖语义）。
func TestChatHeadersGlobalStrongOverride(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "g2", EnterpriseID: "should-be-ignored", Domain: "www.workbuddy.ai"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	if got := req.Header.Get("X-No-Enterprise-Id"); got != "1" {
		t.Errorf("global(virtual enterprise) X-No-Enterprise-Id = %q want 1", got)
	}
	if got := req.Header.Get("X-Enterprise-Id"); got != "" {
		t.Errorf("global(virtual enterprise) X-Enterprise-Id = %q want empty", got)
	}
	if got := req.Header.Get("X-Domain"); got != "www.workbuddy.ai" {
		t.Errorf("global(virtual enterprise) X-Domain = %q want www.workbuddy.ai", got)
	}
	if got := req.Header.Get("X-No-Department-Info"); got != "" {
		t.Errorf("global(virtual enterprise) X-No-Department-Info = %q want empty (X-Domain supersedes)", got)
	}
}

// TestChatHeadersGlobalWithConfiguredDomain global 账号即便显式配了登录 domain，
// chat 出站 X-Domain 也固定为 www.workbuddy.ai（国际客户端形态，不回退到会话原值）。
func TestChatHeadersGlobalWithConfiguredDomain(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "g3", Domain: "login.workbuddy.ai"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	if got := req.Header.Get("X-Domain"); got != "www.workbuddy.ai" {
		t.Errorf("global(with custom domain) X-Domain = %q want www.workbuddy.ai", got)
	}
}

// TestChatHeadersCNNoEnterpriseZeroRegression CN 无企业/无 domain 的老形态账号：
// X-No-Enterprise-Id=1 + X-No-Department-Info=1（各自缺省），不注入 global 的 X-Domain。
func TestChatHeadersCNNoEnterpriseZeroRegression(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "c2"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	if got := req.Header.Get("X-No-Enterprise-Id"); got != "1" {
		t.Errorf("CN(no enterprise) X-No-Enterprise-Id = %q want 1", got)
	}
	if got := req.Header.Get("X-No-Department-Info"); got != "1" {
		t.Errorf("CN(no domain) X-No-Department-Info = %q want 1", got)
	}
	if got := req.Header.Get("X-Domain"); got != "" {
		t.Errorf("CN(no enterprise) X-Domain = %q want empty", got)
	}
}

// TestDefaultWorkBuddyUAForGlobal UA 品牌段按 realm 切换的单元级断言：
// global → 第二段 `WorkBuddy AI`；CN → 第二段 `WorkBuddy`。
func TestDefaultWorkBuddyUAForGlobal(t *testing.T) {
	c := &Client{}
	if got := c.defaultWorkBuddyUAFor(&auth.Auth{}); got != defaultUAString {
		t.Errorf("defaultWorkBuddyUAFor(cn) = %q want %q", got, defaultUAString)
	}
	if got := c.defaultWorkBuddyUAFor(&auth.Auth{Domain: "www.workbuddy.ai"}); got != globalUAString {
		t.Errorf("defaultWorkBuddyUAFor(global) = %q want %q", got, globalUAString)
	}
	// version 覆盖仍生效：global 平台段跟随 clientVersion。
	c2 := &Client{ClientVersion: "6.0.0"}
	if got := c2.defaultWorkBuddyUAFor(&auth.Auth{Domain: "www.workbuddy.ai"}); got != "WorkBuddy/6.0.0 WorkBuddy AI/6.0.0 CLI/2.137.1" {
		t.Errorf("defaultWorkBuddyUAFor(global, v6) = %q", got)
	}
}

// TestChatHeadersGlobalConversationPlaceholder global（国际版）会话头族修复：
// workbuddy.ai 上游 2026-10 起对 X-Conversation-ID / X-Conversation-Request-ID /
// X-Conversation-Message-ID 三头强校验——官方 Web 客户端即送占位 "-"，缺失头
// （无 conversation_id 的 OpenAI 兼容客户端）或任意生成值一律 400 code 11128
// "request illegal"（「请求被拦截，请重新发送」）。故 global 出站三头恒定 "-"
// （即便 meta 带了真实 conversationID / conversationRequestID 也被覆写）；
// 消息级/链路族（X-Request-ID / X-Root-Request-ID / X-Trace-ID / X-B3-*）无
// realm 差异，维持生成语义。
func TestChatHeadersGlobalConversationPlaceholder(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "g1", Domain: "www.workbuddy.ai"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{
		ConversationID:        "conv-1",
		ConversationRequestID: "0123456789abcdef0123456789abcdef",
		TraceID:               "trace-inbound",
	})

	for _, h := range []string{"X-Conversation-ID", "X-Conversation-Request-ID", "X-Conversation-Message-ID"} {
		if got := req.Header.Get(h); got != "-" {
			t.Errorf("global %s = %q want placeholder %q (intl upstream 11128 gate)", h, got, "-")
		}
	}
	// 链路/消息族保持生成语义（上游仅拦截 X-Conversation-* 三头）。
	msgID := req.Header.Get("X-Request-ID")
	if !hex32Re.MatchString(msgID) {
		t.Errorf("X-Request-ID = %q want 32 hex", msgID)
	}
	if got := req.Header.Get("X-Root-Request-ID"); got != "0123456789abcdef0123456789abcdef" {
		t.Errorf("X-Root-Request-ID = %q want convReqID", got)
	}
	if got := req.Header.Get("X-Trace-ID"); got != "trace-inbound" {
		t.Errorf("X-Trace-ID = %q want trace-inbound (passthrough)", got)
	}
	if got := req.Header.Get("X-B3-TraceId"); got != "0123456789abcdef0123456789abcdef" {
		t.Errorf("X-B3-TraceId = %q want convReqID (32 hex)", got)
	}
	if !hex16Re.MatchString(req.Header.Get("X-B3-SpanId")) {
		t.Errorf("X-B3-SpanId = %q want 16 hex", req.Header.Get("X-B3-SpanId"))
	}
	// global 指纹头零回归（三件套仍在）。
	if got := req.Header.Get("User-Agent"); got != globalUAString {
		t.Errorf("global UA = %q want %q", got, globalUAString)
	}
	if got := req.Header.Get("X-No-Enterprise-Id"); got != "1" {
		t.Errorf("X-No-Enterprise-Id = %q want 1", got)
	}
	if got := req.Header.Get("X-Domain"); got != "www.workbuddy.ai" {
		t.Errorf("X-Domain = %q want www.workbuddy.ai", got)
	}
}

// TestChatHeadersCNConversationZeroRegression CN 账号会话头族零回归：既有
// 透传/生成语义（不因 global 修复改道）——X-Conversation-ID 透传 meta 原值、
// X-Conversation-Request-ID 为聚合主键、消息级 ID 32 hex。
func TestChatHeadersCNConversationZeroRegression(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "c1"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{
		ConversationID:        "conv-1",
		ConversationRequestID: "0123456789abcdef0123456789abcdef",
	})

	if got := req.Header.Get("X-Conversation-ID"); got != "conv-1" {
		t.Errorf("CN X-Conversation-ID = %q want conv-1 (passthrough)", got)
	}
	if got := req.Header.Get("X-Conversation-Request-ID"); got != "0123456789abcdef0123456789abcdef" {
		t.Errorf("CN X-Conversation-Request-ID = %q want convReqID (aggregation key)", got)
	}
	if mid := req.Header.Get("X-Conversation-Message-ID"); !hex32Re.MatchString(mid) {
		t.Errorf("CN X-Conversation-Message-ID = %q want 32 hex", mid)
	}
}

// mustRequest 构造一个无 body 的 POST 请求（测试 ChatHeaders 用）。
func mustRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://chat.example/console/chat/completions", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}