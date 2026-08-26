package controller

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"x-ui/web/service"
)

// SubscriptionController 提供以 token 鉴权的订阅接口。
//
// 这是面板上唯一一个不需要登录的写外内容接口，所以约束格外紧：
//   - 只接受 GET；
//   - 任何失败（token 不存在 / 用户停用 / 超配额 / 入站关闭）都回同一个
//     纯文本 404，不透露任何区别；
//   - 不写审计明文 token。
type SubscriptionController struct {
	subscriptionService service.SubscriptionService
	settingService      service.SettingService
}

// NewSubscriptionController 把 sub/:token 挂在 basePath 组上。
// 调用方必须传入尚未挂 checkLogin 的组。
func NewSubscriptionController(g *gin.RouterGroup) *SubscriptionController {
	a := &SubscriptionController{}
	g.GET("/sub/:token", a.serve)
	return a
}

func (a *SubscriptionController) serve(c *gin.Context) {
	token := c.Param("token")
	format := service.ParseSubFormat(c.Query("format"))

	sub, err := a.subscriptionService.Render(token, format, a.serverAddress(c))
	if err != nil {
		if !errors.Is(err, service.ErrSubscriptionNotFound) {
			// 内部故障也只对外回 404，细节留在服务端日志里。
			service.Audit(c, service.EventSubscriptionFetch, "error", map[string]interface{}{
				"error": err.Error(),
			})
		}
		c.String(http.StatusNotFound, "not found")
		return
	}

	c.Header("Content-Type", sub.ContentType)
	c.Header("Content-Disposition", `attachment; filename="`+sub.Filename+`"`)
	if sub.UserInfo != "" {
		c.Header("Subscription-Userinfo", sub.UserInfo)
	}
	// 订阅内容含凭证，任何中间缓存都不该留副本。
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, sub.Body)
}

// serverAddress 决定分享链接里写哪个主机名。
//
// 优先用面板设置里的 subAddress：反向代理后面板自己看到的 Host 常常是
// 内网名字，直接写进订阅会让所有客户端连到一个解析不出来的地址。
// 未配置时退回请求的 Host（去掉端口）。
func (a *SubscriptionController) serverAddress(c *gin.Context) string {
	if configured, err := a.settingService.GetSubAddress(); err == nil {
		if configured = strings.TrimSpace(configured); configured != "" {
			return configured
		}
	}
	host := c.Request.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}
