// Package headers 构造三类上游请求头（common / chat / billing / refresh）。
//
// 形态来源：官方客户端真机抓包逐头对照（国际版 5.9.4 / 国内版 5.9.3，CLI 2.165.0）。
// 原则是「**按代码路径分族**」而不是「把所有头堆到每条请求上」——多一个客户端从不发的
// 头，与少一个头一样可判别，而且更容易被规则命中（issue #51）。
package upstream

import (
	"net/http"
	"strings"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/session"
)

const (
	// defaultClientVersionCN / defaultClientVersionGlobal 出站 WorkBuddy 客户端版本段
	// （UA 的 `<brand>/<ver>` 与 X-IDE-Version）。
	//
	// 取值来源：官方分发包 resources/install-manifest.json（appVersion）+ cli/product.json
	// （genieVersion）。
	//
	//	国内版 WorkBuddy      5.9.3（endpoint www.workbuddy.cn，DefaultModelOptimizationOff=false）
	//	国际版 WorkBuddy AI   5.9.4（endpoint www.workbuddy.ai，DefaultModelOptimizationOff=true）
	//
	// 旧实现写死 5.5.4：版本段落后于当前分发包本身就是最直白的非官方特征。
	// config upstream.client_version 可覆盖（空 = 按 realm 取内置默认）。
	defaultClientVersionCN     = "5.9.3"
	defaultClientVersionGlobal = "5.9.4"

	// defaultCliVersion 出站 UA 中 `CLI/<ver>` 段版本。对齐官方内置 CLI **2.165.0**
	// （cli/package.json 的 @genie/agent-cli version；5.9.3/5.9.4 真机 UA 实测同值）。
	// config upstream.cli_version 可覆盖。
	defaultCliVersion = "2.165.0"

	// originRefererCN / originRefererGlobal 只给**web 层**（渲染器/登录页那类浏览器语义
	// 的请求）用，RPC 层与 chat 都不发（见 CommonHeaders 注释）。
	//
	// CN 侧旧值 `https://www.codebuddy.cn` 是 CodeBuddy CLI 时代的遗留：5.9.3 桌面端真机
	// 全量扫描（661 条带 Origin/Referer 的请求）里**没有一条**把 codebuddy.cn 当 Origin——
	// 一律是 `https://www.workbuddy.cn`。codebuddy.cn 只是 billing 的 **API 主机名**。
	originRefererCN     = "https://www.workbuddy.cn"
	originRefererGlobal = "https://www.workbuddy.ai"

	// xDomainCN / xDomainGlobal chat / billing 的 X-Domain 取值（**裸主机名**，不带 scheme）。
	// 真机实测两域各恒一条：CN `www.workbuddy.cn`、global `www.workbuddy.ai`。
	// 注意它不是 API 主机名（CN 的 chat base 是 copilot.tencent.com），而是客户端声明的
	// 「我在哪个域上」——所以按 realm 取常量，不从 base URL 推导。
	xDomainCN     = "www.workbuddy.cn"
	xDomainGlobal = "www.workbuddy.ai"

	// billingUAString billing 域出站 UA：**恒为 axios 默认串**。
	//
	// 2026-10-20 双版本真机抓包实测：两版 billing 层（summary / paid-packages /
	// free-packages / checkin-activity-status / ambassador status）发的都是
	// `axios/1.16.1`，与客户端自身 UA 完全不同——billing 域的 RestOperations 走 axios
	// 实例而非 SDK 客户端。旧实现发 `WorkBuddy/<ver>` 是「按客户端 UA 推理」的产物。
	billingUAString = "axios/1.16.1"
)

// 官方 chat 出站的自报 SDK 指纹（国际版 5.9.4 与国内版 5.9.3 实测**完全一致**）。
//
// 这些是 openai-node SDK 自动加的 x-stainless-* 头 + 业务语义头 x-agent-type/intent。
// 网关是 Go 实现，没有对应的 SDK 运行时；这里按官方实测值固定，理由是「客户端形态
// 一致性」优先——不发这几个头，上游看到的是一份缺了 SDK 段的残缺头集合。
// 一旦客户端版本变化（package-version / node 版本），这几个常量需要跟着更新。
const (
	clientArch    = "x64"
	clientOS      = "Windows"
	clientLang    = "js"
	clientRuntime = "node"
	clientNodeVer = "v22.21.1"
	clientSDKVer  = "6.25.0"

	agentTypeMain    = "main"
	agentIntentCraft = "craft"

	// agentTypeAuxiliary 辅助链档位：真机 resolveModelCallAgentType 的次要分支取值，
	// 用于非用户主对话的模型调用（标题/摘要/网关自发任务）。主对话恒 main。
	agentTypeAuxiliary = "auxiliary"

	// agentRuntimeLocal 出站 X-Agent-Runtime 取值（桌面端本地 agent）。
	// 值是枚举 ["local-agent","cloud-agent","agent-agent"] 之一，桌面端本地对话取
	// local-agent（客户端常量表与 cli resolveAgentRuntime 同源）。
	agentRuntimeLocal = "local-agent"
)

