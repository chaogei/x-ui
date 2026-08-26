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

// legacyCounterReset 识别老前端表达"清零"的方式：提交一份 up=0、down=0 的表单。
//
// UpdateInbound 本身已经不再理会请求里的计数器（那两个数是页面加载时的快照，
// 写回去会抹掉这期间统计到的流量）。但仓库里内嵌的 Vue 产物仍然靠"存一份 0"
// 来清零，在它改调 /inbound/resetTraffic 之前，这里替它把意图翻译过去。
//
// 判断按"字段是否出现"而不是绑定后的值：绑定完 0 有两种来源——显式提交的 0，
// 和根本没提交这个字段。只有前者才是清零意图，后者（比如脚本只改备注）
// 必须原样保留计数器。
//
// 前端切到专用接口之后，删掉这个函数和它唯一的调用点即可。
func legacyCounterReset(c *gin.Context) bool {
	up, upSent := c.GetPostForm("up")
	down, downSent := c.GetPostForm("down")
	return upSent && downSent && up == "0" && down == "0"
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
	if err == nil && legacyCounterReset(c) {
		err = a.inboundService.ResetTraffic(id)
	}
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
