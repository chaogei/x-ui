package controller

import (
	"github.com/gin-gonic/gin"

	"x-ui/web/service"
	"x-ui/web/session"
)

// TwoFactorController 是两步验证的管理接口。
//
// 全部挂在 /xui 组下，因此天然要求已登录会话 + CSRF token。
// 这一点不是可选的：任何一个能被跨站触发的 /2fa/disable 都等于没有 2FA。
type TwoFactorController struct {
	twoFactorService service.TwoFactorService
}

type twoFactorCodeForm struct {
	Code string `json:"code" form:"code"`
}

type twoFactorDisableForm struct {
	Password string `json:"password" form:"password"`
	Code     string `json:"code" form:"code"`
}

func NewTwoFactorController(g *gin.RouterGroup) *TwoFactorController {
	a := &TwoFactorController{}
	a.initRouter(g)
	return a
}

func (a *TwoFactorController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/2fa")

	g.POST("/status", a.status)
	g.POST("/enroll", a.enroll)
	g.POST("/confirm", a.confirm)
	g.POST("/disable", a.disable)
}

func (a *TwoFactorController) status(c *gin.Context) {
	user := session.GetLoginUser(c)
	status, err := a.twoFactorService.GetStatus(user.Id)
	if err != nil {
		jsonMsg(c, I18n(c, "op_get"), err)
		return
	}
	jsonObj(c, status, nil)
}

// enroll 生成一个新密钥并返回二维码。此时 2FA 尚未生效。
func (a *TwoFactorController) enroll(c *gin.Context) {
	user := session.GetLoginUser(c)
	enrollment, err := a.twoFactorService.BeginEnrollment(user.Id, user.Username)
	if err != nil {
		service.Audit(c, service.EventTwoFactorEnroll, "fail", map[string]interface{}{
			"error": err.Error(),
		})
		jsonMsg(c, I18n(c, "op_2fa_enroll"), err)
		return
	}
	// 审计只记"发生过一次注册"，密钥与二维码都不落日志。
	service.Audit(c, service.EventTwoFactorEnroll, "ok", nil)
	jsonObj(c, enrollment, nil)
}

// confirm 用一个真实验证码确认注册，返回只显示这一次的找回码。
func (a *TwoFactorController) confirm(c *gin.Context) {
	form := &twoFactorCodeForm{}
	if err := c.ShouldBind(form); err != nil {
		jsonMsg(c, I18n(c, "op_2fa_enable"), err)
		return
	}
	user := session.GetLoginUser(c)
	codes, err := a.twoFactorService.ConfirmEnrollment(user.Id, form.Code)
	if err != nil {
		service.Audit(c, service.EventTwoFactorEnable, "fail", map[string]interface{}{
			"error": err.Error(),
		})
		jsonMsg(c, I18n(c, "op_2fa_enable"), err)
		return
	}
	service.Audit(c, service.EventTwoFactorEnable, "ok", nil)
	jsonObj(c, gin.H{"recoveryCodes": codes}, nil)
}

// disable 关闭两步验证，需要口令 + 当前验证码。
func (a *TwoFactorController) disable(c *gin.Context) {
	form := &twoFactorDisableForm{}
	if err := c.ShouldBind(form); err != nil {
		jsonMsg(c, I18n(c, "op_2fa_disable"), err)
		return
	}
	user := session.GetLoginUser(c)
	err := a.twoFactorService.Disable(user.Id, user.Username, form.Password, form.Code)
	if err != nil {
		service.Audit(c, service.EventTwoFactorDisable, "fail", map[string]interface{}{
			"error": err.Error(),
		})
		jsonMsg(c, I18n(c, "op_2fa_disable"), err)
		return
	}
	service.Audit(c, service.EventTwoFactorDisable, "ok", nil)
	jsonMsg(c, I18n(c, "op_2fa_disable"), nil)
}