// X-Agent-Type 的服务层取值（ChatMeta.AgentType 用）：空串等价 AgentTypeMain。
const (
	AgentTypeMain      = agentTypeMain
	AgentTypeAuxiliary = agentTypeAuxiliary
)

// originRefererFor 按账号 realm 返回 Origin/Referer 基础域（**仅 web 层使用**）：
// global → https://www.workbuddy.ai；cn（含全局开关未开）→ https://www.workbuddy.cn。
func originRefererFor(a *auth.Auth) string {
	if a != nil && a.IsGlobal() {
		return originRefererGlobal
	}
	return originRefererCN
}

// clientVersion 生效的 WorkBuddy 客户端版本：Client.ClientVersion 非空则取之，
// 否则按 realm 取内置默认（CN 5.9.3 / global 5.9.4）。
func (c *Client) clientVersion(a *auth.Auth) string {
	if c != nil && c.ClientVersion != "" {
		return c.ClientVersion
	}
	if a != nil && a.IsGlobal() {
		return defaultClientVersionGlobal
	}
	return defaultClientVersionCN
}

// cliVersion 生效的 CLI 版本：Client.CliVersion 非空则取之，否则内置默认 defaultCliVersion。
func (c *Client) cliVersion() string {
	if c != nil && c.CliVersion != "" {
		return c.CliVersion
	}
	return defaultCliVersion
}

// uaBrandFor 出站 UA 的**品牌段**：按 realm 取，两版形态不同（不同分发包）。
//
//   - 国内版 5.9.3（productName `WorkBuddy`）→ `WorkBuddy`，**驼峰原样**；
//   - 国际版 5.9.4（productName `WorkBuddy AI`）→ `workbuddy-ai`，**小写连字符 slug**。
//
// 为什么两版规则不同：brand 段来自 buildUserAgent() 的 applicationName/productName，
// 而 applicationName 在打包期被改写。CN 包直接沿用驼峰 productName；GL 包走了
// Electron 的 userAgentFallback 替换路径（asciiSlug：非字母数字换成 `-` 并转小写），
// 于是 `WorkBuddy AI` → `workbuddy-ai`。
//
// 反例警示（本仓库旧实现踩过）：曾把 global 品牌写成 `WorkBuddy AI`（带空格驼峰），
// 那是 productName 原文、不是打包后的 slug，与真机逐字不符；也曾把 CN 写成小写
// `workbuddy`——CN 的每个业务请求都是驼峰，写成 slug 反而成了最明显的非官方指纹。
func uaBrandFor(a *auth.Auth) string {
	if a != nil && a.IsGlobal() {
		return "workbuddy-ai"
	}
	return "WorkBuddy"
}

// defaultWorkBuddyUAFor 组装 **chat 链**（OpenAI SDK buildUserAgent）出站 UA：
// `WorkBuddy/<ver> <brand>/<ver> CLI/<cliVer>`
//
// 首段**恒为驼峰 `WorkBuddy`**，只有第二段随 realm 换品牌 slug：
//
//	CN 5.9.3     → WorkBuddy/5.9.3 WorkBuddy/5.9.3 CLI/2.165.0
//	global 5.9.4 → WorkBuddy/5.9.4 workbuddy-ai/5.9.4 CLI/2.165.0
//
// 官方 chat 走 OpenAI SDK（请求带 x-stainless-* 头），SDK 的 buildUserAgent 把首段固定成
// productName 的驼峰形式 `WorkBuddy/<ver>`，第二段才用打包期改写后的 slug。
// 把首段也写成 realm 品牌（global → `workbuddy-ai/… workbuddy-ai/…`）与真机不符——
// 那是 RPC 族的形态，chat 链路从不用它。
func (c *Client) defaultWorkBuddyUAFor(a *auth.Auth) string {
	ver := c.clientVersion(a)
	return "WorkBuddy/" + ver + " " + uaBrandFor(a) + "/" + ver + " CLI/" + c.cliVersion()
}

// rpcUAFor 桌面端 **RPC/REST 层**出站 UA（不是 chat 那条 SDK 链）。
//
// 2026-10-30 双版本真机抓包（同一台机器、同一会话）实测两族并存：
//
//	chat   → WorkBuddy/<ver> <brand>/<ver> CLI/<cli>   （SDK buildUserAgent，首段恒驼峰）
//	RPC    → <brand>/<ver> <brand>/<ver> CLI/<cli>     （首段即品牌 slug）
//
// RPC 族覆盖 /v2/report、/v2/chat/busy、/console/as/*、/v2/plugin/auth/token/refresh、
// /v2/as/scheduler/tasks、/v2/user-asset/*、/portal/*：
//
//	CN 5.9.3     → WorkBuddy/5.9.3 WorkBuddy/5.9.3 CLI/2.165.0
//	global 5.9.4 → workbuddy-ai/5.9.4 workbuddy-ai/5.9.4 CLI/2.165.0
//
// CN 下与 userAgent() 同值（品牌段本来就是驼峰），只有 global 会分叉——旧实现让
// global 的 RPC 请求也发 `WorkBuddy/…` 首段，与真机不符。
func (c *Client) rpcUAFor(a *auth.Auth) string {
	if c != nil && c.UserAgent != "" {
		return c.UserAgent
	}
	ver := c.clientVersion(a)
	brand := uaBrandFor(a)
	return brand + "/" + ver + " " + brand + "/" + ver + " CLI/" + c.cliVersion()
}

