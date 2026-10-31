package server

import (
	"testing"
	"time"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/pool"
	"workbuddy2api/internal/upstream"
)

// 这一组锁死「同一条 msg 骑不同 code」的分野：上游把 `request illegal` 用在三种
// 语义上，只有 code 11140 是账号级授权失效，其余两种是请求级拦截（上游自己的
// displayMsg/displayTips 都写着「请重新发送」）。
//
// 旧实现只匹配 msg 字面量 → 一律 Disable()：一条被拦的请求就能把一个凭证/余额
// 都正常的号永久踢出池子，还得人工解冻。三个用例合起来钉住「谁能禁用、谁只能冷却」。

// newFaultTestHandler 构造带软冷却基数的 handler（NewHandler 会把 0 补成 600s）。
func newFaultTestHandler(t *testing.T, uid string) (*Handler, *pool.Pool) {
	t.Helper()
	p := pool.New("")
	p.Add(&auth.Auth{UID: uid})
	return NewHandler(Config{Pool: p, SoftCooldown: 600 * time.Second}), p
}

// TestAccountFaultBodiesClassifyAsAccountFault 前置断言：三个用例的 body 都必须先被
// Classify 判成 ErrAccountFault，才谈得上 applyErrorPolicy 的分野。
//
// 这一步不能省——若上游哪天把 11128 的文案改成命中 contentBlockedRule（更合适的
// 分类其实是「不罚号」），本组用例会在策略层通过、实际却根本走不到策略层。
func TestAccountFaultBodiesClassifyAsAccountFault(t *testing.T) {
	for name, body := range map[string]string{
		"11140":  `{"code":11140,"msg":"request illegal"}`,
		"11-128": `{"code":"11-128","msg":"request illegal","displayTips":{"zh":"渠道校验失败"}}`,
		"11128":  `{"code":11128,"msg":"request illegal","displayMsg":{"zh":"请求被拦截，请重新发送"}}`,
	} {
		if got := upstream.Classify(400, body); got != upstream.ErrAccountFault {
			t.Errorf("%s: Classify = %v want ErrAccountFault", name, got)
		}
	}
}

// TestAccountFault11140StillDisables code 11140 = auth_forbidden 账号级授权风控：
// 软冷却到期也不会自愈（要重新 OAuth），必须硬禁用，否则每次冷却结束都白打一次上游。
func TestAccountFault11140StillDisables(t *testing.T) {
	h, p := newFaultTestHandler(t, "u1")
	h.applyErrorPolicy("u1", upstream.ErrAccountFault,
		`{"code":11140,"msg":"request illegal","requestId":"r1"}`, "m", nil)

	st, _ := p.Status("u1")
	if !st.Disabled {
		t.Fatalf("11140 授权风控应硬禁用: %+v", st)
	}
	if st.Cooling {
		t.Fatalf("硬禁用不应同时挂软冷却: %+v", st)
	}
}

// TestAccountFaultChannelVerification11_128KeepsAccount code "11-128"（字符串）=
// 渠道校验拦截：displayTips 原文「渠道校验失败，可能不是从官方客户端发出的，
// 请用官方客户端重新发送」——请求级可重试，不得永久禁用。
func TestAccountFaultChannelVerification11_128KeepsAccount(t *testing.T) {
	h, p := newFaultTestHandler(t, "u1")
	body := `{"code":"11-128","msg":"request illegal","requestId":"r1",` +
		`"displayMsg":{"zh":"请求被拦截，请重新发送"},` +
		`"displayTips":{"zh":"渠道校验失败，可能不是从官方客户端发出的，请用官方客户端重新发送"}}`
	h.applyErrorPolicy("u1", upstream.ErrAccountFault, body, "m", nil)

	st, _ := p.Status("u1")
	if st.Disabled {
		t.Fatalf("11-128 渠道校验拦截不得永久禁用账号: %+v", st)
	}
	if !st.Cooling || st.CoolKind != "soft_rate" {
		t.Fatalf("应软冷却让账号出池自愈: %+v", st)
	}
}

// TestAccountFaultRequestBlocked11128KeepsAccount code 11128（数字）=
// 请求级拦截，displayMsg 原文「请求被拦截，请重新发送」。这是本仓 2026-10 国际版
// 400 日志里的实测形态（acct=xiao001 那次），此前被当成 11140 硬禁用。
func TestAccountFaultRequestBlocked11128KeepsAccount(t *testing.T) {
	h, p := newFaultTestHandler(t, "u1")
	body := `{"code":11128,"msg":"request illegal","requestId":"e91deeffeeec313209e8cbc8845e9bfe",` +
		`"displayMsg":{"en":"Request blocked. Please send it again","zh":"请求被拦截，请重新发送"}}`
	h.applyErrorPolicy("u1", upstream.ErrAccountFault, body, "m", nil)

	st, _ := p.Status("u1")
	if st.Disabled {
		t.Fatalf("11128 请求级拦截不得永久禁用账号: %+v", st)
	}
	if !st.Cooling || st.CoolKind != "soft_rate" {
		t.Fatalf("应软冷却让账号出池自愈: %+v", st)
	}
}

// TestAccountFaultUnknownCodeSoftCools 无 code 证据的 request illegal 走软冷却。
//
// 这是**有意的偏离** panel 17b5204：panel 的写法是「不是 11-128 就 Disable」，
// 但 11128 同样带 request illegal 且是请求级（见上个用例），按 panel 的默认分支
// 会继续误禁用。只有 11140 有「账号级授权失效」的正面证据，其余形态没有证据支持
// 永久禁用；误软冷却的代价只是一次白打的上游请求。
func TestAccountFaultUnknownCodeSoftCools(t *testing.T) {
	h, p := newFaultTestHandler(t, "u1")
	h.applyErrorPolicy("u1", upstream.ErrAccountFault, `{"msg":"request illegal"}`, "m", nil)

	st, _ := p.Status("u1")
	if st.Disabled {
		t.Fatalf("无 11140 证据的 request illegal 不应永久禁用: %+v", st)
	}
	if !st.Cooling || st.CoolKind != "soft_rate" {
		t.Fatalf("应软冷却: %+v", st)
	}
}

// TestAccountFault14017StillSoftCools 14017（trial not activated）行为零回归：
// 补完 register 后可能自愈，保持软冷却。
func TestAccountFault14017StillSoftCools(t *testing.T) {
	h, p := newFaultTestHandler(t, "u1")
	h.applyErrorPolicy("u1", upstream.ErrAccountFault,
		`{"code":14017,"msg":"trial not activated"}`, "m", nil)

	st, _ := p.Status("u1")
	if st.Disabled {
		t.Fatalf("14017 不应硬禁用: %+v", st)
	}
	if !st.Cooling || st.CoolKind != "soft_rate" {
		t.Fatalf("14017 应软冷却: %+v", st)
	}
}
