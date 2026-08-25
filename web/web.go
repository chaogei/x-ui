package web

import (
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"x-ui/config"
	"x-ui/logger"
	"x-ui/util/common"
	"x-ui/web/controller"
	"x-ui/web/job"
	"x-ui/web/locale"
	"x-ui/web/middleware"
	"x-ui/web/network"
	"x-ui/web/render"
	"x-ui/web/service"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
)

//go:embed all:assets
var assetsFS embed.FS

// 必须用 all: 前缀：默认的 embed 模式会跳过以 `_` 或 `.` 开头的文件，
// 而 html/xui/form/ 下的 _tls.html 与 _transport.html 正是这种命名。
// 少了它们，release 构建里每个协议表单都会因 `no such template "form/_tls"`
// 渲染失败，入站页返回 200 但内容为空——开发模式（从磁盘读模板）看不出来。
//
//go:embed all:html
var htmlFS embed.FS

//go:embed all:translation
var i18nFS embed.FS

var startTime = time.Now()

// shutdownTimeout 是 HTTP 服务优雅停机的等待上限。
const shutdownTimeout = 10 * time.Second

type wrapAssetsFS struct {
	embed.FS
}

func (f *wrapAssetsFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open("assets/" + name)
	if err != nil {
		return nil, err
	}
	return &wrapAssetsFile{
		File: file,
	}, nil
}

type wrapAssetsFile struct {
	fs.File
}

func (f *wrapAssetsFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return &wrapAssetsFileInfo{
		FileInfo: info,
	}, nil
}

type wrapAssetsFileInfo struct {
	fs.FileInfo
}

func (f *wrapAssetsFileInfo) ModTime() time.Time {
	return startTime
}

type Server struct {
	httpServer *http.Server
	listener   net.Listener

	index  *controller.IndexController
	server *controller.ServerController
	xui    *controller.XUIController

	coreService    service.CoreService
	settingService service.SettingService
	inboundService service.InboundService

	// loginLimiter 面板登录失败 IP 限流器，跨请求共享内存状态
	loginLimiter *service.LoginLimiter

	cron *cron.Cron

	ctx    context.Context
	cancel context.CancelFunc
}

func NewServer() *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:          ctx,
		cancel:       cancel,
		loginLimiter: service.NewLoginLimiter(),
	}
}

// isHTTPS 根据 settingService 中的证书配置判定面板是否运行在 HTTPS 模式。
// 用于驱动 session cookie 的 Secure 属性与 HSTS 头；读取失败时保守返回 false。
func (s *Server) isHTTPS() bool {
	cert, _ := s.settingService.GetCertFile()
	key, _ := s.settingService.GetKeyFile()
	return cert != "" && key != ""
}