// userAgent 返回 chat 链出站 UA。
// 优先级：Client.UserAgent（config user_agent）显式覆盖 > 按账号 realm 的默认形态。
func (c *Client) userAgent(a *auth.Auth) string {
	if c != nil && c.UserAgent != "" {
		return c.UserAgent
	}
	return c.defaultWorkBuddyUAFor(a)
}

// billingUA billing 白名单类（billing/checkin/banner）出站 UA：恒为 axios 默认串。
// 显式 client_name="SaaS" 时仍不设 UA（保留既有逃生开关，还原 Go 默认 UA）。
func (c *Client) billingUA(a *auth.Auth) string {
	if c == nil || c.attributionClientName() == "SaaS" {
		return ""
	}
	return billingUAString
}

// resolveDeviceToken 解析本次请求的 X-Device-Token 取值。
// 优先级：auth.Auth.DeviceToken（每号）> Client.DeviceToken（config 全局）> 文件兜底。
// 三者皆空/读失败则返回空串（调用方不注入该头，优雅降级）。
// 为什么不放进 CommonHeaders：鉴权/刷新类头（refresh / FetchModels）给设备 token
// 无意义且可能被上游风控误判为异常客户端；只在 chat/billing 业务请求注入。
func (c *Client) resolveDeviceToken(a *auth.Auth) string {
	if a != nil && a.DeviceToken != "" {
		return a.DeviceToken
	}
	if c != nil && c.DeviceToken != "" {
		return c.DeviceToken
	}
	if c != nil && c.DeviceTokenFile != "" {
		return readDeviceTokenFile(c.DeviceTokenFile)
	}
	return ""
}

// injectDeviceToken 在 req 注入 X-Device-Token 头（仅当取到非空 token）。
func (c *Client) injectDeviceToken(req *http.Request, a *auth.Auth) {
	if tok := c.resolveDeviceToken(a); tok != "" {
		req.Header.Set("X-Device-Token", tok)
	}
}

// CommonHeaders 设置 **RPC/REST 层**共享请求头（refresh / report / busy / console-as /
// user-asset / portal / 模型目录探测）。
//
// 这一层刻意保持「薄」：真机这些路径只发 Accept / X-Product / User-Agent 三件，
// 其余头都是 chat 那条链专属（见 ChatHeaders）。
func (c *Client) CommonHeaders(req *http.Request, a *auth.Auth) {
	req.Header.Set("Content-Type", "application/json")
	// Accept：RPC/REST 层真机恒发宽松值 `application/json, text/plain, */*`
	// （/v2/report、/v2/chat/busy、/console/as/*、refresh、user-asset、portal 全部同值）。
	// chat 那条链只发 `application/json`，由 ChatHeaders 收窄覆盖。
	req.Header.Set("Accept", "application/json, text/plain, */*")
	// X-Product：SKU 段，恒 "SaaS"（官方两版实测都不是产品名）。
	req.Header.Set("X-Product", "SaaS")
	// X-Requested-With：**RPC/REST 层不发**。
	//
	// 2026-10-30 双版本全量扫描（1182 条 flow，125 条路径）按 UA 族切开后形态是干净的：
	//
	//	UA workbuddy-ai/…                         → 无 XRW（30/30）   ← RPC 层（node fetch）
	//	UA WorkBuddy/<ver> WorkBuddy/<ver> CLI/…  → 无 XRW（26/26）   ← CN 的 RPC 层
	//	UA WorkBuddy/<ver> <slug>/<ver> CLI/…     → 有 XRW（10/10）   ← 渲染器侧另一条链
	//
	// 即「带不带 XRW」由**哪条代码路径发的**决定，不是按 realm 切。本函数服务的是
	// RPC 层，恒不带；chat 那条（SDK 链）**带**，由 ChatHeaders 自己补。
	//
	// Origin / Referer：**不发**。桌面端 RPC 层是 node fetch 调用，没有浏览器同源语义，
	// 真机这些路径一律不带这两个头。旧实现在 CommonHeaders 里塞 Origin+Referer，等于给
	// 每个 RPC 请求盖一个浏览器痕迹——反而不是客户端形态。需要浏览器语义的调用方
	// （登录页 / web 层）自行设置 originRefererFor。
	//
	// X-CodeBuddy-Request：**只有 chat 带**（见 ChatHeaders）。真机全量扫描——7 条
	// /v2/chat/completions 全带，而 busy / report / refresh / v3-config / billing 等
	// 30+ 条业务路径**一条都没有**。
	//
	// Accept-Language：客户端 RPC 层同样不发（refresh / v3-config / busy / report / chat
	// 逐条确认均无该头）。浏览器语义的 web 层才由 Chromium 自动带。
	//
	// X-Machine-ID / X-Session-ID **已移除**（2026-10-28，真机 + 静态反编译双重确认）：
	// 客户端**从不发这两个头**——app.asar（main/ + renderer/）与内置 cli/dist 全量字符串
	// 里 `X-Machine-ID` 命中 0 次，`X-Session-ID` 只作为常量表条目存在、从不出现在出站头
	// 里；真机 chat 的 40 个出站头逐一比对也没有它们。设备标识是放在 /v2/report 的
	// **body** 里（machineId / sessionId 字段），不是头。旧实现按 uid 派生两个 36 hex
	// 塞进头里，是纯粹的网关自造指纹。
	req.Header.Set("User-Agent", c.rpcUAFor(a))
}

