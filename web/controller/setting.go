package controller

import (
	"errors"
	"github.com/gin-gonic/gin"
	"time"
	"x-ui/web/entity"
	"x-ui/web/service"
	"x-ui/web/session"
)

type updateUserForm struct {
	OldUsername string `json:"oldUsername" form:"oldUsername"`
	OldPassword string `json:"oldPassword" form:"oldPassword"`
	NewUsername string `json:"newUsername" form:"newUsername"`
	NewPassword string `json:"newPassword" form:"newPassword"`
}

type SettingController struct {
	settingService service.SettingService
	userService    service.UserService
	panelService   service.PanelService
}

func NewSettingController(g *gin.RouterGroup) *SettingController {
	a := &SettingController{}
	a.initRouter(g)
	return a
}

func (a *SettingController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/setting")

	g.POST("/all", a.getAllSetting)
	g.POST("/update", a.updateSetting)
	g.POST("/updateUser", a.updateUser)
	g.POST("/restartPanel", a.restartPanel)
}

func (a *SettingController) getAllSetting(c *gin.Context) {
	allSetting, err := a.settingService.GetAllSetting()
	if err != nil {
		jsonMsg(c, "获取设置", err)
		return
	}
	jsonObj(c, allSetting, nil)
}

func (a *SettingController) updateSetting(c *gin.Context) {
	allSetting := &entity.AllSetting{}
	err := c.ShouldBind(allSetting)
	if err != nil {
		jsonMsg(c, "修改设置", err)
		return
	}
	err = a.settingService.UpdateAllSetting(allSetting)
	if err == nil {
		service.Audit(c, service.EventSettingUpdate, "ok", nil)
	} else {
		service.Audit(c, service.EventSettingUpdate, "fail", map[string]interface{}{
			"error": err.Error(),
		})
	}
	jsonMsg(c, "修改设置", err)
}

// updateUser 修改面板账号。
// 旧密码校验必须走 UserService.CheckUser 走 bcrypt 兼容路径，
// 因为 session 已不再持久化密码字段（见 session.SetLoginUser）。
func (a *SettingController) updateUser(c *gin.Context) {
	form := &updateUserForm{}
	err := c.ShouldBind(form)
	if err != nil {
		jsonMsg(c, "修改用户", err)
		return
	}
	user := session.GetLoginUser(c)
	verified := a.userService.CheckUser(form.OldUsername, form.OldPassword)
	if verified == nil || verified.Id != user.Id {
		service.Audit(c, service.EventUserUpdate, "fail", map[string]interface{}{
			"reason": "wrong_old_credentials",
		})
		jsonMsg(c, "修改用户", errors.New(I18n(c, "auth_wrong_old_credentials")))
		return
	}
	if form.NewUsername == "" || form.NewPassword == "" {
		jsonMsg(c, "修改用户", errors.New(I18n(c, "auth_new_empty")))
		return
	}
	err = a.userService.UpdateUser(user.Id, form.NewUsername, form.NewPassword)
	if err == nil {
		user.Username = form.NewUsername
		// SetLoginUser 会主动清空 Password 字段，这里无需也不能再塞明文/哈希
		session.SetLoginUser(c, user)
		service.Audit(c, service.EventUserUpdate, "ok", map[string]interface{}{
			"new_username": form.NewUsername,
		})
	} else {
		service.Audit(c, service.EventUserUpdate, "fail", map[string]interface{}{
			"error": err.Error(),
		})
	}
	jsonMsg(c, "修改用户", err)
}

func (a *SettingController) restartPanel(c *gin.Context) {
	service.Audit(c, service.EventPanelRestart, "ok", nil)
	err := a.panelService.RestartPanel(time.Second * 3)
	jsonMsg(c, "重启面板", err)
}
