package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"net"
	"net/http"
	"strings"
	"x-ui/config"
	"x-ui/core/singbox/spec"
	"x-ui/logger"
	"x-ui/web/entity"
	"x-ui/web/middleware"
)

// I18n 按 gin.Context 里注入的 localizer 翻译一个 messageID。
// 找不到 localizer 或翻译失败时回退为 messageID 本身（前端肉眼可见，便于定位漏 key）。
func I18n(c *gin.Context, messageID string) string {
	v, ok := c.Get("localizer")
	if !ok {
		return messageID
	}
	localizer, ok := v.(*i18n.Localizer)
	if !ok {
		return messageID
	}
	msg, err := localizer.Localize(&i18n.LocalizeConfig{MessageID: messageID})
	if err != nil {
		logger.Warningf("i18n localize failed for %q: %v", messageID, err)
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

func getRemoteIp(c *gin.Context) string {
	value := c.GetHeader("X-Forwarded-For")
	if value != "" {
		ips := strings.Split(value, ",")
		return ips[0]
	} else {
		addr := c.Request.RemoteAddr
		ip, _, _ := net.SplitHostPort(addr)
		return ip
	}
}

func jsonMsg(c *gin.Context, msg string, err error) {
	jsonMsgObj(c, msg, nil, err)
}

func jsonObj(c *gin.Context, obj interface{}, err error) {
	jsonMsgObj(c, "", obj, err)
}

func jsonMsgObj(c *gin.Context, msg string, obj interface{}, err error) {
	m := entity.Msg{
		Obj: obj,
	}
	if err == nil {
		m.Success = true
		if msg != "" {
			m.Msg = msg + "成功"
		}
	} else {
		m.Success = false
		m.Msg = msg + "失败: " + err.Error()
		logger.Warning(msg+"失败: ", err)
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
	c.HTML(http.StatusOK, name, getContext(data))
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
