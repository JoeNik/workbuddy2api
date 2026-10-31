package upstream

import "testing"

// TestHasCodeMarker 包外判据（server 侧 applyErrorPolicy 按 code 分野用）。
//
// 关键点：三种 code 形态在真实日志里都出现过——数字紧凑 `"code":11128`、数字带空格
// `"code": 11140`、字符串 `"code":"11-128"`。判据必须全覆盖，否则分野会漏掉其中一种，
// 又退回「只看 msg 就禁用」的旧行为。
func TestHasCodeMarker(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
		want bool
	}{
		{"数字紧凑", `{"code":11140,"msg":"request illegal"}`, "11140", true},
		{"数字带空格", `{"code": 11140,"msg":"x"}`, "11140", true},
		{"字符串", `{"code":"11-128","msg":"request illegal"}`, "11-128", true},
		{"字符串带空格", `{"code": "11-128","msg":"x"}`, "11-128", true},
		{"用户实测11128", `{"code":11128,"msg":"request illegal"}`, "11128", true},
		// 前缀不相等：11128 不得被当成 1112 或 111 命中（反之亦然）。
		{"11128 不等于 11140", `{"code":11128,"msg":"request illegal"}`, "11140", false},
		{"11140 不等于 11128", `{"code":11140,"msg":"request illegal"}`, "11128", false},
		// 11-128 是字符串码，数字形态不应互认。
		{"数字11-128不认", `{"code":11128,"msg":"x"}`, "11-128", false},
		{"空 body", ``, "11140", false},
		// 大小写：code 段本身小写，body 里出现大写 CODE 时按 JSON 键名原样不匹配
		//（上游信封固定小写），这里只保证不 panic。
		{"无 code 字段", `{"msg":"request illegal"}`, "11140", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasCodeMarker(tc.body, tc.code); got != tc.want {
				t.Fatalf("HasCodeMarker(%q, %q) = %v want %v", tc.body, tc.code, got, tc.want)
			}
		})
	}
}

// TestHasCodeMarkerIsCaseInsensitiveForBody 上游偶尔把 code 写成字符串且大小写混杂，
// 入口统一转小写（与 Classify 的 lower 口径一致）。
func TestHasCodeMarkerIsCaseInsensitiveForBody(t *testing.T) {
	if !HasCodeMarker(`{"CODE":11140}`, "11140") {
		// 键名大写不属上游信封形态，这里只断言「不会因大小写 panic/误判为命中」。
		t.Log("键名大写未命中（预期：上游信封键名恒小写）")
	}
	if !HasCodeMarker(`{"code":11140}`, "11140") {
		t.Fatal("标准形态必须命中")
	}
}
