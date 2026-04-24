package controller

import (
	"github.com/gin-gonic/gin"
)

type XUIController struct {
	BaseController

	inboundController *InboundController
	settingController *SettingController
	// protocolController 暴露 sing-box 协议元数据，供前端初始化时拉取，
	// 作为 ProtocolSpec 的单一来源（SSoT）。
	protocolController *ProtocolController
}

func NewXUIController(g *gin.RouterGroup) *XUIController {
	a := &XUIController{}
	a.initRouter(g)
	return a
}

func (a *XUIController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/xui")
	g.Use(a.checkLogin)

	g.GET("/", a.index)
	g.GET("/inbounds", a.inbounds)
	g.GET("/setting", a.setting)

	a.inboundController = NewInboundController(g)
	a.settingController = NewSettingController(g)
	a.protocolController = NewProtocolController(g)
}

func (a *XUIController) index(c *gin.Context) {
	html(c, "index.html", I18n(c, "menu_system_status"), nil)
}

func (a *XUIController) inbounds(c *gin.Context) {
	html(c, "inbounds.html", I18n(c, "menu_inbound_list"), nil)
}

func (a *XUIController) setting(c *gin.Context) {
	html(c, "setting.html", I18n(c, "menu_panel_setting"), nil)
}
