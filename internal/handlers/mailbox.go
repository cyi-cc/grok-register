package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"grok-register/internal/mailfetch"
	"grok-register/internal/models"
	"grok-register/internal/varymail"

	"github.com/gin-gonic/gin"
)

type mailboxInput struct {
	Email        string `json:"email" binding:"required"`
	Password     string `json:"password"`
	Provider     string `json:"provider"`
	ClientID     string `json:"client_id"`
	RefreshToken string `json:"refresh_token"`
	Status       string `json:"status"`
	Note         string `json:"note"`
}

var mailboxStatuses = map[string]bool{
	"unverified":    true,
	"verifying":     true,
	"verify_failed": true,
	"verified":      true,
}

func validMailboxStatus(s string) bool {
	return s == "" || mailboxStatuses[s]
}

func (h *Handler) MailboxList(c *gin.Context) {
	var items []models.Mailbox
	q := h.DB.Order("id desc")
	if s := c.Query("status"); s != "" {
		q = q.Where("status = ?", s)
	}
	if src := c.Query("source"); src != "" {
		if src == models.MailboxSourceLocal {
			// 加字段前的老数据没有来源，按本地邮箱算。
			q = q.Where("source = ? OR source IS NULL OR source = ''", src)
		} else {
			q = q.Where("source = ?", src)
		}
	}
	if kw := c.Query("q"); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("email LIKE ? OR provider LIKE ? OR note LIKE ?", like, like, like)
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	q.Model(&models.Mailbox{}).Count(&total)
	if err := q.Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	registerLimit := 1
	for i := range items {
		items[i].RegisterCount = h.mailboxRegisterCount(items[i])
		items[i].RegisterLimit = registerLimit
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "total": total, "page": page, "size": size})
}

func (h *Handler) mailboxRegisterCount(m models.Mailbox) int {
	var n int64
	// 归属该邮箱的记录：本身地址或 mailbox_id
	match := h.DB.Where("mailbox_id = ? OR email = ?", m.ID, m.Email)
	// 只统计已占用名额的记录（成功注册 / 停用），pending / 失败可重试不计入
	h.DB.Model(&models.Registration{}).
		Where(match).
		Where("status IN ?", []string{"registered", "already_registered"}).
		Count(&n)
	return int(n)
}

func (h *Handler) MailboxCreate(c *gin.Context) {
	var in mailboxInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validMailboxStatus(in.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
		return
	}
	m := models.Mailbox{Email: in.Email, Password: in.Password, Provider: in.Provider, ClientID: in.ClientID, RefreshToken: in.RefreshToken, Status: in.Status, Note: in.Note, Source: models.MailboxSourceLocal}
	if m.Status == "" {
		m.Status = "unverified"
	}
	if err := h.DB.Create(&m).Error; err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, m)
}

type mailboxImportItem struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	ClientID     string `json:"client_id"`
	RefreshToken string `json:"refresh_token"`
}