// acceptLanguageFor 返回 Accept-Language。
//
// **出站已不再调用本函数**：真机确认桌面端 RPC 层（chat / refresh / v3-config / busy /
// report / billing）全部不发 Accept-Language——只有浏览器语义的 web 层才由 Chromium
// 自动带。保留函数是因为 web 层将来若要补这个头可直接复用。
//
// 旧实现「global → en-US、cn → zh-CN」是按账号域臆测的：真机里该头跟随操作系统区域，
// 同一台中文机器上两域表现一致（多数请求根本不带，带上时以 `zh` / `zh-CN` 为主，
// `en-US` 只在 GL 出现 1 次）。把国际版所有请求都打成 en-US，反而成了与真机不符的指纹。
func acceptLanguageFor(a *auth.Auth) string {
	return "zh"
}

// injectCodeBuddyRequest 在 req 注入 X-CodeBuddy-Request: 1。
// **仅 chat 路径调用**：真机只有 /v2/chat/completions 带该头（7/7），其余 30+ 业务
// 路径一条都没有。
func (c *Client) injectCodeBuddyRequest(req *http.Request) {
	req.Header.Set("X-CodeBuddy-Request", "1")
}

// injectGlobalChatHeaders global 账号的 chat 专属域声明头。
//
// 只发 X-Domain：桌面端个人号实测并**不**发 X-No-Enterprise-Id（旧实现按 CodeBuddy CLI
// 约定补的哨兵头），国际版与国内版真机报文里都不存在该头。多发一个客户端从不发的头
// 同样是指纹。
func (c *Client) injectGlobalChatHeaders(req *http.Request, a *auth.Auth) {
	if a == nil || !a.IsGlobal() {
		return
	}
	req.Header.Set("X-Domain", xDomainGlobal)
}

// ChatMeta 一次 chat 出站的会话头族元数据（后台按 X-Conversation-Request-ID 聚合请求）。
//
// handler 在轮转循环外生成 conversationID / conversationRequestID，循环内每次出站
// 复用同值；messageID（消息级，每条独立）由 ChatHeaders 内部生成，无需外部可见。
type ChatMeta struct {
	// ConversationID X-Conversation-ID：body 提取的入站值，空则按 CacheKeyID 派生
	// （透传优先，不伪造客户端未声明的会话）。
	ConversationID string
	// CacheKeyID prompt_cache_key 的会话段来源（网关派生的粘性会话键 sessKey）。
	// 与 ConversationID 分开：头是「客户端声明的会话」，缓存槽是「网关认的同一会话」——
	// 无 conversation_id 的客户端（Claude Code 等）只填这个。
	CacheKeyID string
	// ConversationRequestID X-Conversation-Request-ID：对话轮级聚合主键，必发。
	ConversationRequestID string
	// RootRequestID X-Root-Request-ID：**会话级根**请求 ID（真机取 session.meta.rootRequestId，
	// 会话首个请求生成、整会话复用，与轮级的 ConversationRequestID 不是同一个值）。
	// 空 → 回落 ConversationRequestID：真机 chat 7/7 都带该头，零值 meta 的调用方
	// （面板任务、测试）不能因为「没有根」就把头整个丢掉。
	RootRequestID string
	// TraceID X-Trace-ID：入站透传值，空则现造一条**独立**的纯随机 32 hex（**不**回落
	// conversationRequestID——真机两者从无相等，恒等本身就是网关自造的痕迹）。
	TraceID string
	// AgentType X-Agent-Type 场景枚举：空 = main（用户主对话）。网关自发、不属于用户
	// 对话的模型调用填 auxiliary。真机另有 compaction / subagent / team 三档，网关没有
	// 对应路径，故不预留假值。
	AgentType string
}

