package controller

import (
	"github.com/gin-gonic/gin"

	"x-ui/web/service"
)

type XUIController struct {
	BaseController

	userService service.UserService

	inboundController *InboundController
	clientController  *ClientController
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
	a.clientController = NewClientController(g)
	a.settingController = NewSettingController(g)
	a.protocolController = NewProtocolController(g)
}

func (a *XUIController) index(c *gin.Context) {
	html(c, "index.html", I18n(c, "menu_system_status"), nil)
}

// inbounds 渲染入站列表页。
//
// initial_credentials 驱动页面顶部的安全告警：为 true 说明管理员仍在使用
// 首次启动自动生成的随机口令，尚未自行改密。历史代码把这个提示写死为
// `v-if="false"` 永久隐藏，等于告警形同虚设。
func (a *XUIController) inbounds(c *gin.Context) {
	html(c, "inbounds.html", I18n(c, "menu_inbound_list"), gin.H{
		"initial_credentials": a.userService.UsingInitialCredentials(),
	})
}

func (a *XUIController) setting(c *gin.Context) {
	html(c, "setting.html", I18n(c, "menu_panel_setting"), nil)
}
