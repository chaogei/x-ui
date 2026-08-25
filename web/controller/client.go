package controller

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"x-ui/database/model"
	"x-ui/web/service"
)

// ClientController 管理入站下的终端用户（多用户能力的 HTTP 入口）。
//
// 所有路由都挂在 /xui 组下，因此继承了 checkLogin 与 CSRF 校验。
type ClientController struct {
	clientService service.ClientService
	coreService   service.CoreService
}

func NewClientController(g *gin.RouterGroup) *ClientController {
	a := &ClientController{}
	a.initRouter(g)
	return a
}

func (a *ClientController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/client")

	g.POST("/list/:inboundId", a.listClients)
	g.POST("/add", a.addClient)
	g.POST("/update/:id", a.updateClient)
	g.POST("/del/:id", a.delClient)
	g.POST("/resetTraffic/:id", a.resetTraffic)
	g.POST("/rotateToken/:id", a.rotateToken)
}

func (a *ClientController) listClients(c *gin.Context) {
	inboundID, err := strconv.Atoi(c.Param("inboundId"))
	if err != nil {
		jsonMsg(c, I18n(c, "op_get"), err)
		return
	}
	clients, err := a.clientService.GetClients(inboundID)
	if err != nil {
		jsonMsg(c, I18n(c, "op_get"), err)
		return
	}
	jsonObj(c, clients, nil)
}

func (a *ClientController) addClient(c *gin.Context) {
	client := &model.Client{}
	if err := c.ShouldBind(client); err != nil {
		jsonMsg(c, I18n(c, "op_add"), err)
		return
	}
	err := a.clientService.AddClient(client)
	jsonMsgObj(c, I18n(c, "op_add"), client, err)
	if err == nil {
		a.coreService.SetToNeedRestart()
		// 审计里只留 email 与入站，绝不记录 uuid/password/sub_token ——
		// 审计日志的读者远多于面板管理员。
		service.Audit(c, service.EventClientAdd, "ok", map[string]interface{}{
			"inbound_id": client.InboundId,
			"email":      client.Email,
		})
	} else {
		service.Audit(c, service.EventClientAdd, "fail", map[string]interface{}{
			"inbound_id": client.InboundId,
			"error":      err.Error(),
		})
	}
}

func (a *ClientController) updateClient(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18n(c, "op_update"), err)
		return
	}
	client := &model.Client{Id: id}
	if err := c.ShouldBind(client); err != nil {
		jsonMsg(c, I18n(c, "op_update"), err)
		return
	}
	client.Id = id
	err = a.clientService.UpdateClient(client)
	jsonMsg(c, I18n(c, "op_update"), err)
	if err == nil {
		a.coreService.SetToNeedRestart()
		service.Audit(c, service.EventClientUpdate, "ok", map[string]interface{}{
			"client_id": id,
			"email":     client.Email,
		})
	} else {
		service.Audit(c, service.EventClientUpdate, "fail", map[string]interface{}{
			"client_id": id,
			"error":     err.Error(),
		})
	}
}

func (a *ClientController) delClient(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18n(c, "op_delete"), err)
		return
	}
	err = a.clientService.DelClient(id)
	jsonMsg(c, I18n(c, "op_delete"), err)
	if err == nil {
		a.coreService.SetToNeedRestart()
		service.Audit(c, service.EventClientDelete, "ok", map[string]interface{}{
			"client_id": id,
		})
	} else {
		service.Audit(c, service.EventClientDelete, "fail", map[string]interface{}{
			"client_id": id,
			"error":     err.Error(),
		})
	}
}

func (a *ClientController) resetTraffic(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18n(c, "op_update"), err)
		return
	}
	err = a.clientService.ResetClientTraffic(id)
	jsonMsg(c, I18n(c, "op_reset_traffic"), err)
	if err == nil {
		// 清零可能让一个因超配额被停用的客户端重新变得可用。
		a.coreService.SetToNeedRestart()
		service.Audit(c, service.EventClientResetTraffic, "ok", map[string]interface{}{
			"client_id": id,
		})
	}
}

// rotateToken 轮换订阅 token，旧链接立刻失效。
func (a *ClientController) rotateToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18n(c, "op_update"), err)
		return
	}
	token, err := a.clientService.RotateSubToken(id)
	if errors.Is(err, service.ErrClientNotFound) {
		jsonMsg(c, I18n(c, "op_update"), err)
		return
	}
	jsonMsgObj(c, I18n(c, "op_rotate_token"), gin.H{"subToken": token}, err)
	if err == nil {
		service.Audit(c, service.EventClientRotateToken, "ok", map[string]interface{}{
			"client_id": id,
		})
	}
}