// ChatHeaders 在 common 之上加 chat 专属的账号头。
// clientIP 为本次请求的客户端 IP（按参数传递，不读共享字段——避免并发串扰）；
// PassthroughIP=false 或 clientIP 为空时不注入 IP 头。
// meta 为会话头族元数据，见 injectConversationHeaders。
func (c *Client) ChatHeaders(req *http.Request, a *auth.Auth, clientIP string, meta ChatMeta) {
	c.CommonHeaders(req, a)
	// chat 出站头剥离：CommonHeaders 是 REST 层通用形态，桌面端 chat 并不发这几个
	// （两版客户端真机实测逐头对照）：
	//   Origin / Referer     ← CLI 客户端没有浏览器语义，chat 路径从不发
	//   Accept-Language      ← 桌面端 chat 不发
	// X-Machine-ID / X-Session-ID 已从 CommonHeaders 整体移除（客户端从不发），
	// 这里保留 Del 是给外部直接构造的 req 兜底，无副作用。
	req.Header.Del("Origin")
	req.Header.Del("Referer")
	req.Header.Del("Accept-Language")
	req.Header.Del("X-Machine-ID")
	req.Header.Del("X-Session-ID")
	// Accept 与官方 chat 完全一致：客户端只发 application/json，照样收 SSE 流
	// （两版实测；旧值多带 ", text/event-stream" 属自造）。
	// Accept-Encoding 刻意不设：官方发 br/gzip 全集，但显式设置会关闭 net/http 的
	// 透明解压，收到 br 时网关没有 brotli 解码器——收益（一个头）远小于风险。
	req.Header.Set("Accept", "application/json")
	// X-Requested-With：**chat 必带**（CommonHeaders 已不再设它）。
	// 真机 /v2/chat/completions 5/5 条全带，而 RPC 族一条都没有——chat 是唯一带的业务链。
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	// User-Agent：chat 走 OpenAI SDK 的 buildUserAgent，首段**恒为驼峰 productName**
	// `WorkBuddy/<ver>`，第二段才是品牌 slug——与 RPC/REST 层（首段即 slug）不同。
	// CommonHeaders 已按 RPC 形态设过，这里按 chat 形态覆盖。
	req.Header.Set("User-Agent", c.userAgent(a))
	// AccessToken 加锁快照：keepalive 定时刷新会在 a.mu 内改写它，锁外直读构成数据竞争
	// （见 auth.AccessTokenValue 注释与 upstream.TestChatHeadersRacesRefreshToken）。
	if at := a.AccessTokenValue(); at != "" {
		req.Header.Set("Authorization", "Bearer "+at)
	} else {
		req.Header.Set("X-No-Authorization", "true")
	}
	if a.UID != "" {
		req.Header.Set("X-User-Id", a.UID)
	} else {
		req.Header.Set("X-No-User-Id", "true")
	}
	// 安全红线：绝不在 chat 请求里携带 X-Refresh-Token。
	//
	// 企业/域头：**只发有值的**。桌面端个人号实测两个方向都不发 X-No-* 哨兵头
	// （X-No-Enterprise-Id / X-No-Department-Info 逐头对照均无）。旧实现用 X-No-*
	// 声明「无企业」，那是 CodeBuddy CLI 的约定，不是 WorkBuddy 桌面端的形态。
	if a != nil && !a.IsGlobal() {
		if a.EnterpriseID != "" {
			req.Header.Set("X-Enterprise-Id", a.EnterpriseID)
		}
		// X-Domain 必发：真机 chat 恒带 `X-Domain: www.workbuddy.cn`（裸主机名）。
		// 该头缺失同样是可判别形态（客户端从无「缺 X-Domain 的 chat」）。
		req.Header.Set("X-Domain", xDomainCN)
	} else {
		c.injectGlobalChatHeaders(req, a)
	}
	// 用量归属头：默认伪造 WorkBuddy 桌面端指纹（client_name="SaaS" 还原旧行为）。
	c.injectAttribution(req, a)
	// X-Private-Data：按**发行版**取（不是账号形态），见 privateDataHeaderValue。
	req.Header.Set("X-Private-Data", privateDataHeaderValue(a))
	// 客户端 IP 透传：仅当 PassthroughIP=true 且本次请求 clientIP 参数非空（见 handler 设置）。
	// 缺省 false（反代安全边界：不把内网/代理 IP 暴露给上游）。
	c.injectClientIP(req, clientIP)
	// 设备风控头：auth 每号 > config 全局 > 文件兜底；空则不注入（见 resolveDeviceToken）。
	c.injectDeviceToken(req, a)
	// 会话头族（对话/请求/消息/链路/baggage/acp），见 injectConversationHeaders。
	c.injectConversationHeaders(req, a.UID, meta)
	// SDK / agent 段指纹头（x-stainless-* / x-agent-type / x-agent-intent）。
	c.injectClientSDKHeaders(req, meta.AgentType)
	// X-CodeBuddy-Request: 1 —— 真机**仅 chat** 携带，故从 CommonHeaders 移到这里。
	c.injectCodeBuddyRequest(req)
	// X-Agent-Runtime: local-agent —— 真机 chat 必带（本地 agent 运行时）。
	req.Header.Set("X-Agent-Runtime", agentRuntimeLocal)
	// baggage：W3C 传播头，客户端把会话/轮次 ID 以逗号分隔的 k=v 列表塞进去。
	c.injectBaggage(req, a.UID, meta)
}

// privateDataHeaderValue 出站 X-Private-Data 取值。
//
// 客户端逻辑（cli/dist 反编译 applyPrivateDataHeaderPolicy）：
//
//	A = resolveModelOptimizationEnabled({explicitEnabled, productDefaultEnabled, isEnterpriseEdition})
//	p[PRIVATE_DATA_HEADER] = A ? "false" : "true"
//	resolveModelOptimizationEnabled = (t,i,n) => typeof t=="boolean" ? t : !n && (i ?? true)
//
// 其中 productDefaultEnabled = !productFeatures.DefaultModelOptimizationOff，i.e.
//
//	真机值 = 企业版 || (发行版 DefaultModelOptimizationOff==true) ? "true" : "false"
//
// **判定维度是发行版，不是账号形态**——双版本真机 + 拆包双向确认：
//
//	国际版 5.9.4  product.json DefaultModelOptimizationOff = true  → 个人号发 "true"（实测 4/4 条）
//	国内版 5.9.3  product.json DefaultModelOptimizationOff = false → 个人号发 "false"（实测）
//
// 旧实现按「企业号 → true，其余 false」，把国际版个人号判成 false，方向正好相反
// （每条 chat 都错）。网关没有「用户手动关优化」这个开关可读，故按 realm 取发行版默认。
func privateDataHeaderValue(a *auth.Auth) string {
	if a == nil {
		return "false"
	}
	if a.IsGlobal() {
		// 国际版（workbuddy.ai）发行版默认关模型优化 → 声明 true。
		return "true"
	}
	return "false"
}

