package controller

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"x-ui/web/entity"
	"x-ui/web/global"
	"x-ui/web/service"
)

// ServerController 暴露面板首页的机器状态与内核版本。
//
// 并发契约：缓存字段（lastStatus / lastGetStatusTime / lastVersions /
// lastGetVersionsTime）同时被两方触碰——每 2 秒跑一次的 cron goroutine，
// 以及每个 /server/status 请求所在的 gin goroutine。它们必须全部在 mu 之下
// 读写。缓存的值本身是只读快照：ServerService.GetStatus 每次返回一个新的
// *Status，发布之后没人再改它，所以在锁外序列化那个指针是安全的。
//
// 采集与拉取版本都在锁外进行：GetStatus 要读 /proc，GetCoreVersions 要出网，
// 拿着锁做这些事等于让每个状态请求排在系统调用后面。
type ServerController struct {
	BaseController

	serverService service.ServerService

	mu                sync.RWMutex
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
	a.mu.RLock()
	previous := a.lastStatus
	a.mu.RUnlock()

	// 采集要读 /proc 与磁盘，耗时远大于一次赋值，放在锁外做。
	status := a.serverService.GetStatus(previous)

	a.mu.Lock()
	a.lastStatus = status
	a.mu.Unlock()
}

// statusRequestedWithin 报告最近一次状态请求是否落在 window 之内。
// 没人看面板时就不必每 2 秒去读一遍 /proc。
func (a *ServerController) statusRequestedWithin(window time.Duration) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return time.Since(a.lastGetStatusTime) <= window
}

func (a *ServerController) startTask() {
	webServer := global.GetWebServer()
	c := webServer.GetCron()
	c.AddFunc("@every 2s", func() {
		if !a.statusRequestedWithin(3 * time.Minute) {
			return
		}
		a.refreshStatus()
	})
}

func (a *ServerController) status(c *gin.Context) {
	a.mu.Lock()
	a.lastGetStatusTime = time.Now()
	status := a.lastStatus
	a.mu.Unlock()

	jsonObj(c, status, nil)
}

func (a *ServerController) getCoreVersion(c *gin.Context) {
	a.mu.RLock()
	cached, cachedAt := a.lastVersions, a.lastGetVersionsTime
	a.mu.RUnlock()
	if time.Since(cachedAt) <= time.Minute {
		jsonObj(c, cached, nil)
		return
	}

	// 出网拉 release 列表，同样不能拿着锁做。并发的两个请求可能都错过缓存
	// 而各拉一次；多一次 HTTP 请求，好过让状态接口跟着阻塞。
	versions, err := a.serverService.GetCoreVersions()
	if err != nil {
		jsonMsg(c, I18n(c, "op_get_version"), err)
		return
	}

	a.mu.Lock()
	a.lastVersions = versions
	a.lastGetVersionsTime = time.Now()
	a.mu.Unlock()

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
