// cache_key.go 注入上游 prompt_cache_key 字段（P0 费用优化）。
//
// 逆向实测（buddy-adapter.ts:706-714）：上游服务端支持 prompt_cache_key，
// 同一段 8k token 前缀：
//   - 不带 → prompt_cache_hit_tokens=0, credit≈0.34
//   - 带   → prompt_cache_hit_tokens=7808, credit≈0.02（费用降 ~17×）
//
// 网关在此为每个出站 chat 请求注入一个稳定、按账号隔离的 cache key，
// 让同一客户端对同一账号的连续请求复用上游前缀缓存。
package upstream

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
)

// InjectPromptCacheKey 在已改写的出站 body 上注入 prompt_cache_key 字段。
//
// 优先级：
//  1. body 已带 prompt_cache_key → 原值保留（客户端自知复用哪个键）
//  2. body 已带 conversation_id / conversationId → 用它做会话哈希源
//  3. 都没有 → 用入站 conversationID 参数（来自 X-Conversation-ID 头解析）
//
// 安全约束——按账号隔离：
//   - 生成键格式 `wb2a-<uid8>-<convHex>`
//   - uid8 是账号 UID 前 8 字符，跨账号绝不相同 → 跨账号缓存键绝不碰撞
//   - 跨账号复用同一 cache key 会让上游命中错账号的前缀缓存、泄露对方对话，故 uid 是硬隔离因子
//
// 入参 uid 为账号 UID（空则用 "-"，但仍会注入键；调用方应保证传真实 UID）。
// 入参 conversationID 为网关解析出的会话标识（body 里没有 conversation_id 时用它做哈希源）。
// 两源都空时 convHex 为定值（每次新会话不复用前缀，但仍保留账号隔离段）。
// body 不可解析时原样返回（与 prepareBody 语义一致：坏 body 不二次错误化）。
func InjectPromptCacheKey(body []byte, uid, conversationID string) []byte {
	if len(body) == 0 {
		return body
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	// 优先级 1：客户端已显式带 key → 绝不覆盖。
	if existing, ok := obj["prompt_cache_key"].(string); ok && existing != "" {
		return body
	}
	// 优先级 2：body 里的 conversation_id / conversationId 做会话哈希源。
	conv := conversationID
	if v := strField(obj, "conversation_id"); v != "" {
		conv = v
	} else if v := strField(obj, "conversationId"); v != "" {
		conv = v
	}
	key := buildCacheKey(uid, conv)
	obj["prompt_cache_key"] = key
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// buildCacheKey 生成与官方客户端同形的会话标识：UUID v4 形态的 36 字符。
//
// 为什么不再带前缀（issue #51，真机取证）：官方出站 X-Conversation-ID 实测恒为
// `xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx`（36 字符带横线 UUID）。旧实现
// `wb2a-<uid8>-<convHex>` 长 48、自带项目缩写、并把账号 UID 前 8 位明文写进请求头与
// body——三处都可与官方值区分，等于自证网关身份（上游风控一条正则就能筛出来）。
//
// 语义不变：
//   - uid 仍参与哈希输入 → 跨账号绝不碰撞（隔离靠哈希输入，不再靠明文 uid 段）；
//   - 同 (uid, conversation) 恒得同值 → 会话级前缀缓存槽语义与旧实现一致；
//   - 空会话源时参数固化，仍不复用其他会话的槽。
func buildCacheKey(uid, conversation string) string {
	return buildStableUUID(uid, conversation, "")
}

// buildStableUUID 由 (uid, conversation, purpose) 稳定派生一个 UUID v4 形态的 36 字符值。
func buildStableUUID(uid, conversation, purpose string) string {
	in := uid + "|" + conversation
	if purpose != "" {
		in += "|" + purpose
	}
	sum := sha256.Sum256([]byte(in))
	var u [16]byte
	copy(u[:], sum[:16])
	u[6] = (u[6] & 0x0f) | 0x40 // version 4
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 4122 variant
	return formatUUID(u)
}

// buildTimeOrderedUUID 由 (uid, conversation, purpose) 稳定派生一个**时间有序 UUID（v7）**。
//
// 为什么不能用 buildCacheKey（v4）摆到会话头族上：5.9.3 真机 X-Conversation-ID /
// acp-connection-id 恒为 `01a1815d-dacf-75f1-…` 这种 v7 形态（前 48 bit 是时刻、
// 第 13 位恒为 7），v4 没有时间位、跨请求不可排序——位布局与客户端不同本身就是指纹。
//
// 时间位取哈希派生值而非 time.Now()：这两个头要求「同会话多轮稳定」，用当前时刻会让
// 同一会话每轮换值，反而比官方更像伪造。派生区间锚在 2026-01-01 起的约 24 个月内，
// 落在真实客户端可能出现的时段里，且同 (uid, 会话, 用途) 恒同值。
func buildTimeOrderedUUID(uid, conversation, purpose string) string {
	in := uid + "|" + conversation
	if purpose != "" {
		in += "|" + purpose
	}
	sum := sha256.Sum256([]byte(in))
	const (
		anchorMS = int64(1767225600000)                 // 2026-01-01T00:00:00Z
		windowMS = int64(24 * 30 * 24 * 60 * 60 * 1000) // ≈24 个月
	)
	ms := uint64(anchorMS + int64(binary.BigEndian.Uint64(sum[8:16])%uint64(windowMS)))
	var u [16]byte
	u[0] = byte(ms >> 40)
	u[1] = byte(ms >> 32)
	u[2] = byte(ms >> 24)
	u[3] = byte(ms >> 16)
	u[4] = byte(ms >> 8)
	u[5] = byte(ms)
	copy(u[6:], sum[:10])
	u[6] = (u[6] & 0x0f) | 0x70 // version 7
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 4122 variant
	return formatUUID(u)
}

// formatUUID 把 16 字节按 8-4-4-4-12 拼成带横线的 36 字符形态。
func formatUUID(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// cacheKeySource 返回会话头族可用的「会话源」：客户端声明的 conversationId 优先，
// 否则用网关派生的粘性会话键（CacheKeyID）。
//
// 两者语义不同但都可作为「同一会话」的稳定标识：前者是客户端自己认的会话，
// 后者是网关认的同一会话（Claude Code 这类不带 conversation_id 的客户端只有后者）。
// 都为空 → 返回 ""，调用方现造一个客户端同形态的 v7 UUID（**不留空**：上游按三件套
// 是否齐全做渠道校验，缺任一头即 400 code=11-128「conversation id missing」）。
func cacheKeySource(meta ChatMeta) string {
	if meta.ConversationID != "" {
		return meta.ConversationID
	}
	return meta.CacheKeyID
}

// strField 从 map 取 string 字段，非 string 或空串返回 ""。
func strField(obj map[string]any, key string) string {
	v, ok := obj[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}
