// Package codexreg 注册 Grok（x.ai）账号，产出可直接使用的 auth 数据（sso cookie）。
// 由 producer 批量调用。
//
// 注册本身不在 Go 里实现，而是 subprocess 调用 pyreg/ 下的 python 脚本：
//   - pyreg/grok_register.py : nodriver 无头跑完 accounts.x.ai 注册流程，提取 sso cookie
//
// 验证码由调用方通过 FetchCode 回调提供（producer 用 varymail / mailfetch 取），
// python 需要时打印 __NEED_CODE__，Go 把码写回它的 stdin。
package codexreg

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrAccountTaken 注册时提示"账号不存在或已被删除/停用"，视为该地址已被注册，不应重试。
var ErrAccountTaken = errors.New("账号不存在或已被删除/停用")

// Input 单个账号的生产参数。
type Input struct {
	Email    string
	Password string // 注册流程要求创建密码时使用（为空则自动生成）
	FullName string
	Age      string
	Proxy    string // 空=直连
	Headless bool

	// FetchCode 拉取 x.ai 发到邮箱的验证码。由 producer 用 varymail/mailfetch 实现。
	FetchCode func(ctx context.Context) (string, error)

	// Log 输出进度（可为 nil）。
	Log func(format string, a ...any)

	// SaveShot 保存注册失败时的页面截图(PNG)，用于事后排查（可为 nil）。
	// python 无头注册不回传截图，保留字段兼容调用方。
	SaveShot func(png []byte)
}

// Result 生产结果。
type Result struct {
	SSO       string         `json:"-"`
	AuthJSON  map[string]any `json:"auth_json"`  // 完整 auth 数据
	AccountID string         `json:"account_id"` // 不解码 JWT，保留字段兼容调用方
	UserID    string         `json:"user_id"`
	PlanType  string         `json:"plan_type"`
}

func (in Input) logf(format string, a ...any) {
	if in.Log != nil {
		in.Log(format, a...)
	}
}

// Register 完整生产一个账号：python 注册 grok → sso → 组装 auth 数据。
func Register(ctx context.Context, in Input) (*Result, error) {
	if in.FetchCode == nil {
		return nil, fmt.Errorf("缺少 FetchCode 回调，无法自动读取验证码")
	}
	if in.FullName == "" {
		in.FullName = genName()
	}
	if in.Age == "" {
		in.Age = genAge()
	}
	if in.Password == "" {
		in.Password = GenPassword(16)
	}

	given, family := splitName(in.FullName)
	res, err := registerViaPython(ctx, in, given, family)
	if err != nil {
		return nil, fmt.Errorf("Grok 注册失败: %w", err)
	}

	return &Result{SSO: res.SSO, AuthJSON: buildAuth(in, res)}, nil
}

// splitName 把随机全名拆成名/姓，缺姓时用固定占位。
func splitName(full string) (given, family string) {
	parts := strings.Fields(full)
	switch len(parts) {
	case 0:
		return genName(), "User"
	case 1:
		return parts[0], "User"
	default:
		return parts[0], strings.Join(parts[1:], " ")
	}
}
