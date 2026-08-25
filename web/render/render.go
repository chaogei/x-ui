// Package render 提供请求级模板渲染。
//
// gin 的 HTMLRender 无法访问 gin.Context，因此没法把「本次请求的 localizer」
// 注入模板 FuncMap。本包绕开 gin 的渲染器：持有一份解析好的模板集合，
// 每次渲染时 Clone 出副本并覆盖 FuncMap，从而让 `{{ i18n "key" }}` 使用
// 请求自己的语言，彻底消除跨请求共享 localizer 的数据竞争。
package render

import (
	"bytes"
	"html/template"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// Renderer 持有基础模板集合。零值不可用，必须经 New 构造。
type Renderer struct {
	mu   sync.RWMutex
	base *template.Template
}

// New 用已解析的模板集合构造渲染器。
func New(base *template.Template) *Renderer {
	return &Renderer{base: base}
}

// Render 以 funcs 覆盖模板函数后渲染 name 模板。
//
// 先渲染到内存缓冲再一次性写出：模板执行中途出错时不会把半截 HTML
// 连同 200 状态码发给客户端。
func (r *Renderer) Render(c *gin.Context, code int, name string, funcs template.FuncMap, data interface{}) error {
	r.mu.RLock()
	base := r.base
	r.mu.RUnlock()
	if base == nil {
		return http.ErrAbortHandler
	}

	cloned, err := base.Clone()
	if err != nil {
		return err
	}
	if len(funcs) > 0 {
		cloned = cloned.Funcs(funcs)
	}

	var buf bytes.Buffer
	if err := cloned.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(code)
	_, err = c.Writer.Write(buf.Bytes())
	return err
}

var (
	globalMu sync.RWMutex
	global   *Renderer
)

// SetGlobal 注册进程内的当前渲染器，供 controller 包使用。
// web.Server 在初始化路由时调用；重启面板会覆盖为新实例。
func SetGlobal(r *Renderer) {
	globalMu.Lock()
	global = r
	globalMu.Unlock()
}

// Global 返回当前注册的渲染器；未注册时返回 nil。
func Global() *Renderer {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}
