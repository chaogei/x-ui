package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"x-ui/config"
	"x-ui/core/singbox/spec"
	"x-ui/logger"
	"x-ui/web/entity"
	"x-ui/web/locale"
	"x-ui/web/middleware"
	"x-ui/web/render"
	"x-ui/web/service"
)

// I18n 按 gin.Context 里注入的（请求级）localizer 翻译一个 messageID。
// 找不到 localizer 或翻译失败时回退为 messageID 本身（前端肉眼可见，便于定位漏 key）。
func I18n(c *gin.Context, messageID string) string {
	loc := locale.FromContext(c)
	if loc == nil {
		return messageID
	}
	msg, err := locale.Translate(loc, messageID)
	if err != nil || msg == "" {
		if err != nil {
			logger.Warningf("i18n localize failed for %q: %v", messageID, err)
		}
		return messageID
	}
	return msg
}

func getUriId(c *gin.Context) int64 {
	s := struct {
		Id int64 `uri:"id"`
	}{}

	_ = c.BindUri(&s)
	return s.Id
}

// getRemoteIp 返回请求方 IP。
//
// 实现委托给 service.ClientIP（内部走 gin 的受信代理链），
// 与审计日志共用同一判定，避免"限流按 A 计数、审计记录 B"的语义分裂。
// 默认不信任任何代理，因此伪造 X-Forwarded-For 不会改变限流分桶。
func getRemoteIp(c *gin.Context) string {
	return service.ClientIP(c)
}

func jsonMsg(c *gin.Context, msg string, err error) {
	jsonMsgObj(c, msg, nil, err)
}

func jsonObj(c *gin.Context, obj interface{}, err error) {
	jsonMsgObj(c, "", obj, err)
}

// jsonMsgObj 构造统一的 {success,msg,obj} 响应。
//
// 成功/失败后缀通过 i18n key（op_suffix_success / op_suffix_fail）本地化，
// 历史实现硬编码了简体中文，让 EN / zh-Hant 用户看到中英混排的操作提示。
func jsonMsgObj(c *gin.Context, msg string, obj interface{}, err error) {
	m := entity.Msg{
		Obj: obj,
	}
	if err == nil {
		m.Success = true
		if msg != "" {
			m.Msg = msg + I18n(c, "op_suffix_success")
		}
	} else {
		m.Success = false
		m.Msg = msg + I18n(c, "op_suffix_fail") + err.Error()
		logger.Warning(msg+I18n(c, "op_suffix_fail"), err)
	}
	c.JSON(http.StatusOK, m)
}

func pureJsonMsg(c *gin.Context, success bool, msg string) {
	if success {
		c.JSON(http.StatusOK, entity.Msg{
			Success: true,
			Msg:     msg,
		})
	} else {
		c.JSON(http.StatusOK, entity.Msg{
			Success: false,
			Msg:     msg,
		})
	}
}

// html 渲染一个模板。
//
// 不走 gin 的 HTMLRender：后者拿不到 gin.Context，无法给模板注入
// 「本次请求的 localizer」。这里改用 web/render 的请求级渲染器，
// 每次执行前 Clone 模板并绑定当前语言的 i18n 函数。
func html(c *gin.Context, name string, title string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	data["title"] = title
	data["request_uri"] = c.Request.RequestURI
	data["base_path"] = c.GetString("base_path")
	// csrf_token 由 middleware.CSRF 在请求 context 里注入，供 head 模板渲染到 <meta name="csrf-token">
	data["csrf_token"] = middleware.SessionCSRFToken(c)
	// protocol_specs 是 sing-box 协议元数据单一来源的前端副本。
	// 以 []spec.Spec 注入，Go html/template 在 <script> 上下文会按 JS 字面量编码，
	// 前端拿到的直接是对象字面量，无需 JSON.parse。
	data["protocol_specs"] = spec.All()
	// 语言切换器需要知道可选语言与当前选择。
	data["languages"] = locale.Supported
	data["current_lang"] = locale.CurrentCode(c)

	r := render.Global()
	if r == nil {
		c.String(http.StatusInternalServerError, "template renderer not initialized")
		return
	}
	loc := locale.FromContext(c)
	if err := r.Render(c, http.StatusOK, name, locale.FuncMap(loc), getContext(data)); err != nil {
		logger.Warningf("render template %q failed: %v", name, err)
	}
}

func getContext(h gin.H) gin.H {
	a := gin.H{
		"cur_ver": config.GetVersion(),
	}
	if h != nil {
		for key, value := range h {
			a[key] = value
		}
	}
	return a
}

func isAjax(c *gin.Context) bool {
	return c.GetHeader("X-Requested-With") == "XMLHttpRequest"
}
