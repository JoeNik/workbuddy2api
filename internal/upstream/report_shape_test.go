package upstream

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"workbuddy2api/internal/auth"
)

// 这一组把 /v2/report 的**事件键集**钉死成真机形态（5.9.3 国内版抓包）。
//
// 上报体是最容易被忽略的指纹面：它的键集不参与任何业务逻辑，却是「这是不是桌面端」
// 的直接证据。旧实现发了 8 个真机没有的键（maxToken / maxSteps / temperature /
// maxRetries / skillCount / totalCount / fileUri / expertId），又不带 machineId /
// cpuModel / commit / releaseDate——多发的空值键比缺字段更显眼。

// reportCapture 跑一次上报并返回单条事件 map。
func reportCapture(t *testing.T, a *auth.Auth) map[string]any {
	t.Helper()
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("body must be a JSON array: %v (body=%s)", err, raw)
		}
		w.Write([]byte(`{"code":0,"msg":"OK"}`))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), GlobalEnabled: true,
		BillingBaseCN: srv.URL, BillingBaseGlobal: srv.URL}
	if err := c.ReportChatActivity(a, "0190f2c4-8a3e-7c1d-9b2f-0123456789ab", "req-1"); err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("events=%d want 1", len(got))
	}
	return got[0]
}

// TestReportEventKeySetMatchesDesktopClient 真机必带的键一个都不能少。
func TestReportEventKeySetMatchesDesktopClient(t *testing.T) {
	ev := reportCapture(t, &auth.Auth{AccessToken: "at", UID: "u-cn", Nickname: "nick"})

	// 设备 / 包指纹（缺 machineId 或 cpuModel 是上游侧写设备时最明显的非桌面端特征）。
	for _, k := range []string{
		"timezone", "reportDelay", "userId", "username", "userNickname", "product",
		"releaseDate", "commit", "ideName", "ideType", "ideVersion",
		"machineId", "sessionId", "extName", "extVersion",
		"os", "arch", "osVersion", "cpuModel", "cpuCores", "memorySize",
		"timestamp", "presentAt",
	} {
		if _, ok := ev[k]; !ok {
			t.Errorf("缺少真机必带键 %q", k)
		}
	}
	// chat 专属字段。
	for _, k := range []string{
		"eventCode", "mode", "conversationId", "requestId", "inputLength",
		"requestModelId", "requestModelName", "isPlan", "isAutoExecuteTerminal",
		"isAutoModify", "codebaseEnable", "isMaxMode", "source",
		"mentionContexts", "knowledgeId", "knowledgeName", "codebaseId",
		"mentionContextCount", "command", "inputAttemptId", "recommendId",
		"skillId", "traceId", "rootRequestId", "parentConversationId",
		"agentName", "agentType",
		"vcsType", "vcsRepo", "vcsBranchName", "vcsRevId",
		"buddyId", "buddyName",
		"codebuddy.session_id", "codebuddy.conversation_request_id",
	} {
		if _, ok := ev[k]; !ok {
			t.Errorf("缺少 chat_request_send 必带键 %q", k)
		}
	}
}

// TestReportEventDropsStaleKeys 真机没有的键不得出现（旧实现从更早客户端抄来的 8 个）。
func TestReportEventDropsStaleKeys(t *testing.T) {
	ev := reportCapture(t, &auth.Auth{AccessToken: "at", UID: "u-cn"})
	for _, k := range []string{
		"maxToken", "maxSteps", "temperature", "maxRetries",
		"skillCount", "totalCount", "fileUri", "expertId",
	} {
		if _, ok := ev[k]; ok {
			t.Errorf("真机无此键，不得发送: %q = %v", k, ev[k])
		}
	}
}

// TestReportEventValueShapes 关键取值形态（agentName/agentType、traceId、版本三元组）。
func TestReportEventValueShapes(t *testing.T) {
	ev := reportCapture(t, &auth.Auth{AccessToken: "at", UID: "u-cn"})

	// 桌面端走内置 CLI：两个字段都是 cli / main（旧实现 default / conversation）。
	if ev["agentName"] != "cli" || ev["agentType"] != "main" {
		t.Errorf("agentName/agentType = %v/%v want cli/main", ev["agentName"], ev["agentType"])
	}
	if ev["isMaxMode"] != true || ev["source"] != "work" {
		t.Errorf("isMaxMode/source = %v/%v want true/work", ev["isMaxMode"], ev["source"])
	}
	// traceId 真机有值（32 hex），旧实现留空串。
	if s, _ := ev["traceId"].(string); len(s) != 32 || !isHex32(s) {
		t.Errorf("traceId = %q want 32 hex", ev["traceId"])
	}
	// 版本三元组：CN 5.9.3 + 对应 commit/release。
	if ev["ideVersion"] != reportVersionCN || ev["commit"] != reportCommitCN {
		t.Errorf("CN 三元组 = %v/%v want %v/%v", ev["ideVersion"], ev["commit"], reportVersionCN, reportCommitCN)
	}
	if r, _ := ev["releaseDate"].(float64); int64(r) != reportReleaseDateCN {
		t.Errorf("releaseDate = %v want %d", ev["releaseDate"], reportReleaseDateCN)
	}
	// 设备标识稳定派生：同 uid 两次一致（每请求换值等于自证非真机）。
	ev2 := reportCapture(t, &auth.Auth{AccessToken: "at", UID: "u-cn"})
	if ev["machineId"] != ev2["machineId"] || ev["sessionId"] != ev2["sessionId"] {
		t.Error("machineId/sessionId 必须按 uid 稳定派生")
	}
}

// TestReportQimeiRealmScoped qimei36 只有国内版带（双版本抓包差集恰好只有这个键）。
func TestReportQimeiRealmScoped(t *testing.T) {
	cn := reportCapture(t, &auth.Auth{AccessToken: "at", UID: "u-cn"})
	if s, _ := cn["qimei36"].(string); len(s) != 36 || !isHex36(s) {
		t.Errorf("CN qimei36 = %q want 36 hex", cn["qimei36"])
	}
	// 国际版判定走 Realm()：需要打开 global 逃生门（默认关）才认 domain 后缀。
	auth.SetGlobalEnabled(true)
	t.Cleanup(func() { auth.SetGlobalEnabled(true) })
	gl := reportCapture(t, &auth.Auth{AccessToken: "at", UID: "u-gl", Domain: "www.workbuddy.ai"})
	if _, ok := gl["qimei36"]; ok {
		t.Errorf("国际版真机不带 qimei36，不得补: %v", gl["qimei36"])
	}
	if gl["ideVersion"] != reportVersionGlobal || gl["commit"] != reportCommitGlobal {
		t.Errorf("global 三元组 = %v/%v want %v/%v", gl["ideVersion"], gl["commit"],
			reportVersionGlobal, reportCommitGlobal)
	}
}

// isHex32 / isHex36 小写 hex 校验（避免重复断言逻辑）。
func isHex32(s string) bool { return len(s) == 32 && isHex36(s) }

func isHex36(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
