package upstream

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"workbuddy2api/internal/auth"
)

// global 族出站形态常量（官方 WorkBuddy AI 5.9.4 真机抓包）。
//
// 注意两族 UA 的区别：chat 走 OpenAI SDK 的 buildUserAgent，首段恒驼峰 `WorkBuddy`、
// 第二段才是打包期 slug `workbuddy-ai`；RPC/REST 层（node fetch）首段就是 slug。
const (
	globalChatUAString = "WorkBuddy/5.9.4 workbuddy-ai/5.9.4 CLI/2.165.0"
	globalRPCUAString  = "workbuddy-ai/5.9.4 workbuddy-ai/5.9.4 CLI/2.165.0"
)

// uuidV7Re 官方会话级标识形态：36 字符带横线 UUID v7（第 13 位恒 7）。
// 真机 X-Conversation-ID / acp-connection-id 恒为 `01a1815d-dacf-75f1-…` 这种时间有序形态。
var uuidV7Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestChatHeadersGlobalRealm global 账号 chat 头族对齐国际版 5.9.4 真机：
//  1. UA 第二段是打包期 slug `workbuddy-ai`（**不是** productName 原文 `WorkBuddy AI`）；
//  2. X-Private-Data: true（国际发行版 DefaultModelOptimizationOff=true）；
//  3. X-Domain: www.workbuddy.ai；
//  4. 个人号**不发** X-No-Enterprise-Id（旧实现按 CodeBuddy CLI 约定补的哨兵头）。
func TestChatHeadersGlobalRealm(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "g1", Domain: "www.workbuddy.ai"} // global 账号
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	if got := req.Header.Get("User-Agent"); got != globalChatUAString {
		t.Errorf("global chat UA = %q want %q", got, globalChatUAString)
	}
	if got := req.Header.Get("X-Domain"); got != "www.workbuddy.ai" {
		t.Errorf("global X-Domain = %q want %q", got, "www.workbuddy.ai")
	}
	// 国际版发行版默认关模型优化 → 声明 true。
	if got := req.Header.Get("X-Private-Data"); got != "true" {
		t.Errorf("global X-Private-Data = %q want true", got)
	}
	// 个人号不发任何 X-No-* 哨兵头：客户端从不发，多发一个同样是指纹。
	if got := req.Header.Get("X-No-Enterprise-Id"); got != "" {
		t.Errorf("global X-No-Enterprise-Id = %q want empty（客户端不发）", got)
	}
	if got := req.Header.Get("X-Enterprise-Id"); got != "" {
		t.Errorf("global X-Enterprise-Id = %q want empty", got)
	}
}

// TestChatHeadersCNRealm CN 账号 chat 头族对齐国内版 5.9.3 真机：
// UA 两段都是驼峰 `WorkBuddy`、X-Private-Data=false、X-Domain=www.workbuddy.cn、
// 企业号发 X-Enterprise-Id、个人号不发 X-No-* 哨兵头。
func TestChatHeadersCNRealm(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "c1", EnterpriseID: "e1"} // CN 企业账号
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	if got := req.Header.Get("User-Agent"); got != defaultUAString {
		t.Errorf("CN chat UA = %q want %q", got, defaultUAString)
	}
	if got := req.Header.Get("X-Enterprise-Id"); got != "e1" {
		t.Errorf("CN X-Enterprise-Id = %q want e1", got)
	}
	if got := req.Header.Get("X-No-Enterprise-Id"); got != "" {
		t.Errorf("CN X-No-Enterprise-Id = %q want empty（客户端不发）", got)
	}
	// X-Domain 必发：真机 chat 恒带 `X-Domain: www.workbuddy.cn`（裸主机名）。
	if got := req.Header.Get("X-Domain"); got != "www.workbuddy.cn" {
		t.Errorf("CN X-Domain = %q want www.workbuddy.cn", got)
	}
	if got := req.Header.Get("X-No-Department-Info"); got != "" {
		t.Errorf("CN X-No-Department-Info = %q want empty（客户端不发）", got)
	}
	// 国内版发行版 DefaultModelOptimizationOff=false → 声明 false。
	if got := req.Header.Get("X-Private-Data"); got != "false" {
		t.Errorf("CN X-Private-Data = %q want false", got)
	}
}

