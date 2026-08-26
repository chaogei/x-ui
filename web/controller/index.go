package controller

import (
	"net/http"
	"time"

	"x-ui/database/model"
	"x-ui/logger"
	"x-ui/web/job"
	"x-ui/web/metrics"
	"x-ui/web/service"
	"x-ui/web/session"

	"github.com/gin-gonic/gin"
)

type LoginForm struct {
	Username string `json:"username" form:"username"`
	Password string `json:"password" form:"password"`
	// TwoFactorCode 是 TOTP 验证码或一张找回码。
	// 账号未开启两步验证时为空且被忽略。
	TwoFactorCode string `json:"twoFactorCode" form:"twoFactorCode"`
}

type IndexController struct {
	BaseController

	userService      service.UserService
	twoFactorService service.TwoFactorService
	loginLimiter     *service.LoginLimiter
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
		metrics.RecordLoginFailure(metrics.ReasonLocked)
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
		metrics.RecordLoginFailure(metrics.ReasonBadCredentials)
		pureJsonMsg(c, false, I18n(c, "auth_invalid_credentials"))
		return
	}

	// 口令对了还不算登录成功：开启了两步验证的账号必须再过一道。
	// 这一步放在建立 session 之前，中途失败不会留下任何可用的会话。
	if done := a.checkTwoFactor(c, user, form, ip, timeStr); !done {
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

// checkTwoFactor 在口令通过之后执行第二因素校验。
//
// 返回 false 表示已经向客户端写了失败响应，调用方必须立即返回。
//
// 两个刻意的设计：
//   - 二次验证失败同样计入登录限流。否则攻击者拿到口令后可以无限次
//     暴力猜 6 位验证码，一百万次就能撞开——比口令本身还弱。
//   - 读取 2FA 状态出错时按"已启用"处理并拒绝登录。让一次数据库抖动
//     就跳过第二因素，等于给攻击者一个可触发的降级开关。
func (a *IndexController) checkTwoFactor(c *gin.Context, user *model.User, form LoginForm, ip, timeStr string) bool {
	enabled, err := a.twoFactorService.IsEnabled(user.Id)
	if err != nil {
		logger.Warning("read two-factor state failed:", err)
		service.Audit(c, service.EventTwoFactorFail, "fail", map[string]interface{}{
			"username": form.Username,
			"error":    err.Error(),
		})
		pureJsonMsg(c, false, I18n(c, "auth_totp_unavailable"))
		return false
	}
	if !enabled {
		return true
	}

	if form.TwoFactorCode == "" {
		// 这条响应会让前端把登录框切换成"请输入验证码"的形态，
		// 因此它必须能与"验证码错误"区分开。这不泄露额外信息：
		// 口令已经验过了，对面本来就知道自己拿到了正确的口令。
		pureJsonMsg(c, false, I18n(c, "auth_totp_required"))
		return false
	}

	usedRecoveryCode, err := a.twoFactorService.Verify(user.Id, form.TwoFactorCode)
	if err != nil {
		locked, remaining := a.loginLimiter.RecordFail(ip)
		job.NewStatsNotifyJob().UserLoginNotify(form.Username, ip, timeStr, 0)
		logger.Infof("two-factor check failed: username=%q ip=%s", form.Username, ip)
		service.Audit(c, service.EventTwoFactorFail, "fail", map[string]interface{}{
			"username":  form.Username,
			"remaining": remaining,
			"locked":    locked,
		})
		metrics.RecordLoginFailure(metrics.ReasonTwoFactor)
		pureJsonMsg(c, false, I18n(c, "auth_totp_invalid"))
		return false
	}
	if usedRecoveryCode {
		// 找回码用一次少一张，必须留痕：这往往是"手机丢了"或
		// "有人拿着一张偷来的纸条"的第一个信号。
		service.Audit(c, service.EventRecoveryCodeUsed, "ok", map[string]interface{}{
			"username": form.Username,
		})
	}
	return true
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
