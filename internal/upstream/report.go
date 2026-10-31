// report.go growth 域「对话活跃上报」接口：POST {billingBase}/v2/report。
//
// 上报体形状对齐官方桌面端 chat_request_send 事件（5.9.3 真机抓包逐字段比对）：
// 业务字段 + **设备/包指纹**合并成一条扁平事件。事件必须带 userId（=账号 uid），
// 缺失则服务端 200 但静默丢弃（实测见 REPORT-active-map.md §2）。
//
// 一条上报同时点亮 growth 连登 + 解锁 first_buddy 任务（领养前置）。
// 风控口径：每号每天 1 次即可（activity_hours 单时点），不做多时点高频上报。
package upstream

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/session"
)

// reportPath 活跃上报通道（实测）。
const reportPath = "/v2/report"

// 官方分发包的版本三元组实测值（ideVersion / commit / releaseDate）。
//
// 三者必须**内部自洽**：真机上报里 commit 是构建哈希、releaseDate 是构建时间戳，
// 同一个客户端永远成对出现。混搭（例如新版本号配旧 commit）比缺字段更容易被规则命中，
// 故这里按 realm 取整组常量，不跟 `client_version` 覆盖开关联动——那个开关只调 UA 段，
// 若同时改上报版本会让三元组失去对应关系。
const (
	reportVersionCN         = "5.9.3"
	reportCommitCN          = "d46924219e79332f49c37ccebf27292f87001b19"
	reportReleaseDateCN     = int64(1793093056315)
	reportVersionGlobal     = "5.9.4"
	reportCommitGlobal      = "fa0a0901906addaa1a4259f6df347388a5563ded"
	reportReleaseDateGlobal = int64(1793288804029)
)

// reportTriple 按 realm 返回上报用的版本三元组（见上方常量说明）。
func reportTriple(a *auth.Auth) (ver, commit string, release int64) {
	if a != nil && a.IsGlobal() {
		return reportVersionGlobal, reportCommitGlobal, reportReleaseDateGlobal
	}
	return reportVersionCN, reportCommitCN, reportReleaseDateCN
}

// reportDeviceID 由 uid 稳定派生一个 36 位小写 hex 设备标识（machineId / sessionId /
// qimei36 共用同一口径，靠 salt 区分）。
//
// 稳定派生而不是每次随机：真机上同一台设备的 machineId 跨重启恒定、sessionId 在一次
// 会话内恒定。每请求换值等于自证「这不是一台真实设备」。
func reportDeviceID(a *auth.Auth, salt string) string {
	uid := ""
	if a != nil {
		uid = a.UID
	}
	sum := sha256.Sum256([]byte(salt + ":" + uid))
	return hex.EncodeToString(sum[:18]) // 18 字节 = 36 hex
}

// reportFingerprint 桌面端设备 + 包指纹（合并进每个事件，业务字段可覆盖同名键）。
//
// 字段集来自 5.9.3 国内版真机 /v2/report 抓取。旧实现只发业务字段、不含任何设备指纹，
// 而「缺 cpuModel / 缺 machineId」正是上游侧写设备时最明显的非桌面端特征。
//
// qimei36（腾讯设备标识，36 位小写 hex）**按 realm 条件注入**：2026-10-20 双版本抓包
// 实测国内版每个事件都带、国际版一条都不带（CN 28 键 vs GL 27 键，差集恰好只有它）。
// 给国际版补这个键反而是伪造。
func reportFingerprint(a *auth.Auth) map[string]any {
	now := time.Now().UnixMilli()
	ver, commit, release := reportTriple(a)
	uid, nick := "", ""
	if a != nil {
		uid, nick = a.UID, a.Nickname
	}
	fp := map[string]any{
		"timezone":     "Asia/Shanghai",
		"reportDelay":  2000,
		"userId":       uid,
		"username":     nick,
		"userNickname": nick,
		"product":      "SaaS",
		"releaseDate":  release,
		"commit":       commit,
		"ideName":      "WorkBuddy",
		"ideType":      "WorkBuddy",
		"ideVersion":   ver,
		"machineId":    reportDeviceID(a, "machine"),
		"sessionId":    reportDeviceID(a, "session"),
		"extName":      "workbuddy-desktop",
		"extVersion":   ver,
		"os":           "win32",
		"arch":         "x64",
		"osVersion":    "10.0.28000",
		"cpuModel":     "11th Gen Intel(R) Core(TM) i5-11320H @ 3.20GHz",
		"cpuCores":     8,
		"memorySize":   16,
		"timestamp":    now,
		"presentAt":    now,
	}
	if a != nil && !a.IsGlobal() {
		fp["qimei36"] = reportDeviceID(a, "qimei")
	}
	return fp
}