// TestChatHeadersGlobalIgnoresEnterprise global 账号即便带 EnterpriseID（异常数据）也不落
// CN 侧企业头：仍发国际客户端形态（X-Domain=www.workbuddy.ai、无 X-Enterprise-Id）。
func TestChatHeadersGlobalIgnoresEnterprise(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "g2", EnterpriseID: "should-be-ignored", Domain: "www.workbuddy.ai"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	if got := req.Header.Get("X-Enterprise-Id"); got != "" {
		t.Errorf("global(virtual enterprise) X-Enterprise-Id = %q want empty", got)
	}
	if got := req.Header.Get("X-Domain"); got != "www.workbuddy.ai" {
		t.Errorf("global(virtual enterprise) X-Domain = %q want www.workbuddy.ai", got)
	}
	if got := req.Header.Get("X-No-Department-Info"); got != "" {
		t.Errorf("global(virtual enterprise) X-No-Department-Info = %q want empty", got)
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

// TestChatHeadersCNNoEnterpriseZeroRegression CN 无企业账号：不发 X-No-* 哨兵头，
// X-Domain 仍必发 CN 域（缺头是可判别形态）。
func TestChatHeadersCNNoEnterpriseZeroRegression(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "c2"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	if got := req.Header.Get("X-No-Enterprise-Id"); got != "" {
		t.Errorf("CN(no enterprise) X-No-Enterprise-Id = %q want empty", got)
	}
	if got := req.Header.Get("X-No-Department-Info"); got != "" {
		t.Errorf("CN(no enterprise) X-No-Department-Info = %q want empty", got)
	}
	if got := req.Header.Get("X-Domain"); got != "www.workbuddy.cn" {
		t.Errorf("CN(no enterprise) X-Domain = %q want www.workbuddy.cn", got)
	}
}

// TestUAFamiliesByRealm UA 双族断言：chat 族首段恒驼峰、RPC 族首段即品牌 slug。
// 同一会话里两族并存本身就是官方形态（真机 1182 条 flow 逐条核对）。
func TestUAFamiliesByRealm(t *testing.T) {
	c := &Client{}
	global := &auth.Auth{Domain: "www.workbuddy.ai"}
	cn := &auth.Auth{}

	if got := c.userAgent(global); got != globalChatUAString {
		t.Errorf("global chat UA = %q want %q", got, globalChatUAString)
	}
	if got := c.rpcUAFor(global); got != globalRPCUAString {
		t.Errorf("global RPC UA = %q want %q", got, globalRPCUAString)
	}
	if got := c.userAgent(cn); got != defaultUAString {
		t.Errorf("CN chat UA = %q want %q", got, defaultUAString)
	}
	// CN 下两族同值（品牌段本来就是驼峰）。
	if got := c.rpcUAFor(cn); got != defaultUAString {
		t.Errorf("CN RPC UA = %q want %q (same as chat)", got, defaultUAString)
	}
	// 版本覆盖仍生效：global 品牌段跟随 clientVersion。
	c2 := &Client{ClientVersion: "6.0.0"}
	if got := c2.defaultWorkBuddyUAFor(global); got != "WorkBuddy/6.0.0 workbuddy-ai/6.0.0 CLI/2.165.0" {
		t.Errorf("global chat UA(v6) = %q", got)
	}
	if got := c2.rpcUAFor(global); got != "workbuddy-ai/6.0.0 workbuddy-ai/6.0.0 CLI/2.165.0" {
		t.Errorf("global RPC UA(v6) = %q", got)
	}
}

// TestCommonHeadersUsesRPCFamily RPC 层（CommonHeaders）发 RPC 族 UA 且不带 chat 专属头。
func TestCommonHeadersUsesRPCFamily(t *testing.T) {
	c := &Client{}
	req := mustRequest(t)
	c.CommonHeaders(req, &auth.Auth{UID: "g1", Domain: "www.workbuddy.ai"})

	if got := req.Header.Get("User-Agent"); got != globalRPCUAString {
		t.Errorf("RPC UA = %q want %q", got, globalRPCUAString)
	}
	if got := req.Header.Get("Accept"); got != "application/json, text/plain, */*" {
		t.Errorf("RPC Accept = %q want loose value", got)
	}
	if got := req.Header.Get("X-Product"); got != "SaaS" {
		t.Errorf("RPC X-Product = %q want SaaS", got)
	}
	for _, h := range []string{"X-Requested-With", "X-CodeBuddy-Request", "X-Private-Data"} {
		if got := req.Header.Get(h); got != "" {
			t.Errorf("RPC %s = %q want empty（chat 专属）", h, got)
		}
	}
}

// TestChatHeadersConversationRealForm 会话头族对齐真机形态（替代早前「global 送 "-"」的
// 猜测性修复）：三件套**必须齐全且是客户端同形态**，而不是占位串。
//
// 上游对渠道校验失败的响应自述是「渠道校验失败，可能不是从官方客户端发出的」
// （400 code=11-128 / msg=request illegal），缺任一头即报 conversation id missing；
// 而官方客户端的 X-Conversation-ID 恒为 v7 时间有序 UUID（36 字符带横线），
// X-Conversation-Request-ID 是另一条 32 hex，X-Trace-ID 与它**从无相等**。
func TestChatHeadersConversationRealForm(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "g1", Domain: "www.workbuddy.ai"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{
		ConversationRequestID: "0123456789abcdef0123456789abcdef",
	})

	convID := req.Header.Get("X-Conversation-ID")
	if !uuidV7Re.MatchString(convID) {
		t.Errorf("X-Conversation-ID = %q want UUID v7 form", convID)
	}
	if got := req.Header.Get("X-Conversation-Request-ID"); got != "0123456789abcdef0123456789abcdef" {
		t.Errorf("X-Conversation-Request-ID = %q want convReqID", got)
	}
	msgID := req.Header.Get("X-Conversation-Message-ID")
	if !hex32Re.MatchString(msgID) {
		t.Errorf("X-Conversation-Message-ID = %q want 32 hex", msgID)
	}
	if got := req.Header.Get("X-Request-ID"); got != msgID {
		t.Errorf("X-Request-ID = %q want == X-Conversation-Message-ID (%q)", got, msgID)
	}
	// X-Trace-ID 独立于轮级键：真机两者从无相等（恒等本身是网关自造的痕迹）。
	traceID := req.Header.Get("X-Trace-ID")
	if traceID == "0123456789abcdef0123456789abcdef" {
		t.Errorf("X-Trace-ID must NOT equal ConversationRequestID")
	}
	if !hex32Re.MatchString(traceID) {
		t.Errorf("X-Trace-ID = %q want 32 hex", traceID)
	}
	if got := req.Header.Get("X-B3-TraceId"); got != traceID {
		t.Errorf("X-B3-TraceId = %q want == X-Trace-ID (%q)", got, traceID)
	}
	// acp-connection-id 与 X-Conversation-ID 同为 v7、但值不同。
	acp := req.Header.Get("acp-connection-id")
	if !uuidV7Re.MatchString(acp) {
		t.Errorf("acp-connection-id = %q want UUID v7 form", acp)
	}
	if acp == convID {
		t.Errorf("acp-connection-id must differ from X-Conversation-ID")
	}
	// 三件套不得是占位串（早前 "-" 修复已撤销：真机从不发占位）。
	for _, h := range []string{"X-Conversation-ID", "X-Conversation-Request-ID", "X-Conversation-Message-ID"} {
		if got := req.Header.Get(h); got == "-" || got == "" {
			t.Errorf("%s = %q want real value", h, got)
		}
	}
}

// TestChatHeadersConversationPassthrough 客户端声明了 conversationId → 原值透传（不伪造）。
func TestChatHeadersConversationPassthrough(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "c1"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{ConversationID: "conv-1"})

	if got := req.Header.Get("X-Conversation-ID"); got != "conv-1" {
		t.Errorf("X-Conversation-ID = %q want conv-1 (passthrough)", got)
	}
	// baggage 的 session_id 段必须与同请求的头值一致（头体不一致本身就是可判别形态）。
	if bag := req.Header.Get("baggage"); !strings.Contains(bag, "codebuddy.session_id=conv-1") {
		t.Errorf("baggage = %q want session_id=conv-1", bag)
	}
}

