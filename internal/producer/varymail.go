package producer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"grok-register/internal/codexreg"
	"grok-register/internal/models"
	"grok-register/internal/varymail"
)

// runVarymail 用 vary.email 取件作为邮箱来源生产账号：
// 逐个购买邮箱（无母号/裂变概念），并发注册；库存用尽或余额不足即停止。
func (p *Producer) runVarymail(ctx context.Context, target int, cfg Config) {
	if strings.TrimSpace(cfg.VarymailKey) == "" {
		p.setMessage("未配置 varymail API Key，无法生产")
		p.logf("✗ varymail 未配置：请在设置里填写 API Key")
		return
	}
	cli := varymail.New("", cfg.VarymailKey)

	// 固定使用 xAI 服务，起始库存检查：给出友好提示，库存不足直接不跑。
	svc, _, err := cli.ServiceByName(ctx, varymail.DefaultServiceName)
	if err != nil {
		p.setMessage("varymail 连接失败：" + err.Error())
		p.logf("✗ varymail 查询服务失败：%v", err)
		return
	}
	p.logf("开始生产（varymail），目标 %d，服务=%s 库存=%s 可用=%d 并发 %d",
		target, svc.Name, svc.Stock, svc.Available, cfg.MaxConcurrency)
	if svc.Stock == "out" || svc.Available <= 0 {
		p.setMessage(fmt.Sprintf("varymail 服务「%s」库存不足，无法生产", svc.Name))
		p.logf("✗ varymail 库存不足（%s），本次不生产", svc.Name)
		return
	}

	sem := make(chan struct{}, cfg.MaxConcurrency)
	var wg sync.WaitGroup
	var haltMsg string // 因库存/余额等提前终止时的收尾提示

	for {
		if ctx.Err() != nil {
			p.logf("已手动停止")
			break
		}
		done := p.producedThisRun()
		running := p.inflightCount()
		if done+running >= target {
			if running == 0 {
				break
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// 先复用邮箱管理里已购、还没注册成功的 vary 邮箱，池里没有才下单买新的。
		mb, err := p.claimVarymailBox(ctx, cli, svc.ID)
		if err != nil {
			switch {
			case errors.Is(err, varymail.ErrOutOfStock):
				p.logf("⚠ varymail 库存已用尽，停止领取新任务")
				haltMsg = "varymail 库存不足，已停止"
			case errors.Is(err, varymail.ErrNoBalance):
				p.logf("✗ varymail 余额不足，停止生产")
				haltMsg = "varymail 余额不足，请充值"
			case errors.Is(err, varymail.ErrUnauthorized):
				p.logf("✗ varymail API Key 无效，停止生产")
				haltMsg = "varymail API Key 无效"
			default:
				p.logf("✗ varymail 下单失败：%v", err)
				haltMsg = "varymail 下单失败：" + err.Error()
			}
			// 无论哪种错误都不再开新任务，等在跑的收尾。
			if p.inflightCount() == 0 {
				break
			}
			time.Sleep(800 * time.Millisecond)
			continue
		}

		email := mb.Email
		if email == "" {
			p.logf("✗ varymail 下单未返回邮箱，跳过")
			continue
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(mb models.Mailbox, email string) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				p.releaseInflight(email)
				p.updateProgress()
			}()
			defer func() {
				if r := recover(); r != nil {
					p.markFailed(email)
					msg := fmt.Sprintf("注册异常(panic): %v", r)
					p.setRegistrationFailed(email, msg, "")
					p.logf("✗ %s %s\n%s", mask(email), msg, debug.Stack())
					p.updateProgress()
				}
			}()
			p.updateProgress()

			if err := p.produceOneVarymail(ctx, cfg, cli, mb); err != nil {
				if errors.Is(err, codexreg.ErrAccountTaken) {
					p.logf("⚠ %s 停用（%v），换下一个", mask(email), err)
				} else {
					p.markFailed(email)
					p.logf("✗ %s 注册失败：%v", mask(email), err)
				}
			} else {
				p.markSuccess(email)
				p.incRegistered()
				p.logf("✓ %s 注册成功", mask(email))
			}
			p.updateProgress()
		}(mb, email)
	}

	wg.Wait()
	produced := p.producedThisRun()
	switch {
	case ctx.Err() != nil:
		p.setMessage(fmt.Sprintf("已停止，本次成功 %d 个", produced))
	case haltMsg != "":
		p.setMessage(fmt.Sprintf("%s（本次成功 %d 个）", haltMsg, produced))
	default:
		p.setMessage(fmt.Sprintf("已完成，本次成功 %d 个", produced))
	}
}

// claimVarymailBox 领取一个 vary 邮箱：先复用邮箱管理里已购买、尚未注册成功且未失效的，
// 池里没有可用的才向 vary.email 下单（下单即扣费），买到的邮箱也写进邮箱管理。
func (p *Producer) claimVarymailBox(
	ctx context.Context,
	cli *varymail.Client,
	serviceID int,
) (models.Mailbox, error) {
	p.claimMu.Lock()
	defer p.claimMu.Unlock()

	var pool []models.Mailbox
	p.db.Where("source = ? AND vary_purchase_id > 0 AND status = ?",
		models.MailboxSourceVarymail, "verified").Order("id asc").Find(&pool)
	for _, mb := range pool {
		if p.mailboxBusy(mb.ID) || p.isRegistered(mb.Email) {
			continue
		}
		p.markInflight(mb.Email, mb.ID)
		p.logf("♻ 复用已购 varymail 邮箱 %s（取件权 #%d）", mask(mb.Email), mb.VaryPurchaseID)
		return mb, nil
	}

	pur, bal, err := cli.Buy(ctx, serviceID)
	if err != nil {
		return models.Mailbox{}, err
	}
	email := strings.TrimSpace(pur.Email)
	if email == "" {
		return models.Mailbox{}, nil
	}
	mb := models.Mailbox{
		Email:          email,
		Provider:       "varymail",
		Source:         models.MailboxSourceVarymail,
		VaryPurchaseID: pur.ID,
		Status:         "verified",
		Note:           "vary.email 购买",
	}
	var exist models.Mailbox
	if err := p.db.Where("email = ?", email).First(&exist).Error; err == nil {
		exist.Provider, exist.Source, exist.Status = mb.Provider, mb.Source, mb.Status
		exist.VaryPurchaseID, exist.Note = mb.VaryPurchaseID, mb.Note
		p.db.Save(&exist)
		mb = exist
	} else if err := p.db.Create(&mb).Error; err != nil {
		return models.Mailbox{}, err
	}
	p.markInflight(mb.Email, mb.ID)
	p.logf("🛒 varymail 购买邮箱 %s（取件权 #%d，余额 %.2f）", mask(email), pur.ID, bal)
	return mb, nil
}

// markVarymailBoxInvalid 取件权取不到码时把邮箱标为失效，避免下次继续复用它。
func (p *Producer) markVarymailBoxInvalid(mb models.Mailbox, reason string) {
	if mb.ID == 0 {
		return
	}
	p.db.Model(&models.Mailbox{}).Where("id = ?", mb.ID).
		Updates(map[string]any{"status": "verify_failed", "note": reason})
	p.logf("⚠ varymail 邮箱 %s 取件失败，已标记失效不再复用", mask(mb.Email))
}

// produceOneVarymail 用 varymail 分配的邮箱注册一个 Grok 账号。
func (p *Producer) produceOneVarymail(
	ctx context.Context,
	cfg Config,
	cli *varymail.Client,
	mb models.Mailbox,
) error {
	email := mb.Email
	purchaseID := mb.VaryPurchaseID
	// 复用邮箱时沿用原密码，账号已建成时才能凭同一密码登录。
	password := p.existingPassword(email)
	if password == "" {
		password = codexreg.GenPassword(16)
	}
	p.upsert(models.Registration{
		Email: email, MailboxID: mb.ID, Password: password,
		Status: "registering", IsMother: false, Note: "varymail",
	})

	var logMu sync.Mutex
	var logBuf strings.Builder
	appendLog := func(line string) {
		logMu.Lock()
		logBuf.WriteString(time.Now().Format("2006-01-02 15:04:05") + " " + line + "\n")
		snapshot := logBuf.String()
		logMu.Unlock()
		p.db.Model(&models.Registration{}).Where("email = ?", email).Update("log", snapshot)
	}

	// 提交邮箱前先记下当前最后一个验证码，之后只接受与它不同的新码。
	baseline := latestCodeVarymail(ctx, cli, purchaseID)
	codeTimeout := false
	in := codexreg.Input{
		Email:    email,
		Password: password,
		Proxy:    p.nextProxy(cfg),
		Headless: cfg.Headless,
		Log: func(f string, a ...any) {
			msg := fmt.Sprintf(f, a...)
			appendLog(msg)
			p.logf("%s", "  "+mask(email)+" "+msg)
		},
		FetchCode: func(ctx context.Context) (string, error) {
			code, err := p.fetchCodeVarymail(ctx, cli, purchaseID, baseline)
			if errors.Is(err, errCodeTimeout) {
				codeTimeout = true
			}
			return code, err
		},
		SaveShot: func(png []byte) {
			p.db.Model(&models.Registration{}).Where("email = ?", email).Update("shot", png)
		},
	}

	res, err := codexreg.Register(ctx, in)
	if err != nil {
		if errors.Is(err, codexreg.ErrAccountTaken) {
			appendLog("⚠ 停用（账号不存在或已被删除/停用）")
			p.setRegistrationStatus(email, "already_registered", "停用："+err.Error(), logBuf.String())
			return err
		}
		appendLog("✗ 失败: " + err.Error())
		p.setRegistrationFailed(email, err.Error(), logBuf.String())
		if codeTimeout {
			p.markVarymailBoxInvalid(mb, "vary 取件超时未收到验证码")
		}
		return err
	}

	appendLog("✓ 注册成功")
	authBytes, _ := json.MarshalIndent(res.AuthJSON, "", "  ")
	p.upsert(models.Registration{
		Email: email, MailboxID: mb.ID, Password: password,
		Status: "registered", IsMother: false, Note: "varymail",
		AuthData: string(authBytes), AccountID: res.AccountID,
		UserID: res.UserID, PlanType: res.PlanType, Log: logBuf.String(),
	})
	return nil
}

// errCodeTimeout 取件超时：邮箱可能已失效，不再复用。
var errCodeTimeout = errors.New("超时未收到验证码")

// existingPassword 读取该邮箱已有注册记录里的密码（复用邮箱时沿用，便于登录已建成的账号）。
func (p *Producer) existingPassword(email string) string {
	var reg models.Registration
	if err := p.db.Select("password").Where("email = ?", email).First(&reg).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(reg.Password)
}

// fetchCodeVarymail 每 codePollInterval 轮询一次取件接口，
// 直到拿到与 baseline 不同的新验证码或超时。
func (p *Producer) fetchCodeVarymail(ctx context.Context, cli *varymail.Client, purchaseID int, baseline string) (string, error) {
	deadline := time.Now().Add(codePollTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg, hasMail, err := cli.Code(ctx, purchaseID)
		switch {
		case errors.Is(err, varymail.ErrPickup):
			// 取件暂时失败，稍后重试
		case err != nil:
			return "", err
		case hasMail:
			// x.ai 的码形如 C1O-6KS，页面只接受去掉连字符的 6 位。
			if code := normalizeCode(msg.Code); code != "" && code != baseline {
				return code, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(codePollInterval):
		}
	}
	return "", errCodeTimeout
}

// latestCodeVarymail 取当前已有的最后一个验证码（新邮箱通常为空），作为旧码基线。
func latestCodeVarymail(ctx context.Context, cli *varymail.Client, purchaseID int) string {
	msg, hasMail, err := cli.Code(ctx, purchaseID)
	if err != nil || !hasMail {
		return ""
	}
	return normalizeCode(msg.Code)
}
