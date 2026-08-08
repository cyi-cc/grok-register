package codexreg

// buildAuth 用 python 拿到的 sso cookie 组装 auth 数据。
// 顶层保留 sso，供下载/导出接口直接读取。
func buildAuth(in Input, r *pythonResult) map[string]any {
	return map[string]any{
		"auth_mode":     "sso",
		"platform":      "grok",
		"auth_provider": "xai",
		"email":         in.Email,
		"sso":           r.SSO,
	}
}
