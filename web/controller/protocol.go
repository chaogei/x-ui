package controller

import (
	"github.com/gin-gonic/gin"

	"x-ui/core/singbox/spec"
	"x-ui/web/service"
)

// ProtocolController 暴露 sing-box 协议元数据，作为前端 ProtocolSpec 消费的权威来源。
//
// 职责单一：仅转发 core/singbox/spec 注册表；不涉及任何业务逻辑或数据库交互。
// 未来添加 multi-user / validation 能力时，字段通过扩展 spec.Spec 向前端传递，
// 此 controller 保持透明。
type ProtocolController struct{}

// NewProtocolController 注册 /api/protocols 路由。
// 调用方需确保 RouterGroup 已挂载 checkLogin，元数据虽非敏感，但限制在登录态下
// 减少公网暴露面。
func NewProtocolController(g *gin.RouterGroup) *ProtocolController {
	a := &ProtocolController{}
	a.initRouter(g)
	return a
}

func (a *ProtocolController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/api")
	g.GET("/protocols", a.listProtocols)
	g.POST("/reality/keypair", a.realityKeyPair)
}

// realityKeyPair 生成一对全新的 Reality X25519 密钥。
//
// 前端「生成密钥」按钮调用本接口后同时填入 private_key 与 public_key，
// 保证两者天然匹配——分享链接里的 pbk 参数因此不可能再为空或错配。
func (a *ProtocolController) realityKeyPair(c *gin.Context) {
	pair, err := service.GenerateRealityKeyPair()
	jsonObj(c, pair, err)
}

// listProtocols 返回全部协议元数据，顺序与注册表 order 保持一致。
// 前端在初始化阶段一次性拉取后缓存，无需频繁调用。
func (a *ProtocolController) listProtocols(c *gin.Context) {
	jsonObj(c, spec.All(), nil)
}