// injectConversationHeaders 注入官方客户端会话头族。
//
// 三层语义：
//   - X-Conversation-ID：会话级，多轮稳定。客户端声明（body conversationId）→ 原值透传；
//     客户端没声明 → 用网关的 uid 隔离会话键派生；两者都没有（零值 ChatMeta）→ 现造
//     一个客户端同形态的 v7 UUID，**不留空**——上游按三件套是否齐全做渠道校验，缺任一头
//     即 400 code=11-128 "conversation id missing"。
//     形态必须 v7（真机恒 `01a1815d-…` 时间有序），故走 buildTimeOrderedUUID 而非
//     buildCacheKey——后者是 v4，摆到该头上就是与真机相反的位布局。
//   - X-Conversation-Request-ID：对话轮级聚合主键，必发；同轮内所有 tool call/重试/
//     换号/降级复用同一个。
//   - X-Conversation-Message-ID = X-Request-ID：消息级，每条独立（32 hex，带毫秒前缀）。
//
// 链路族：
//   - X-Root-Request-ID：会话级根，与轮级值**不同**（旧实现恒等于轮级键，两族恒等本身
//     就是网关自造的痕迹——真机语义分层：根 vs 轮）。
//   - X-Trace-ID：入站透传，否则现造**独立**纯随机值（**不**回落轮级键：真机两族从无相等）。
//   - X-B3-TraceId 跟随 trace；SpanId / ParentSpanId 各自独立随机（官方 span 与 trace、
//     request 均无关系；旧实现取 messageID[:16] 会把 12 hex 时间前缀带出来）。
//   - b3 单头 + traceparent：官方两版都双发，缺这两个头时只剩 X-B3-* 三件套，
//     而客户端从不只发三件套。
//   - acp-connection-id：官方列进**标准出站头族**（与 X-Conversation-ID 同一批设置），
//     每 session 生成一次、同会话多轮复用、跨会话不同。
func (c *Client) injectConversationHeaders(req *http.Request, uid string, meta ChatMeta) {
	convReqID := meta.ConversationRequestID
	if convReqID == "" {
		// 零值 meta（直接调 ChatHeaders 的调用方/测试）也要保证聚合主键必发：
		// 本级补一个 32 hex，调用方（handler）已生成稳定的不走到这里。
		convReqID = session.NewMessageID()
	}
	messageID := session.NewMessageID()
	src := cacheKeySource(meta)
	if meta.ConversationID != "" {
		req.Header.Set("X-Conversation-ID", meta.ConversationID)
	} else if src != "" {
		// 客户端没声明会话：用与 prompt_cache_key 同源的 uid 隔离键填该头。
		// 不带 uid 段的话，同一客户端会话打在不同账号上会共用同一个上游缓存槽。
		req.Header.Set("X-Conversation-ID", buildTimeOrderedUUID(uid, src, ""))
	} else {
		// 零值 ChatMeta：现造客户端同形态的时间有序 UUIDv7。
		req.Header.Set("X-Conversation-ID", session.NewTimeOrderedID())
	}
	req.Header.Set("X-Conversation-Request-ID", convReqID)
	req.Header.Set("X-Conversation-Message-ID", messageID)
	req.Header.Set("X-Request-ID", messageID)
	rootReqID := meta.RootRequestID
	if rootReqID == "" {
		rootReqID = convReqID
	}
	req.Header.Set("X-Root-Request-ID", rootReqID)
	traceID := meta.TraceID
	if traceID == "" {
		traceID = session.NewTraceID()
	}
	req.Header.Set("X-Trace-ID", traceID)
	b3Trace := traceID
	if !validTraceID(b3Trace) {
		b3Trace = messageID // 入站透传值非法（含横线/超长/非 hex）→ 回落恒 32 hex
	}
	req.Header.Set("X-B3-TraceId", b3Trace)
	spanID := session.NewSpanID()
	parentSpanID := session.NewSpanID()
	req.Header.Set("X-B3-SpanId", spanID)
	req.Header.Set("X-B3-ParentSpanId", parentSpanID)
	req.Header.Set("X-B3-Sampled", "1")
	// b3 = trace-span-flags-parent；traceparent = 00-trace-span-flags。
	req.Header.Set("b3", b3Trace+"-"+spanID+"-1-"+parentSpanID)
	req.Header.Set("traceparent", "00-"+b3Trace+"-"+spanID+"-01")
	// acp-connection-id：同 (账号, 会话) 稳定、与 X-Conversation-ID 不同值（多一个 purpose
	// 段即可），无会话源时现造一个。
	if src != "" {
		req.Header.Set("acp-connection-id", buildTimeOrderedUUID(uid, src, "acp"))
	} else {
		req.Header.Set("acp-connection-id", session.NewTimeOrderedID())
	}
}