// billingJSON 发 billing 域（billingBase，codebuddy.cn）请求并解信封；body 为 nil 时不带请求体。
// 与 travel.go 的 growthJSON 对称（growth 域走 chatBase + BillingHeaders；billing 域走 billingBase）。
// report/checkin 等 billing 端点共用：请求头统一 BillingHeaders，信封与错误语义同 doJSON。
func (c *Client) billingJSON(a *auth.Auth, method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.billingBase(a)+path, rdr)
	if err != nil {
		return nil, err
	}
	c.BillingHeaders(req, a)
	return c.doJSON(req)
}

// chatRequestEvent 客户端 chat_request_send 事件的**chat 专属**字段（5.9.3 真机键集）。
//
// 设备/包指纹由 reportFingerprint 合并进来，故这里不再重复 userId / timestamp /
// presentAt / reportDelay。
//
// 与旧实现的差异（每一处都是「多发或漏发」造成的形态偏差）：
//   - 删掉 maxToken / maxSteps / temperature / maxRetries / skillCount / totalCount /
//     fileUri / expertId —— 5.9.3 的 chat_request_send 里**没有这些键**。旧结构是从更早
//     的客户端抄的，多出来的空值键反而是可判别特征。
//   - 补 isMaxMode / source / inputAttemptId / vcs* —— 5.9.3 必带。
//   - agentName / agentType 由 "default" / "conversation" 改为 **"cli" / "main"**
//     （真机桌面端走内置 CLI，两个字段都是 cli/main）。
//   - traceId 不再留空：真机有值（32 hex 随机）。
type chatRequestEvent struct {
	EventCode             string `json:"eventCode"`
	Mode                  string `json:"mode"`
	ConversationID        string `json:"conversationId"`
	RequestID             string `json:"requestId"`
	InputLength           int    `json:"inputLength"`
	RequestModelID        string `json:"requestModelId"`
	RequestModelName      string `json:"requestModelName"`
	IsPlan                bool   `json:"isPlan"`
	IsAutoExecuteTerminal bool   `json:"isAutoExecuteTerminal"`
	IsAutoModify          bool   `json:"isAutoModify"`
	CodebaseEnable        bool   `json:"codebaseEnable"`
	IsMaxMode             bool   `json:"isMaxMode"`
	Source                string `json:"source"`
	MentionContexts       []any  `json:"mentionContexts"`
	KnowledgeID           []any  `json:"knowledgeId"`
	KnowledgeName         []any  `json:"knowledgeName"`
	CodebaseID            string `json:"codebaseId"`
	MentionContextCount   int    `json:"mentionContextCount"`
	Command               string `json:"command"`
	InputAttemptID        string `json:"inputAttemptId"`
	RecommendID           string `json:"recommendId"`
	SkillID               string `json:"skillId"`
	TraceID               string `json:"traceId"`
	RootRequestID         string `json:"rootRequestId"`
	ParentConversationID  string `json:"parentConversationId"`
	AgentName             string `json:"agentName"`
	AgentType             string `json:"agentType"`
}

