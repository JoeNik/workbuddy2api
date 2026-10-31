package upstream

import (
	"net/http"
	"testing"

	"workbuddy2api/internal/auth"
)

// TestCommonHeadersCodeBuddyRequest 出站头分族回归（见各用例注释）。
// TestCommonHeadersCodeBuddyRequest X-CodeBuddy-Request **只属 chat 那条链**。
//
// 真机全量扫描：7 条 /v2/chat/completions 全带该头，而 busy / report / refresh /
// v3-config / billing 等 30+ 条业务路径**一条都没有**。旧实现把它放在 CommonHeaders
// 里「全出站覆盖」，等于给 refresh 等轻请求多发一个客户端从不发的头。
func TestCommonHeadersCodeBuddyRequest(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "u1"}
	c := &Client{}

	// chat 路径：必带
	req := mustRequest(t)
	c.ChatHeaders(req, a, "", ChatMeta{})
	if got := req.Header.Get("X-CodeBuddy-Request"); got != "1" {
		t.Errorf("chat X-CodeBuddy-Request = %q want %q", got, "1")
	}

	// billing 路径：不带
	req2 := mustRequest(t)
	c.BillingHeaders(req2, a)
	if got := req2.Header.Get("X-CodeBuddy-Request"); got != "" {
		t.Errorf("billing X-CodeBuddy-Request = %q want empty（客户端不发）", got)
	}

	// refresh 路径：不带
	req3 := mustRequest(t)
	c.RefreshHeaders(req3, a)
	if got := req3.Header.Get("X-CodeBuddy-Request"); got != "" {
		t.Errorf("refresh X-CodeBuddy-Request = %q want empty（客户端不发）", got)
	}
}

// TestRefreshHeadersAuthRefreshSource refresh 出站 X-Auth-Refresh-Source 对齐
// 官方客户端 refresh 渠道标识为 "plugin"（D3，原值 "workbuddy" 与官方不一致）。
func TestRefreshHeadersAuthRefreshSource(t *testing.T) {
	a := &auth.Auth{AccessToken: "at", UID: "u1", RefreshToken: "rt"}
	req := mustRequest(t)
	c := &Client{}
	c.RefreshHeaders(req, a)

	if got := req.Header.Get("X-Auth-Refresh-Source"); got != "plugin" {
		t.Errorf("X-Auth-Refresh-Source = %q want %q", got, "plugin")
	}
}

// TestCommonHeadersOmitsAcceptLanguage 桌面端**不发** Accept-Language。
//
// 真机确认 chat / refresh / v3-config / busy / report / billing 逐条均无该头——只有浏览器
// 语义的 web 层才由 Chromium 自动带。旧实现按 realm 硬切 zh-CN / en-US，是把「账号域」
// 误当成「操作系统区域」，反而成了与真机不符的指纹。
func TestCommonHeadersOmitsAcceptLanguage(t *testing.T) {
	c := &Client{}
	cnA := &auth.Auth{AccessToken: "at", UID: "c1"}
	glA := &auth.Auth{AccessToken: "at", UID: "g1", Domain: "www.workbuddy.ai"}

	for name, tc := range map[string]struct {
		a   *auth.Auth
		run func(*http.Request, *auth.Auth)
	}{
		"cn_chat":        {cnA, func(r *http.Request, a *auth.Auth) { c.ChatHeaders(r, a, "", ChatMeta{}) }},
		"global_chat":    {glA, func(r *http.Request, a *auth.Auth) { c.ChatHeaders(r, a, "", ChatMeta{}) }},
		"cn_refresh":     {cnA, c.RefreshHeaders},
		"global_refresh": {glA, c.RefreshHeaders},
	} {
		req := mustRequest(t)
		tc.run(req, tc.a)
		if got := req.Header.Get("Accept-Language"); got != "" {
			t.Errorf("%s Accept-Language = %q want empty（客户端不发）", name, got)
		}
	}
}

// TestAcceptHeaderStreamVsNonStream Accept 头分流式/非流式（D6）：
// chat 出站带 text/event-stream（流式），billing/refresh 出站仅 application/json
// （去掉宽松的 text/plain, */*）。
func TestAcceptHeaderStreamVsNonStream(t *testing.T) {
	c := &Client{}
	a := &auth.Auth{AccessToken: "at", UID: "u1", RefreshToken: "rt"}

	// chat：客户端只发 application/json，照样收 SSE 流（两版实测；旧值多带
	// ", text/event-stream" 属自造）。
	chatReq := mustRequest(t)
	c.ChatHeaders(chatReq, a, "", ChatMeta{})
	if got := chatReq.Header.Get("Accept"); got != "application/json" {
		t.Errorf("chat Accept = %q want %q", got, "application/json")
	}

	// billing 非流式：application/json
	bReq := mustRequest(t)
	c.BillingHeaders(bReq, a)
	if got := bReq.Header.Get("Accept"); got != "application/json" {
		t.Errorf("billing Accept = %q want %q", got, "application/json")
	}

	// refresh：RPC 族恒发宽松值 application/json, text/plain, */*
	rReq := mustRequest(t)
	c.RefreshHeaders(rReq, a)
	if got := rReq.Header.Get("Accept"); got != "application/json, text/plain, */*" {
		t.Errorf("refresh Accept = %q want loose value", got)
	}
}
