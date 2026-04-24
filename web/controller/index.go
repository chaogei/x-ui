package controller

import (
	"net/http"
	"time"
	"x-ui/logger"
	"x-ui/web/job"
	"x-ui/web/service"
	"x-ui/web/session"

	"github.com/gin-gonic/gin"
)

type LoginForm struct {
	Username string `json:"username" form:"username"`
	Password string `json:"password" form:"password"`
}

type IndexController struct {
	BaseController

	userService  service.UserService
	loginLimiter *service.LoginLimiter
}

// NewIndexController 通过构造函数注入登录限流器，保持全局共享单例，
// 由 web.Server 持有生命周期。
func NewIndexController(g *gin.RouterGroup, limiter *service.LoginLimiter) *IndexController {
	a := &IndexController{loginLimiter: limiter}
	a.initRouter(g)
	return a
}

func (a *IndexController) initRouter(g *gin.RouterGroup) {
	g.GET("/", a.index)
	g.POST("/login", a.login)
	g.GET("/logout", a.logout)
}

func (a *IndexController) index(c *gin.Context) {
	if session.IsLogin(c) {
		c.Redirect(http.StatusTemporaryRedirect, "xui/")
		return
	}
	html(c, "login.html", I18n(c, "login"), nil)
}

func (a *IndexController) login(c *gin.Context) {
	ip := getRemoteIp(c)

	// 预检：已处于锁定期的 IP 直接拒绝，避免进入 DB 查询放大爆破面。
	if locked, retry := a.loginLimiter.IsLocked(ip); locked {
		service.Audit(c, service.EventLoginLocked, "locked", map[string]interface{}{
			"retry_after_sec": int(retry.Seconds()),
		})
		pureJsonMsg(c, false, I18n(c, "auth_ip_locked"))
		return
	}

	var form LoginForm
	if err := c.ShouldBind(&form); err != nil {
		pureJsonMsg(c, false, I18n(c, "auth_form_error"))
		return
	}
	if form.Username == "" {
		pureJsonMsg(c, false, I18n(c, "auth_username_required"))
		return
	}
	if form.Password == "" {
		pureJsonMsg(c, false, I18n(c, "auth_password_required"))
		return
	}

	user := a.userService.CheckUser(form.Username, form.Password)
	timeStr := time.Now().Format("2006-01-02 15:04:05")
	if user == nil {
		locked, remaining := a.loginLimiter.RecordFail(ip)
		job.NewStatsNotifyJob().UserLoginNotify(form.Username, ip, timeStr, 0)
		// 仅记录用户名和来源 IP，禁止泄露用户提交的密码到日志。
		logger.Infof("login failed: username=%q ip=%s", form.Username, ip)
		service.Audit(c, service.EventLoginFail, "fail", map[string]interface{}{
			"username":  form.Username,
			"remaining": remaining,
			"locked":    locked,
		})
		pureJsonMsg(c, false, I18n(c, "auth_invalid_credentials"))
		return
	}

	a.loginLimiter.Reset(ip)
	if err := session.SetLoginUser(c, user); err != nil {
		logger.Warning("set login session failed:", err)
		pureJsonMsg(c, false, I18n(c, "auth_session_save_failed"))
		return
	}
	logger.Infof("login success: username=%q id=%d ip=%s", user.Username, user.Id, ip)
	job.NewStatsNotifyJob().UserLoginNotify(form.Username, ip, timeStr, 1)
	service.Audit(c, service.EventLoginSuccess, "ok", nil)

	pureJsonMsg(c, true, I18n(c, "auth_login_success"))
}

func (a *IndexController) logout(c *gin.Context) {
	// 在清除 session 前触发审计以保留 user 上下文
	if user := session.GetLoginUser(c); user != nil {
		logger.Info("user", user.Id, "logout")
		service.Audit(c, service.EventLogout, "ok", nil)
	}
	session.ClearSession(c)
	c.Redirect(http.StatusTemporaryRedirect, c.GetString("base_path"))
}
