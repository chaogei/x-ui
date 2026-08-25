package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"x-ui/config"
	"x-ui/database"
)

// registerHealthRoutes 挂载运维探针。
//
// 两个端点都不需要登录、不返回任何机密信息，可安全地给 k8s / systemd /
// 负载均衡器做健康检查：
//
//	GET /healthz  进程存活即 200（liveness），不触碰数据库
//	GET /readyz   数据库可 Ping 即 200（readiness）；加 ?core=1 时额外要求
//	              sing-box 内核在运行——面板本身在内核未启动时仍可用于修配置，
//	              所以内核状态默认不参与 readiness 判定。
//
// 路径同时挂在根路径与 basePath 下，避免自定义 basePath 时探针 404。
func (s *Server) registerHealthRoutes(engine *gin.Engine, basePath string) {
	handlers := map[string]gin.HandlerFunc{
		"/healthz":       s.healthz,
		"/readyz":        s.readyz,
		"/api/v1/health": s.healthz,
		"/api/v1/ready":  s.readyz,
	}
	for path, handler := range handlers {
		engine.GET(path, handler)
		if basePath != "/" {
			engine.GET(joinBasePath(basePath, path), handler)
		}
	}
}

// joinBasePath 把 basePath（形如 "/panel/"）与 "/healthz" 拼成 "/panel/healthz"。
func joinBasePath(basePath, path string) string {
	if len(basePath) > 0 && basePath[len(basePath)-1] == '/' {
		basePath = basePath[:len(basePath)-1]
	}
	return basePath + path
}

func (s *Server) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"version": config.GetVersion(),
	})
}

func (s *Server) readyz(c *gin.Context) {
	body := gin.H{"status": "ok", "db": "ok"}

	db := database.GetDB()
	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "db": "not initialized"})
		return
	}
	sqlDB, err := db.DB()
	if err == nil {
		err = sqlDB.Ping()
	}
	if err != nil {
		// 只回传固定文案，不回显驱动错误，避免泄露数据库路径等部署细节。
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "db": "unreachable"})
		return
	}

	if c.Query("core") == "1" {
		if s.coreService.IsCoreRunning() {
			body["core"] = "running"
		} else {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "db": "ok", "core": "stopped"})
			return
		}
	}

	c.JSON(http.StatusOK, body)
}
