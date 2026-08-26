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

// htmlLangAttr 把内部词典标签映射成 <html lang> 用的 BCP-47 值。
var htmlLangAttr = map[string]string{
	"zh-Hans": "zh-CN",
	"zh-Hant": "zh-TW",
	"en-US":   "en",
}

// html 渲染面板外壳页。
//
// 前端是一个 Vue 3 单页应用（源码在 web/frontend，产物在 web/assets/dist），
// Go 这边只剩一张外壳模板 app.html：挂载点 + 一行引导数据。
//
// 不走 gin 的 HTMLRender：后者拿不到 gin.Context，无法给模板注入
// 「本次请求的 localizer」。这里改用 web/render 的请求级渲染器。
//
// page 决定前端挂哪个视图。由服务端下发而不是让前端解析 location.pathname：
// basePath 可以被改成任意前缀，前端自己猜必然会猜错。
func html(c *gin.Context, page string, title string, extra gin.H) {
	basePath := c.GetString("base_path")
	tag := locale.CurrentTag(c)

	// boot 是注入到 window.__XUI__ 的全部服务端状态。
	//
	// i18n 整本词典一次性下发：SPA 渲染时不可能为每个字符串回一次服务端，
	// 而在前端另建一套翻译文件必然与 translation/*.toml 漂移。
	boot := gin.H{
		"basePath": basePath,
		"page":     page,
		// 与 <meta name="csrf-token"> 同源，前端优先读 meta。
		"csrfToken": middleware.SessionCSRFToken(c),
		"lang":      tag,
		"langCode":  locale.CurrentCode(c),
		"languages": locale.Supported,
		"i18n":      locale.Messages(tag),
		// protocols 是 sing-box 协议元数据单一来源（core/singbox/spec）的前端副本。
		"protocols":          spec.All(),
		"version":            config.GetVersion(),
		"requestUri":         c.Request.RequestURI,
		"initialCredentials": false,
	}
	for key, value := range extra {
		boot[key] = value
	}

	data := gin.H{
		"title":      title,
		"base_path":  basePath,
		"cur_ver":    config.GetVersion(),
		"csrf_token": middleware.SessionCSRFToken(c),
		"html_lang":  htmlLangAttr[tag],
		"noscript":   I18n(c, "noscript_hint"),
		"boot":       boot,
	}

	r := render.Global()
	if r == nil {
		c.String(http.StatusInternalServerError, "template renderer not initialized")
		return
	}
	loc := locale.FromContext(c)
	if err := r.Render(c, http.StatusOK, "app.html", locale.FuncMap(loc), data); err != nil {
		logger.Warningf("render page %q failed: %v", page, err)
	}
}

func isAjax(c *gin.Context) bool {
	return c.GetHeader("X-Requested-With") == "XMLHttpRequest"
}
