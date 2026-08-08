package models

import "time"

// 邮箱来源。
const (
	MailboxSourceLocal    = "local"    // 手工导入的本地邮箱（Outlook 凭据取件）
	MailboxSourceVarymail = "varymail" // vary.email 按次购买的邮箱（凭取件权取件）
)

// Mailbox 邮箱管理
//
// Status 流转: unverified(待验证) / verifying(验证中) / verify_failed(验证失败) / verified(已验证)
//
// Source 区分来源：local 本地导入邮箱，varymail 为 vary.email 买来的邮箱
// （VaryPurchaseID 为取件权，注册失败后仍可复用，直到取件权失效）。
type Mailbox struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	Email          string `gorm:"size:255;not null;uniqueIndex" json:"email"`
	Password       string `gorm:"size:255" json:"password"`
	Provider       string `gorm:"size:64" json:"provider"` // gmail / outlook / 临时邮箱...
	Source         string `gorm:"size:16;default:local;index" json:"source"`
	VaryPurchaseID int    `gorm:"index" json:"vary_purchase_id"`
	ClientID      string    `gorm:"size:255" json:"client_id"`
	RefreshToken  string    `gorm:"type:text" json:"refresh_token"`
	Status        string    `gorm:"size:32;default:unverified" json:"status"`
	Note          string    `gorm:"type:text" json:"note"`
	RegisterCount int       `gorm:"-" json:"register_count"`
	RegisterLimit int       `gorm:"-" json:"register_limit"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Setting 系统设置 (key-value)
type Setting struct {
	Key       string    `gorm:"primaryKey;size:128" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Admin 后台管理员账户。
//
// Token 存当前唯一有效的 JWT：签发即写库、换新即作废旧的（旧 token 立即失效）。
// TokenIssuedAt 记录本 token 的签发时间，用于“超过 2 小时自动续期”。
type Admin struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Username      string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash  string    `gorm:"size:255;not null" json:"-"`
	Token         string    `gorm:"type:text" json:"-"`
	TokenIssuedAt time.Time `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
