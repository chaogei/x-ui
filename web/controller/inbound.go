package controller

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"x-ui/database/model"
	"x-ui/web/service"
	"x-ui/web/session"
)

type InboundController struct {
	inboundService service.InboundService
	coreService    service.CoreService
}

func NewInboundController(g *gin.RouterGroup) *InboundController {
	a := &InboundController{}
	a.initRouter(g)
	return a
}

func (a *InboundController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/inbound")

	g.POST("/list", a.getInbounds)
	g.POST("/add", a.addInbound)
	g.POST("/del/:id", a.delInbound)
	g.POST("/update/:id", a.updateInbound)
	g.POST("/resetTraffic/:id", a.resetTraffic)
}

func (a *InboundController) getInbounds(c *gin.Context) {
	user := session.GetLoginUser(c)
	inbounds, err := a.inboundService.GetInbounds(user.Id)
	if err != nil {
		jsonMsg(c, I18n(c, "op_get"), err)
		return
	}
	jsonObj(c, inbounds, nil)
}

func (a *InboundController) addInbound(c *gin.Context) {
	inbound := &model.Inbound{}
	err := c.ShouldBind(inbound)
	if err != nil {
		jsonMsg(c, I18n(c, "op_add"), err)
		return
	}
	user := session.GetLoginUser(c)
	inbound.UserId = user.Id
	inbound.Enable = true
	inbound.Tag = fmt.Sprintf("inbound-%v-%s", inbound.Port, inbound.Protocol)
	err = a.inboundService.AddInbound(inbound)
	jsonMsg(c, I18n(c, "op_add"), err)
	if err == nil {
		a.coreService.SetToNeedRestart()
		service.Audit(c, service.EventInboundAdd, "ok", map[string]interface{}{
			"inbound_id": inbound.Id,
			"protocol":   inbound.Protocol,
			"port":       inbound.Port,
			"remark":     inbound.Remark,
		})
	} else {
		service.Audit(c, service.EventInboundAdd, "fail", map[string]interface{}{
			"protocol": inbound.Protocol,
			"port":     inbound.Port,
			"error":    err.Error(),
		})
	}
}

func (a *InboundController) delInbound(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18n(c, "op_delete"), err)
		return
	}
	err = a.inboundService.DelInbound(id)
	jsonMsg(c, I18n(c, "op_delete"), err)
	if err == nil {
		a.coreService.SetToNeedRestart()
		service.Audit(c, service.EventInboundDelete, "ok", map[string]interface{}{
			"inbound_id": id,
		})
	} else {
		service.Audit(c, service.EventInboundDelete, "fail", map[string]interface{}{
			"inbound_id": id,
			"error":      err.Error(),
		})
	}
}

func (a *InboundController) updateInbound(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18n(c, "op_update"), err)
		return
	}
	inbound := &model.Inbound{
		Id: id,
	}
	err = c.ShouldBind(inbound)
	if err != nil {
		jsonMsg(c, I18n(c, "op_update"), err)
		return
	}
	err = a.inboundService.UpdateInbound(inbound)
	jsonMsg(c, I18n(c, "op_update"), err)
	if err == nil {
		a.coreService.SetToNeedRestart()
		service.Audit(c, service.EventInboundUpdate, "ok", map[string]interface{}{
			"inbound_id": id,
			"protocol":   inbound.Protocol,
			"port":       inbound.Port,
			"remark":     inbound.Remark,
		})
	} else {
		service.Audit(c, service.EventInboundUpdate, "fail", map[string]interface{}{
			"inbound_id": id,
			"error":      err.Error(),
		})
	}
}

// resetTraffic 清零一条入站的累计流量。
//
// 与 /client/resetTraffic/:id 对称。计数器不参与配置生成，所以不请求内核重启。
func (a *InboundController) resetTraffic(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18n(c, "op_reset_traffic"), err)
		return
	}
	err = a.inboundService.ResetTraffic(id)
	jsonMsg(c, I18n(c, "op_reset_traffic"), err)
	if err == nil {
		service.Audit(c, service.EventInboundResetTraffic, "ok", map[string]interface{}{
			"inbound_id": id,
		})
	}
}
