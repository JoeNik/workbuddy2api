package upstream

import (
	"testing"

	"workbuddy2api/internal/auth"
)

// 本文件是「客户端从不发的头，网关也不得发」的回归锚点（issue #51）。
//
// 背景：旧实现按 uid 派生出 X-Machine-ID / X-Session-ID 两个 36 hex 塞进所有出站请求，
// 理由是「每账号一台固定虚拟设备，防多号被设备指纹关联」。但真机 + 静态反编译双重确认
// 客户端**从不发这两个头**——app.asar（main/ + renderer/）与内置 cli/dist 全量字符串里
// `X-Machine-ID` 命中 0 次，`X-Session-ID` 只作为常量表条目存在、从不出现在出站头里；
// 真机 chat 的 40 个出站头逐一比对也没有它们。设备标识是放在 /v2/report 的 **body** 里
// （machineId / sessionId 字段），不是头。
//
// 即：这两个头不是「缺失的设备指纹」，而是「多出来的网关自造指纹」——比不发更容易被
// 规则命中。所以断言方向与旧测试相反。

// TestCommonHeadersOmitsMachineSessionID RPC 层共享头不得携带 X-Machine-ID / X-Session-ID。
func TestCommonHeadersOmitsMachineSessionID(t *testing.T) {
	c := &Client{}
	req := mustRequest(t)
	c.CommonHeaders(req, &auth.Auth{AccessToken: "at", UID: "u1"})

	if got := req.Header.Get("X-Machine-ID"); got != "" {
		t.Errorf("X-Machine-ID = %q, want empty（客户端从不发该头）", got)
	}
	if got := req.Header.Get("X-Session-ID"); got != "" {
		t.Errorf("X-Session-ID = %q, want empty（客户端从不发该头）", got)
	}
}

// TestChatHeadersOmitsMachineSessionID chat 层同样不得携带（含兜底 Del 的验证：
// 调用方自行塞进来的也要被剥掉）。
func TestChatHeadersOmitsMachineSessionID(t *testing.T) {
	c := &Client{}
	req := mustRequest(t)
	// 模拟外部调用方先塞了这两个头，ChatHeaders 必须 Del 掉。
	req.Header.Set("X-Machine-ID", "deadbeef")
	req.Header.Set("X-Session-ID", "cafebabe")

	c.ChatHeaders(req, &auth.Auth{AccessToken: "at", UID: "u9"}, "", ChatMeta{})

	if got := req.Header.Get("X-Machine-ID"); got != "" {
		t.Errorf("X-Machine-ID = %q, want empty after ChatHeaders", got)
	}
	if got := req.Header.Get("X-Session-ID"); got != "" {
		t.Errorf("X-Session-ID = %q, want empty after ChatHeaders", got)
	}
}

// TestChatHeadersOmitsBrowserSemanticHeaders chat 不得带 Origin / Referer /
// Accept-Language：桌面端 chat 走 node fetch / SDK，没有浏览器同源语义，真机两版
// 逐条确认均无这三个头。
func TestChatHeadersOmitsBrowserSemanticHeaders(t *testing.T) {
	c := &Client{}
	req := mustRequest(t)
	// 外部调用方可能先按 web 层形态设过，chat 必须剥掉。
	req.Header.Set("Origin", "https://www.workbuddy.ai")
	req.Header.Set("Referer", "https://www.workbuddy.ai/")
	req.Header.Set("Accept-Language", "en-US")

	c.ChatHeaders(req, &auth.Auth{AccessToken: "at", UID: "u1"}, "", ChatMeta{})

	for _, h := range []string{"Origin", "Referer", "Accept-Language"} {
		if got := req.Header.Get(h); got != "" {
			t.Errorf("%s = %q, want empty（桌面端 chat 不发）", h, got)
		}
	}
}

// TestCommonHeadersOmitsRPCOnlyAbsentHeaders RPC 层不得带 X-Requested-With /
// X-CodeBuddy-Request / Origin / Referer / Accept-Language——这几头都是 chat 或
// 渲染器侧另一条链专属，旧实现把它们放在 CommonHeaders 里「全出站覆盖」。
func TestCommonHeadersOmitsRPCOnlyAbsentHeaders(t *testing.T) {
	c := &Client{}
	req := mustRequest(t)
	c.CommonHeaders(req, &auth.Auth{AccessToken: "at", UID: "u1"})

	for _, h := range []string{
		"X-Requested-With", "X-CodeBuddy-Request",
		"Origin", "Referer", "Accept-Language",
	} {
		if got := req.Header.Get(h); got != "" {
			t.Errorf("%s = %q, want empty（RPC 层不发）", h, got)
		}
	}
}