// MailboxImport 批量导入邮箱，重复 email 自动跳过。
func (h *Handler) MailboxImport(c *gin.Context) {
	var in struct {
		Items []mailboxImportItem `json:"items" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	added, skipped := 0, 0
	seen := map[string]bool{}
	for _, it := range in.Items {
		email := strings.TrimSpace(it.Email)
		if email == "" || !strings.Contains(email, "@") || seen[email] {
			skipped++
			continue
		}
		seen[email] = true
		var count int64
		h.DB.Model(&models.Mailbox{}).Where("email = ?", email).Count(&count)
		if count > 0 {
			skipped++
			continue
		}
		m := models.Mailbox{
			Email:        email,
			Password:     strings.TrimSpace(it.Password),
			ClientID:     strings.TrimSpace(it.ClientID),
			RefreshToken: strings.TrimSpace(it.RefreshToken),
			Status:       "verifying",
			Source:       models.MailboxSourceLocal,
		}
		if err := h.DB.Create(&m).Error; err != nil {
			skipped++
			continue
		}
		added++
	}
	c.JSON(http.StatusOK, gin.H{"added": added, "skipped": skipped})
}

// MailboxVerify 校验单个邮箱凭据是否可用，更新状态为 verified / verify_failed。
func (h *Handler) MailboxVerify(c *gin.Context) {
	var m models.Mailbox
	if err := h.DB.First(&m, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "邮箱不存在"})
		return
	}
	err := h.Mail.Verify(c.Request.Context(), mailfetch.Account{
		Email:        m.Email,
		ClientID:     m.ClientID,
		RefreshToken: m.RefreshToken,
	})
	if err != nil {
		m.Status = "verify_failed"
	} else {
		m.Status = "verified"
	}
	h.DB.Model(&m).Update("status", m.Status)
	c.JSON(http.StatusOK, gin.H{"id": m.ID, "status": m.Status})
}

func (h *Handler) MailboxUpdate(c *gin.Context) {
	var m models.Mailbox
	if err := h.DB.First(&m, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var in mailboxInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validMailboxStatus(in.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
		return
	}
	m.Email = in.Email
	m.Password = in.Password
	m.Provider = in.Provider
	m.ClientID = in.ClientID
	m.RefreshToken = in.RefreshToken
	if in.Status != "" {
		m.Status = in.Status
	}
	m.Note = in.Note
	if err := h.DB.Save(&m).Error; err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *Handler) MailboxDelete(c *gin.Context) {
	if err := h.DB.Delete(&models.Mailbox{}, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// MailboxMessages 取件：拉某个邮箱收件箱最新邮件，含完整 HTML 正文，供网页弹窗轮询展示。
func (h *Handler) MailboxMessages(c *gin.Context) {
	var m models.Mailbox
	if err := h.DB.First(&m, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "邮箱不存在"})
		return
	}
	if m.Source == models.MailboxSourceVarymail {
		msg, err := h.varymailCode(c, m)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "email": m.Email})
			return
		}
		items := []mailfetch.Message{}
		if msg.ID != "" {
			items = append(items, msg)
		}
		c.JSON(http.StatusOK, gin.H{"email": m.Email, "items": items})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	msgs, err := h.Mail.ListMessages(c.Request.Context(), mailfetch.Account{
		Email:        m.Email,
		ClientID:     m.ClientID,
		RefreshToken: m.RefreshToken,
	}, limit)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, mailfetch.ErrMissingCreds) || errors.Is(err, mailfetch.ErrAuthFailed) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error(), "email": m.Email})
		return
	}
	c.JSON(http.StatusOK, gin.H{"email": m.Email, "items": msgs})
}

// MailboxMessage 按消息 ID 拉取单封邮件的完整正文，供点击后按需加载。
func (h *Handler) MailboxMessage(c *gin.Context) {
	var m models.Mailbox
	if err := h.DB.First(&m, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "邮箱不存在"})
		return
	}
	if m.Source == models.MailboxSourceVarymail {
		msg, err := h.varymailCode(c, m)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "email": m.Email})
			return
		}
		c.JSON(http.StatusOK, msg)
		return
	}
	msg, err := h.Mail.GetMessage(c.Request.Context(), mailfetch.Account{
		Email:        m.Email,
		ClientID:     m.ClientID,
		RefreshToken: m.RefreshToken,
	}, c.Query("mid"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, mailfetch.ErrMissingCreds) || errors.Is(err, mailfetch.ErrAuthFailed) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error(), "email": m.Email})
		return
	}
	c.JSON(http.StatusOK, msg)
}

// varymailCode vary 邮箱没有 IMAP 凭据，取件走 vary.email 取件权接口，
// 把最新一封来信（只含验证码）包装成与 Outlook 取件一致的消息结构。
func (h *Handler) varymailCode(c *gin.Context, m models.Mailbox) (mailfetch.Message, error) {
	key := h.setting("varymail_api_key")
	if key == "" {
		return mailfetch.Message{}, errors.New("请先在设置里填写 varymail API Key")
	}
	if m.VaryPurchaseID == 0 {
		return mailfetch.Message{}, errors.New("该 vary 邮箱缺少取件权，无法取件")
	}
	msg, hasMail, err := varymail.New("", key).Code(c.Request.Context(), m.VaryPurchaseID)
	if err != nil {
		return mailfetch.Message{}, err
	}
	if !hasMail {
		return mailfetch.Message{}, nil
	}
	id := msg.ID
	if id == "" {
		id = msg.Code
	}
	return mailfetch.Message{
		ID:         id,
		From:       msg.From,
		FromName:   "vary.email",
		Subject:    "验证码 " + msg.Code,
		ReceivedAt: parseVarymailTime(msg.ReceivedAt),
		Text:       msg.Code,
	}, nil
}

// parseVarymailTime 兼容 vary.email 返回的几种时间格式，解析失败按当前时间。
func parseVarymailTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Now()
}
