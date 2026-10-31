// headers_contract_test.go 出站头族**契约回归**（黄金头集来自官方客户端真机抓包）。
//
// 目的：把「客户端发什么、不发什么」逐条钉住。反代被上游识别的典型原因不是缺一个
// 关键头，而是**头集合本身与客户端不同构**——多一个 Origin、少一个 baggage、把
// 两族的头各取一半拼成第三族，都能被规则筛出来（issue #51）。
//
// 每个用例分三部分：
//   - present：必须存在的头（缺了就是残缺形态）；
//   - values：必须逐字相等的关键值（版本段 / 域 / SKU / 隐私声明）；
//   - absent：**必须不存在**的头（客户端从不发，发了就是自造指纹）。
//
// 真机依据：WorkBuddy AI 5.9.4（CLI 2.165.0，www.workbuddy.ai）与
// WorkBuddy 5.9.3（CLI 2.165.0，www.workbuddy.cn），chat / busy / report / refresh /
// v3-config 逐路径取全部样本的交集；连接层/传输层头（Host / Content-Length /
// Accept-Encoding 等）与随机值头不在断言范围。
package upstream

import (
	"strings"
	"testing"

	"workbuddy2api/internal/auth"
)

// contractCase 一条路径的头族契约。
type contractCase struct {
	name    string
	build   func(t *testing.T, c *Client, a *auth.Auth) map[string][]string
	present []string
	values  map[string]string
	absent  []string
}

// runContract 执行一条契约：先断言 present/absent，再断言 values。
func runContract(t *testing.T, tc contractCase) {
	t.Helper()
	c := &Client{}
	a := &auth.Auth{
		UID:          "204af49d-0208-4ed9-a36d-35dfcbbec818",
		AccessToken:  "AT",
		RefreshToken: "RT",
		Domain:       "www.workbuddy.ai",
	}
	got := tc.build(t, c, a)
	lower := map[string]string{}
	for k, v := range got {
		lower[strings.ToLower(k)] = strings.Join(v, ",")
	}
	for _, h := range tc.present {
		if _, ok := lower[h]; !ok {
			t.Errorf("%s: missing required header %q", tc.name, h)
		}
	}
	for _, h := range tc.absent {
		if v, ok := lower[h]; ok {
			t.Errorf("%s: forbidden header %q present (value %q)——客户端从不发", tc.name, h, v)
		}
	}
	for h, want := range tc.values {
		if got := lower[h]; got != want {
			t.Errorf("%s: header %q = %q want %q", tc.name, h, got, want)
		}
	}
}

// TestContractGlobalChat 国际版 5.9.4 chat 头族金样本。
func TestContractGlobalChat(t *testing.T) {
	runContract(t, contractCase{
		name: "global_chat",
		build: func(t *testing.T, c *Client, a *auth.Auth) map[string][]string {
			req := mustRequest(t)
			c.ChatHeaders(req, a, "", ChatMeta{})
			return req.Header
		},
		present: []string{
			"accept", "authorization", "acp-connection-id", "b3", "baggage",
			"content-type", "traceparent", "user-agent",
			"x-agent-intent", "x-agent-purpose", "x-agent-runtime", "x-agent-type",
			"x-b3-parentspanid", "x-b3-sampled", "x-b3-spanid", "x-b3-traceid",
			"x-codebuddy-request", "x-conversation-id", "x-conversation-message-id",
			"x-conversation-request-id", "x-domain", "x-ide-name", "x-ide-type",
			"x-ide-version", "x-private-data", "x-product", "x-request-id",
			"x-requested-with", "x-root-request-id", "x-trace-id", "x-user-id",
			"x-stainless-arch", "x-stainless-lang", "x-stainless-os",
			"x-stainless-package-version", "x-stainless-retry-count",
			"x-stainless-runtime", "x-stainless-runtime-version",
		},
		values: map[string]string{
			"accept":              "application/json",
			"user-agent":          "WorkBuddy/5.9.4 workbuddy-ai/5.9.4 CLI/2.165.0",
			"x-private-data":      "true",
			"x-domain":            "www.workbuddy.ai",
			"x-ide-name":          "WorkBuddy",
			"x-ide-type":          "WorkBuddy",
			"x-ide-version":       "5.9.4",
			"x-product":           "SaaS",
			"x-agent-intent":      "craft",
			"x-agent-purpose":     "conversation",
			"x-agent-runtime":     "local-agent",
			"x-agent-type":        "main",
			"x-requested-with":    "XMLHttpRequest",
			"x-codebuddy-request": "1",
		},
		absent: []string{
			// 桌面端 chat 走 SDK/node fetch，没有浏览器同源语义。
			"origin", "referer", "accept-language",
			// 个人号不发企业头，也不发任何 X-No-* 哨兵头。
			"x-enterprise-id", "x-no-enterprise-id", "x-no-department-info",
			// 客户端从不发这两个设备头（设备标识在 /v2/report 的 body 里）。
			"x-machine-id", "x-session-id",
		},
	})
}