// TestChatHeadersBaggageFourMembers baggage 恒 4 个成员、两个 buddy 键留空值
// （OTel 传播器序列化形态）。多一个成员就是另一种形态。
func TestChatHeadersBaggageFourMembers(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "c1"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{
		ConversationID:        "conv-1",
		ConversationRequestID: "0123456789abcdef0123456789abcdef",
	})

	bag := req.Header.Get("baggage")
	want := "buddyId=,buddyName=,codebuddy.session_id=conv-1," +
		"codebuddy.conversation_request_id=0123456789abcdef0123456789abcdef"
	if bag != want {
		t.Errorf("baggage = %q want %q", bag, want)
	}
	if n := len(strings.Split(bag, ",")); n != 4 {
		t.Errorf("baggage members = %d want 4", n)
	}
}

// TestChatHeadersSDKAndAgentFingerprint chat 必带的 SDK 段与 agent 段指纹头。
func TestChatHeadersSDKAndAgentFingerprint(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "c1"}
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, a, "", ChatMeta{})

	for hdr, want := range map[string]string{
		"X-Stainless-Lang":            "js",
		"X-Stainless-Package-Version": "6.25.0",
		"X-Stainless-OS":              "Windows",
		"X-Stainless-Arch":            "x64",
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Runtime-Version": "v22.21.1",
		"X-Stainless-Retry-Count":     "0",
		"X-Agent-Type":                "main",
		"X-Agent-Intent":              "craft",
		"X-Agent-Runtime":             "local-agent",
		"X-Agent-Purpose":             "conversation",
		"X-CodeBuddy-Request":         "1",
		"X-Requested-With":            "XMLHttpRequest",
		"Accept":                      "application/json",
	} {
		if got := req.Header.Get(hdr); got != want {
			t.Errorf("%s = %q want %q", hdr, got, want)
		}
	}
	// 链路族：b3 单头 + traceparent 双发（客户端从不只发 X-B3-* 三件套）。
	if got := req.Header.Get("b3"); !strings.HasPrefix(got, req.Header.Get("X-B3-TraceId")+"-") {
		t.Errorf("b3 = %q want trace-span-flags-parent", got)
	}
	if got := req.Header.Get("traceparent"); !strings.HasPrefix(got, "00-"+req.Header.Get("X-B3-TraceId")+"-") {
		t.Errorf("traceparent = %q want 00-trace-span-flags", got)
	}
	if !hex16Re.MatchString(req.Header.Get("X-B3-ParentSpanId")) {
		t.Errorf("X-B3-ParentSpanId = %q want 16 hex", req.Header.Get("X-B3-ParentSpanId"))
	}
}

// TestChatHeadersAgentTypeAuxiliary 辅助链（网关自发任务）声明 auxiliary，不冒充主对话。
func TestChatHeadersAgentTypeAuxiliary(t *testing.T) {
	req := mustRequest(t)
	c := &Client{}
	c.ChatHeaders(req, &auth.Auth{AccessToken: "at", UID: "c1"}, "", ChatMeta{AgentType: AgentTypeAuxiliary})
	if got := req.Header.Get("X-Agent-Type"); got != "auxiliary" {
		t.Errorf("X-Agent-Type = %q want auxiliary", got)
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