func (s *Server) getHtmlFiles() ([]string, error) {
	files := make([]string, 0)
	dir, _ := os.Getwd()
	err := fs.WalkDir(os.DirFS(dir), "web/html", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (s *Server) getHtmlTemplate(funcMap template.FuncMap) (*template.Template, error) {
	t := template.New("").Funcs(funcMap)
	err := fs.WalkDir(htmlFS, "html", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			newT, err := t.ParseFS(htmlFS, path+"/*.html")
			if err != nil {
				// ignore
				return nil
			}
			t = newT
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Server) initRouter() (*gin.Engine, error) {
	if config.IsDebug() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.DefaultWriter = io.Discard
		gin.DefaultErrorWriter = io.Discard
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.Use(gin.Recovery())
	// 访问日志：只记录方法/路径/状态/耗时/客户端 IP，绝不记录请求体（登录表单含密码）。
	engine.Use(middleware.AccessLog())

	if err := s.applyTrustedProxies(engine); err != nil {
		return nil, err
	}

	secret, err := s.settingService.GetSecret()
	if err != nil {
		return nil, err
	}

	basePath, err := s.settingService.GetBasePath()
	if err != nil {
		return nil, err
	}
	assetsBasePath := basePath + "assets/"

	isHTTPS := s.isHTTPS()
	engine.Use(middleware.SecurityHeaders(isHTTPS))

	// session cookie 在两种传输上都强化：
	//   HttpOnly       : 禁止 JS 访问 document.cookie，挡 XSS 偷 session
	//   SameSite=Lax   : 阻止跨站自动携带，降低 CSRF 攻击面（同时 CSRF 中间件兜底）
	//   Secure         : 仅在面板配置了 TLS 证书时开启，HTTP 模式下若开启会导致 cookie 完全不下发
	//   MaxAge 6 小时  : 控制长期留存风险；到期后需重新登录
	store := cookie.NewStore(secret)
	store.Options(sessions.Options{
		Path:     basePath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS,
		MaxAge:   6 * 60 * 60,
	})
	engine.Use(sessions.Sessions("session", store))
	// CSRF 中间件依赖 sessions 已挂载：对 GET 自动下发 token，对非幂等方法强制校验 header
	engine.Use(middleware.CSRF())
	engine.Use(func(c *gin.Context) {
		c.Set("base_path", basePath)
	})
	engine.Use(func(c *gin.Context) {
		uri := c.Request.RequestURI
		if strings.HasPrefix(uri, assetsBasePath) {
			c.Header("Cache-Control", "max-age=31536000")
		}
	})
	if err := s.initI18n(engine); err != nil {
		return nil, err
	}

	if config.IsDebug() {
		// for develop：模板从磁盘读取，改完刷新即可生效。
		files, err := s.getHtmlFiles()
		if err != nil {
			return nil, err
		}
		t, err := template.New("").Funcs(locale.PlaceholderFuncMap()).ParseFiles(files...)
		if err != nil {
			return nil, err
		}
		render.SetGlobal(render.New(t))
		engine.StaticFS(basePath+"assets", http.FS(os.DirFS("web/assets")))
	} else {
		// for prod：模板与静态资源都来自二进制内嵌 FS。
		t, err := s.getHtmlTemplate(locale.PlaceholderFuncMap())
		if err != nil {
			return nil, err
		}
		render.SetGlobal(render.New(t))
		engine.StaticFS(basePath+"assets", http.FS(&wrapAssetsFS{FS: assetsFS}))
	}

	s.registerHealthRoutes(engine, basePath)

	g := engine.Group(basePath)

	s.index = controller.NewIndexController(g, s.loginLimiter)
	s.server = controller.NewServerController(g)
	s.xui = controller.NewXUIController(g)

	return engine, nil
}

// applyTrustedProxies 配置 gin 的受信代理列表。
//
// 默认（设置为空）调用 SetTrustedProxies(nil)：gin 完全忽略 X-Forwarded-For /
// X-Real-IP，c.ClientIP() 返回 TCP 对端地址。这是登录限流不被伪造头绕过的前提。
func (s *Server) applyTrustedProxies(engine *gin.Engine) error {
	proxies, err := s.settingService.GetTrustedProxies()
	if err != nil {
		return err
	}
	if len(proxies) == 0 {
		engine.ForwardedByClientIP = false
		return engine.SetTrustedProxies(nil)
	}
	engine.ForwardedByClientIP = true
	logger.Info("trusting reverse proxies:", strings.Join(proxies, ","))
	return engine.SetTrustedProxies(proxies)
}

func (s *Server) initI18n(engine *gin.Engine) error {
	if err := locale.Init(i18nFS, "translation"); err != nil {
		return err
	}
	engine.Use(locale.Middleware())
	return nil
}

func (s *Server) startTask() {
	if err := s.coreService.RestartCore(true); err != nil {
		logger.Warning("start sing-box failed:", err)
	}
	// 每 30 秒检查一次 sing-box 是否在运行
	s.cron.AddJob("@every 30s", job.NewCheckCoreRunningJob())

	go func() {
		time.Sleep(time.Second * 5)
		// 每 10 秒统计一次流量，首次启动延迟 5 秒，与内核启动时间错开
		s.cron.AddJob("@every 10s", job.NewCoreTrafficJob())
	}()

	// 每 30 秒检查一次 inbound 流量超出和到期的情况
	s.cron.AddJob("@every 30s", job.NewCheckInboundJob())
	// 每一天提示一次流量情况,上海时间8点30
	var entry cron.EntryID
	isTgbotenabled, err := s.settingService.GetTgbotenabled()
	if (err == nil) && (isTgbotenabled) {
		runtime, err := s.settingService.GetTgbotRuntime()
		if err != nil || runtime == "" {
			logger.Errorf("Add NewStatsNotifyJob error[%s],Runtime[%s] invalid,wil run default", err, runtime)
			runtime = "@daily"
		}
		logger.Infof("Tg notify enabled,run at %s", runtime)
		entry, err = s.cron.AddJob(runtime, job.NewStatsNotifyJob())
		if err != nil {
			logger.Warning("Add NewStatsNotifyJob error", err)
			return
		}
	} else {
		s.cron.Remove(entry)
	}
}

func (s *Server) Start() (err error) {
	//这是一个匿名函数，没没有函数名
	defer func() {
		if err != nil {
			s.Stop()
		}
	}()

	loc, err := s.settingService.GetTimeLocation()
	if err != nil {
		return err
	}
	s.cron = cron.New(cron.WithLocation(loc), cron.WithSeconds())
	s.cron.Start()

	engine, err := s.initRouter()
	if err != nil {
		return err
	}

	certFile, err := s.settingService.GetCertFile()
	if err != nil {
		return err
	}
	keyFile, err := s.settingService.GetKeyFile()
	if err != nil {
		return err
	}
	listen, err := s.settingService.GetListen()
	if err != nil {
		return err
	}
	port, err := s.settingService.GetPort()
	if err != nil {
		return err
	}
	listenAddr := net.JoinHostPort(listen, strconv.Itoa(port))
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	if certFile != "" || keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			listener.Close()
			return err
		}
		// 明确下限 TLS 1.2：Go 默认服务端下限仍接受 TLS 1.0/1.1，不满足现行合规要求。
		c := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		listener = network.NewAutoHttpsListener(listener)
		listener = tls.NewListener(listener, c)
	}

	if certFile != "" || keyFile != "" {
		logger.Info("web server run https on", listener.Addr())
	} else {
		logger.Info("web server run http on", listener.Addr())
	}
	s.listener = listener

	s.startTask()

	s.httpServer = &http.Server{
		Handler:           engine,
		ReadHeaderTimeout: 20 * time.Second,
	}

	go func() {
		s.httpServer.Serve(listener)
	}()

	return nil
}

// Stop 按「先子进程、再定时任务、最后 HTTP」的顺序停机。
//
// 关键修正：Shutdown 必须使用独立的、带超时的 context。
// 历史实现先 s.cancel() 再把已取消的 s.ctx 传给 Shutdown，
// 等价于立即强制关闭，正在处理的请求会被直接掐断，"优雅"二字名存实亡。
// 现在改为：排空请求（最多 shutdownTimeout）之后才 cancel 服务级 context。
func (s *Server) Stop() error {
	_ = s.coreService.StopCore()
	if s.cron != nil {
		s.cron.Stop()
	}

	var err1 error
	var err2 error
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		err1 = s.httpServer.Shutdown(ctx)
		cancel()
	}
	if s.listener != nil {
		err2 = s.listener.Close()
		// Shutdown 成功时监听套接字已被关闭，这里的二次 Close 必然返回
		// "use of closed network connection"，不是真实故障，忽略之。
		if err2 != nil && errors.Is(err2, net.ErrClosed) {
			err2 = nil
		}
	}

	s.cancel()
	return common.Combine(err1, err2)
}

func (s *Server) GetCtx() context.Context {
	return s.ctx
}

func (s *Server) GetCron() *cron.Cron {
	return s.cron
}