// TestContractCNChat 国内版 5.9.3 chat 头族金样本。
func TestContractCNChat(t *testing.T) {
	runContract(t, contractCase{
		name: "cn_chat",
		build: func(t *testing.T, c *Client, a *auth.Auth) map[string][]string {
			// CN 账号无国际域声明：新建一份而不是拷贝（auth.Auth 内含互斥锁）。
			cn := &auth.Auth{UID: a.UID, AccessToken: a.AccessToken, RefreshToken: a.RefreshToken}
			req := mustRequest(t)
			c.ChatHeaders(req, cn, "", ChatMeta{})
			return req.Header
		},
		present: []string{
			"accept", "authorization", "acp-connection-id", "b3", "baggage",
			"content-type", "traceparent", "user-agent",
			"x-agent-intent", "x-agent-purpose", "x-agent-runtime", "x-agent-type",
			"x-b3-parentspanid", "x-b3-sampled", "x-b3-spanid", "x-b3-traceid",
			"x-codebuddy-request", "x-conversation-id", "x-conversation-message-id",
			"x-conversation-request-id", "x-domain", "x-ide-name", "x-ide-type",
			"x-ide-version", "x-private-data", "x-product", "x-request-id",
			"x-requested-with", "x-root-request-id", "x-trace-id", "x-user-id",
		},
		values: map[string]string{
			"accept":         "application/json",
			"user-agent":     "WorkBuddy/5.9.3 WorkBuddy/5.9.3 CLI/2.165.0",
			"x-private-data": "false",
			"x-domain":       "www.workbuddy.cn",
			"x-ide-version":  "5.9.3",
			"x-product":      "SaaS",
		},
		absent: []string{
			"origin", "referer", "accept-language",
			"x-enterprise-id", "x-no-enterprise-id", "x-no-department-info",
			"x-machine-id", "x-session-id",
		},
	})
}

// TestContractGlobalRPC 国际版 RPC/REST 层头族（/v2/chat/busy、/v2/report 等）：
// 比 chat 薄得多——只有 Accept / X-Product / UA 三件 + 认证族，且 UA 首段即品牌 slug。
func TestContractGlobalRPC(t *testing.T) {
	runContract(t, contractCase{
		name: "global_rpc",
		build: func(t *testing.T, c *Client, a *auth.Auth) map[string][]string {
			req := mustRequest(t)
			c.CommonHeaders(req, a)
			return req.Header
		},
		present: []string{"accept", "content-type", "user-agent", "x-product"},
		values: map[string]string{
			"accept":     "application/json, text/plain, */*",
			"user-agent": "workbuddy-ai/5.9.4 workbuddy-ai/5.9.4 CLI/2.165.0",
			"x-product":  "SaaS",
		},
		absent: []string{
			"origin", "referer", "accept-language",
			// 这三头只属 chat 那条链。
			"x-requested-with", "x-codebuddy-request", "x-private-data",
			"x-machine-id", "x-session-id",
		},
	})
}

// TestContractRefresh refresh 头族：双发 access/refresh token、X-Product=SaaS、
// X-Request-ID 必带；不发 X-CodeBuddy-Request / Accept-Language / X-Requested-With。
func TestContractRefresh(t *testing.T) {
	runContract(t, contractCase{
		name: "refresh",
		build: func(t *testing.T, c *Client, a *auth.Auth) map[string][]string {
			req := mustRequest(t)
			c.RefreshHeaders(req, a)
			return req.Header
		},
		present: []string{
			"accept", "authorization", "content-type", "user-agent",
			"x-auth-refresh-source", "x-domain", "x-product",
			"x-refresh-token", "x-request-id", "x-user-id",
		},
		values: map[string]string{
			"accept":                "application/json, text/plain, */*",
			"authorization":         "Bearer AT",
			"x-auth-refresh-source": "plugin",
			"x-product":             "SaaS",
			"x-domain":              "www.workbuddy.ai",
		},
		absent: []string{
			"origin", "referer", "accept-language",
			"x-codebuddy-request", "x-requested-with",
			"x-machine-id", "x-session-id",
		},
	})
}

// TestContractBilling billing 域头族：UA 恒 axios、X-Request-ID 必带、
// 不发 X-CodeBuddy-Request / Accept-Language。
func TestContractBilling(t *testing.T) {
	runContract(t, contractCase{
		name: "billing",
		build: func(t *testing.T, c *Client, a *auth.Auth) map[string][]string {
			req := mustRequest(t)
			c.BillingHeaders(req, a)
			return req.Header
		},
		present: []string{"accept", "authorization", "content-type", "user-agent", "x-domain", "x-request-id", "x-user-id"},
		values: map[string]string{
			"accept":     "application/json",
			"user-agent": "axios/1.16.1",
			"x-domain":   "www.workbuddy.ai",
		},
		absent: []string{
			"origin", "referer", "accept-language",
			"x-codebuddy-request", "x-requested-with",
			"x-machine-id", "x-session-id",
		},
	})
}