// injectBaggage 注入 W3C `baggage` 头，对齐客户端 chat 出站形态。
//
// 真机逐字（main 与 auxiliary 两条链同形）：
//
//	baggage: buddyId=,buddyName=,codebuddy.session_id=<X-Conversation-ID 同值>,
//	         codebuddy.conversation_request_id=<X-Conversation-Request-ID 同值>
//
// 客户端由 OTel W3CBaggagePropagator 序列化（空值保留 `k=` 形态），故 buddyId /
// buddyName 恒为空串但仍在列表里。这里照抄：两个 buddy 键发空值，两个 codebuddy.*
// 键取本次出站真实值，保证与同请求的 X-Conversation-ID / X-Conversation-Request-ID
// 一致（头体不一致本身就是可判别形态）。
//
// 成员数恒为 **4**：曾按「客户端 resolveAgentRuntime / resolveRequestScene 从 baggage
// 读这两项」推断了 x-agent-runtime / x-agent-intent 两个成员并注入——真机报文里没有，
// 多出的成员让 baggage 的序列化结构与客户端不一致（baggage 是整体串，成员集合本身
// 就是可比对的形态）。
func (c *Client) injectBaggage(req *http.Request, uid string, meta ChatMeta) {
	conv := meta.ConversationID
	if conv == "" {
		conv = req.Header.Get("X-Conversation-ID")
	}
	reqID := meta.ConversationRequestID
	if reqID == "" {
		reqID = req.Header.Get("X-Conversation-Request-ID")
	}
	req.Header.Set("baggage", "buddyId=,buddyName=,codebuddy.session_id="+conv+
		",codebuddy.conversation_request_id="+reqID)
}

// injectClientSDKHeaders 注入 x-stainless-* 与 x-agent-* 两族出站头。
// 仅在 chat 路径调用——billing / checkin 等 REST 域没有真机对照证据，不臆造。
func (c *Client) injectClientSDKHeaders(req *http.Request, agentType string) {
	// X-Agent-Type 按场景取（ChatMeta.AgentType）：空 = 用户主对话 main；
	// auxiliary = 网关自发/辅助链。真机还会出 compaction/subagent/team，网关无对应
	// 路径，不臆造。旧实现恒发 main，等于让面板任务都自称用户主对话。
	if agentType == "" {
		agentType = agentTypeMain
	}
	req.Header.Set("X-Agent-Type", agentType)
	req.Header.Set("X-Agent-Intent", agentIntentCraft)
	req.Header.Set("X-Stainless-Lang", clientLang)
	req.Header.Set("X-Stainless-Package-Version", clientSDKVer)
	req.Header.Set("X-Stainless-OS", clientOS)
	req.Header.Set("X-Stainless-Arch", clientArch)
	req.Header.Set("X-Stainless-Runtime", clientRuntime)
	req.Header.Set("X-Stainless-Runtime-Version", clientNodeVer)
	req.Header.Set("X-Stainless-Retry-Count", "0")
}

// validTraceID 判断 B3 TraceId 是否合法：16 或 32 位 hex（大小写均可）。
// 入站透传值可能是任意形状（含横线/超长/非 hex），直接塞进 B3 头会破坏链路关联。
func validTraceID(s string) bool {
	if len(s) != 16 && len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// attributionClientName 生效的用量归属名：ClientName 非空取之；
// 空默认 "WorkBuddy"（伪造官方桌面端指纹；显式配 "SaaS" 可还原旧行为）。
func (c *Client) attributionClientName() string {
	if c != nil && c.ClientName != "" {
		return c.ClientName
	}
	return "WorkBuddy"
}

// injectAttribution 注入用量归属头（X-Agent-Purpose / X-IDE-* / X-Product）。
// 仅在 chat/completions 路径生效（ChatHeaders 调用）。
//
// X-Product 恒为 "SaaS"，与 X-IDE-* 分开取值——这是真机抓包纠正过的形态（issue #51）：
//
//	国际版出站：X-Product: SaaS  X-IDE-Name: WorkBuddy  X-IDE-Type: WorkBuddy  X-IDE-Version: 5.9.4
//
// 旧实现让 X-Product 也取客户端名（=WorkBuddy），于是同一个头官方发 SaaS、网关发
// WorkBuddy——「同名不同值」比多一个头更容易被规则命中。X-IDE-* 才是承载产品名的地方。
func (c *Client) injectAttribution(req *http.Request, a *auth.Auth) {
	name := c.attributionClientName()
	// X-Product 是 SKU 段，恒 SaaS：官方两版实测都不是产品名。
	req.Header.Set("X-Product", "SaaS")
	if name == "SaaS" {
		// 显式 ClientName=SaaS：仅声明 SKU，不补 X-IDE-*（旧行为，保留兼容）。
		return
	}
	req.Header.Set("X-Agent-Purpose", "conversation")
	req.Header.Set("X-IDE-Name", name)
	req.Header.Set("X-IDE-Type", name)
	req.Header.Set("X-IDE-Version", c.clientVersion(a))
}

// injectClientIP 在 PassthroughIP 开启时把 clientIP 参数透传给上游。
// 三个等价头（X-Forwarded-For/X-Real-IP/X-Client-IP）一并设，与桌面端透传一致。
// 按**参数传递**而非读共享字段：避免并发请求交叉污染对方 IP。
func (c *Client) injectClientIP(req *http.Request, clientIP string) {
	if c == nil || !c.PassthroughIP || clientIP == "" {
		return
	}
	req.Header.Set("X-Forwarded-For", clientIP)
	req.Header.Set("X-Real-IP", clientIP)
	req.Header.Set("X-Client-IP", clientIP)
}

// ExtractClientIP 从入站请求提取客户端 IP 首段（X-Forwarded-For 首段，回落 X-Real-IP）。
// 供 handler 在 PassthroughIP 开启时按请求取值后传入 ChatStream（chat 路径专属，不跨请求）。
// 取不到返回空串（handler 据此跳过透传）。
func ExtractClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			// 取逗号前首段并 trim 空白。
			if xff[i] == ',' {
				return strings.TrimSpace(xff[:i])
			}
		}
		return strings.TrimSpace(xff)
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		return real
	}
	return ""
}

