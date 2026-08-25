package controller

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"x-ui/web/entity"
	"x-ui/web/global"
	"x-ui/web/service"
)

type ServerController struct {
	BaseController

	serverService service.ServerService

	lastStatus        *service.Status
	lastGetStatusTime time.Time

	lastVersions        []string
	lastGetVersionsTime time.Time
}

func NewServerController(g *gin.RouterGroup) *ServerController {
	a := &ServerController{
		lastGetStatusTime: time.Now(),
	}
	a.initRouter(g)
	a.startTask()
	return a
}

func (a *ServerController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/server")

	g.Use(a.checkLogin)
	g.POST("/status", a.status)
	g.POST("/getCoreVersion", a.getCoreVersion)
	g.POST("/installCore/:version", a.installCore)
}

func (a *ServerController) refreshStatus() {
	a.lastStatus = a.serverService.GetStatus(a.lastStatus)
}

func (a *ServerController) startTask() {
	webServer := global.GetWebServer()
	c := webServer.GetCron()
	c.AddFunc("@every 2s", func() {
		now := time.Now()
		if now.Sub(a.lastGetStatusTime) > time.Minute*3 {
			return
		}
		a.refreshStatus()
	})
}

func (a *ServerController) status(c *gin.Context) {
	a.lastGetStatusTime = time.Now()

	jsonObj(c, a.lastStatus, nil)
}

func (a *ServerController) getCoreVersion(c *gin.Context) {
	now := time.Now()
	if now.Sub(a.lastGetVersionsTime) <= time.Minute {
		jsonObj(c, a.lastVersions, nil)
		return
	}

	versions, err := a.serverService.GetCoreVersions()
	if err != nil {
		jsonMsg(c, I18n(c, "op_get_version"), err)
		return
	}

	a.lastVersions = versions
	a.lastGetVersionsTime = time.Now()

	jsonObj(c, versions, nil)
}

// installCore 安装指定版本的 sing-box 内核。
// 版本号在发起任何网络/文件操作之前先过白名单，非法输入直接以 400 拒绝。
func (a *ServerController) installCore(c *gin.Context) {
	version := c.Param("version")
	if _, err := service.ValidateCoreVersion(version); err != nil {
		c.JSON(http.StatusBadRequest, entity.Msg{
			Success: false,
			Msg:     I18n(c, "op_install_core") + ": " + I18n(c, "err_invalid_version"),
		})
		return
	}
	err := a.serverService.UpdateCore(version)
	jsonMsg(c, I18n(c, "op_install_core"), err)
}