// reportChatEventMap 把 chat 专属字段与设备/包指纹拼成一条完整事件（真机键集）。
//
// 拼接顺序是「指纹先、业务后」：业务字段覆盖同名键（真机两侧同名键值本来就一致，
// 这里只是让调用方保有覆盖能力，与官方客户端的构造顺序无关）。
func reportChatEventMap(a *auth.Auth, ev chatRequestEvent) map[string]any {
	m := reportFingerprint(a)
	m["eventCode"] = ev.EventCode
	m["mode"] = ev.Mode
	m["conversationId"] = ev.ConversationID
	m["requestId"] = ev.RequestID
	m["inputLength"] = ev.InputLength
	m["requestModelId"] = ev.RequestModelID
	m["requestModelName"] = ev.RequestModelName
	m["isPlan"] = ev.IsPlan
	m["isAutoExecuteTerminal"] = ev.IsAutoExecuteTerminal
	m["isAutoModify"] = ev.IsAutoModify
	m["codebaseEnable"] = ev.CodebaseEnable
	m["isMaxMode"] = ev.IsMaxMode
	m["source"] = ev.Source
	m["mentionContexts"] = ev.MentionContexts
	m["knowledgeId"] = ev.KnowledgeID
	m["knowledgeName"] = ev.KnowledgeName
	m["codebaseId"] = ev.CodebaseID
	m["mentionContextCount"] = ev.MentionContextCount
	m["command"] = ev.Command
	m["inputAttemptId"] = ev.InputAttemptID
	m["recommendId"] = ev.RecommendID
	m["skillId"] = ev.SkillID
	m["traceId"] = ev.TraceID
	m["rootRequestId"] = ev.RootRequestID
	m["parentConversationId"] = ev.ParentConversationID
	m["agentName"] = ev.AgentName
	m["agentType"] = ev.AgentType
	// VCS 段：真机恒带四个键（未知仓库时为 unknown + 三个空串），缺了是可判别形态。
	m["vcsType"] = "unknown"
	m["vcsRepo"] = ""
	m["vcsBranchName"] = ""
	m["vcsRevId"] = ""
	// buddy 段 + OTel 语义键：真机每个事件都带，buddyId/Name 为空串。
	m["buddyId"] = ""
	m["buddyName"] = ""
	m["codebuddy.session_id"] = ev.ConversationID
	m["codebuddy.conversation_request_id"] = ev.RequestID
	return m
}

// ReportChatActivity 向上游发送一条对话活跃上报（chat_request_send）。
// conversationID 由调用方生成（客户端同形 UUIDv7），无需真实会话——服务端不校验一致性。
// requestID 为本轮请求独立标识（多轮同会话上报时各条不同）；空时回落 conversationID。
// 错误语义与 doJSON 一致：HTTP 非 2xx / 业务 code != 0 → *Error。
//
// 与 issue #35 会话头族（X-Conversation-Request-ID）保持独立：本接口是 growth 域
// 活跃上报（仅点亮连登/first_buddy，每号每天 1 次），event.requestId 是事件级标识，
// 后台按 growth 事件去重，不走 chat 后台的 X-Conversation-Request-ID 聚合——对齐
// 官方 chat_request_send 事件形状，刻意不复用聚合主键。
func (c *Client) ReportChatActivity(a *auth.Auth, conversationID, requestID string) error {
	if requestID == "" {
		requestID = conversationID
	}
	ev := chatRequestEvent{
		EventCode:             "chat_request_send",
		Mode:                  "craft",
		ConversationID:        conversationID,
		RequestID:             requestID,
		InputLength:           12,
		RequestModelID:        "deepseek-v4-flash",
		RequestModelName:      "DeepSeek V4 Flash",
		IsPlan:                false,
		IsAutoExecuteTerminal: false,
		IsAutoModify:          false,
		CodebaseEnable:        false,
		IsMaxMode:             true,
		Source:                "work",
		MentionContexts:       []any{},
		KnowledgeID:           []any{},
		KnowledgeName:         []any{},
		CodebaseID:            "",
		MentionContextCount:   0,
		Command:               "",
		InputAttemptID:        session.NewConversationID(),
		RecommendID:           "",
		SkillID:               "",
		TraceID:               session.NewTraceID(),
		RootRequestID:         requestID,
		ParentConversationID:  conversationID,
		AgentName:             "cli",
		AgentType:             "main",
	}
	raw, err := json.Marshal([]map[string]any{reportChatEventMap(a, ev)})
	if err != nil {
		return err
	}
	_, err = c.billingJSON(a, http.MethodPost, reportPath, json.RawMessage(raw))
	return err
}
