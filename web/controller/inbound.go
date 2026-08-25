package controller

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"x-ui/database/model"
	"x-ui/logger"
	"x-ui/web/global"
	"x-ui/web/service"
	"x-ui/web/session"
)

type InboundController struct {
	inboundService service.InboundService
	coreService    service.CoreService

	// restartBackoff 抑制"配置永久非法 → 每 10 秒重启一次内核"的忙循环。
	restartBackoff *service.Backoff
}

func NewInboundController(g *gin.RouterGroup) *InboundController {
	a := &InboundController{
		restartBackoff: service.NewBackoff(10*time.Second, 10*time.Minute),
	}
	a.initRouter(g)
	a.startTask()
	return a
}

func (a *InboundController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/inbound")

	g.POST("/list", a.getInbounds)
	g.POST("/add", a.addInbound)
	g.POST("/del/:id", a.delInbound)
	g.POST("/update/:id", a.updateInbound)
}

// startTask 以 10 秒为节拍消费"需要重启内核"的标志位。
//
// 配置非法（例如用户填了 sing-box 拒绝的字段）时重启会持续失败。
// 若无退避，这里会每 10 秒 fork 一次 sing-box 并写一行错误日志，
// 直到用户手动修好为止——CPU、磁盘与日志都在白白消耗。
// 现在改为指数退避（10s → 20s → … → 封顶 10min），任何一次成功立即复位。
func (a *InboundController) startTask() {
	webServer := global.GetWebServer()
	c := webServer.GetCron()
	c.AddFunc("@every 10s", func() {
		if !a.coreService.IsNeedRestartAndSetFalse() {
			return
		}
		if !a.restartBackoff.Ready() {
			// 仍在退避窗口内：把标志放回去，等下一个周期再试。
			a.coreService.SetToNeedRestart()
			return
		}
		if err := a.coreService.RestartCore(false); err != nil {
			delay := a.restartBackoff.Fail()
			logger.Errorf("restart sing-box failed, next attempt in %v: %v", delay, err)
			a.coreService.SetToNeedRestart()
			return
		}
		a.restartBackoff.Succeed()
	})
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
