package db

import (
	"grok-register/internal/emailalias"
	"grok-register/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func Init(path string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&models.Registration{}, &models.Mailbox{}, &models.Setting{}, &models.Admin{}); err != nil {
		return nil, err
	}
	normalizeLegacyStatuses(db)
	reclaimOrphanRegistering(db)
	backfillRegistrationMailboxIDs(db)
	backfillMailboxSource(db)
	return db, nil
}

// reclaimOrphanRegistering 启动时把残留的 registering 记录标为 register_failed。
// 生产任务状态只在内存里，程序重启后这些"注册中"记录不会再有人推进，
// 置为失败后可在下次生产时被重新领取（母号+裂变补齐规则）。
func reclaimOrphanRegistering(db *gorm.DB) {
	db.Model(&models.Registration{}).Where("status = ?", "registering").
		Updates(map[string]any{"status": "register_failed", "note": "程序重启中断，可重新生产"})
}

// normalizeLegacyStatuses 把旧的 AdSkull 验证态注册记录迁移到新的生产态。
// Mailbox 的 unverified/verified 表示邮箱凭据是否校验通过，语义不变，保持原样。
func normalizeLegacyStatuses(db *gorm.DB) {
	regStatusMap := map[string]string{
		"unverified":    "pending",
		"verifying":     "registering",
		"verify_failed": "register_failed",
		"verified":      "registered",
	}
	for oldStatus, newStatus := range regStatusMap {
		db.Model(&models.Registration{}).Where("status = ?", oldStatus).Update("status", newStatus)
	}
}

// backfillMailboxSource 给加字段之前的老邮箱补来源：历史数据都是手工导入的本地邮箱。
func backfillMailboxSource(db *gorm.DB) {
	db.Model(&models.Mailbox{}).Where("source IS NULL OR source = ''").
		Update("source", models.MailboxSourceLocal)
}

func backfillRegistrationMailboxIDs(db *gorm.DB) {
	var regs []models.Registration
	if err := db.Where("mailbox_id IS NULL OR mailbox_id = 0").Find(&regs).Error; err != nil {
		return
	}
	for _, reg := range regs {
		baseEmail := emailalias.Base(reg.Email)
		var mb models.Mailbox
		if err := db.Where("email = ?", baseEmail).First(&mb).Error; err == nil {
			db.Model(&models.Registration{}).Where("id = ?", reg.ID).Update("mailbox_id", mb.ID)
		}
	}
}