// BillingHeaders billing 接口请求头。
//
// 头族按真机 billing 域对齐：UA 恒 axios（见 billingUA）、X-Request-ID 必带、
// **不发** X-CodeBuddy-Request 与 Accept-Language（旧实现两个都发，是多出来的形态）。
func (c *Client) BillingHeaders(req *http.Request, a *auth.Auth) {
	// AccessToken 加锁快照（同 ChatHeaders：keepalive 可在 a.mu 内改写）。
	req.Header.Set("Authorization", "Bearer "+a.AccessTokenValue())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	// X-Request-ID：真机 billing 族必带（checkin 23 条、resource-summary 等全部有）。
	// 值为纯随机 32 hex（无时间前缀）——与 chat 的 X-Request-ID 形态不同，故用 NewTraceID。
	req.Header.Set("X-Request-ID", session.NewTraceID())
	if c != nil && c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	} else if ua := c.billingUA(a); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if a.UID != "" {
		req.Header.Set("X-User-Id", a.UID)
	}
	if a.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", a.EnterpriseID)
		req.Header.Set("X-Tenant-Id", a.EnterpriseID)
	}
	// X-Domain：客户端恒发裸主机名。优先用 auth 文件声明的 domain，空则按 realm 取常量
	// ——缺头是可判别形态。
	if d := a.DomainValue(); d != "" {
		req.Header.Set("X-Domain", d)
	} else if a.IsGlobal() {
		req.Header.Set("X-Domain", xDomainGlobal)
	} else {
		req.Header.Set("X-Domain", xDomainCN)
	}
	// 设备风控头：billing 域（report/travel/balance/checkin）同样注入（见 resolveDeviceToken）。
	c.injectDeviceToken(req, a)
}

// RefreshHeaders refresh 端点专属头（X-Refresh-Token 只允许出现在这里）。
//
// 头族按真机 /v2/plugin/auth/token/refresh 逐头对齐：
//
//	Accept: application/json, text/plain, */*     Content-Type: application/json
//	X-Domain: <裸主机名>        X-Product: SaaS
//	X-Refresh-Token / X-Auth-Refresh-Source: plugin
//	X-Request-ID / User-Agent(RPC 族) / Authorization / X-User-Id
//
// 客户端这条路径**不发** X-CodeBuddy-Request 与 Accept-Language，也不发
// X-Requested-With（XRW 只属 chat 那条链），且 body 是 `{}`。
func (c *Client) RefreshHeaders(req *http.Request, a *auth.Auth) {
	c.CommonHeaders(req, a)
	// Accept 用客户端值（与 CommonHeaders 同值，显式重申以免将来 CommonHeaders 收窄）。
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Refresh-Token", a.RefreshToken)
	if a.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", a.EnterpriseID)
	}
	// X-Auth-Refresh-Source 对齐官方客户端 refresh 渠道标识 "plugin"。
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	// X-Product: SaaS —— 真机 refresh 实测必带。
	req.Header.Set("X-Product", "SaaS")
	// Authorization：真机 refresh **同时**带旧的 access token 与 refresh token
	// （两行并存）。注意与 chat 相反——chat 绝不带 X-Refresh-Token，refresh 则双发。
	if at := a.AccessTokenValue(); at != "" {
		req.Header.Set("Authorization", "Bearer "+at)
	} else {
		req.Header.Set("X-No-Authorization", "true")
	}
	// X-Request-ID / X-User-Id / X-Domain：真机三头齐全。
	req.Header.Set("X-Request-ID", session.NewTraceID())
	if a.UID != "" {
		req.Header.Set("X-User-Id", a.UID)
	}
	if d := a.DomainValue(); d != "" {
		req.Header.Set("X-Domain", d)
	} else if a.IsGlobal() {
		req.Header.Set("X-Domain", xDomainGlobal)
	} else {
		req.Header.Set("X-Domain", xDomainCN)
	}
}
