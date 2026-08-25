package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"x-ui/config"
	"x-ui/database"
	"x-ui/logger"
	"x-ui/web"
	"x-ui/web/global"
	"x-ui/web/service"

	"github.com/op/go-logging"
)

func runWebServer() {
	log.Printf("%v %v", config.GetName(), config.GetVersion())

	switch config.GetLogLevel() {
	case config.Debug:
		logger.InitLogger(logging.DEBUG)
	case config.Info:
		logger.InitLogger(logging.INFO)
	case config.Warn:
		logger.InitLogger(logging.WARNING)
	case config.Error:
		logger.InitLogger(logging.ERROR)
	default:
		log.Fatal("unknown log level:", config.GetLogLevel())
	}

	err := database.InitDB(config.GetDBPath())
	if err != nil {
		log.Fatal(err)
	}

	var server *web.Server

	server = web.NewServer()
	global.SetWebServer(server)
	err = server.Start()
	if err != nil {
		log.Println(err)
		return
	}

	// 信号捕获：SIGHUP 走热重启、SIGINT/SIGTERM 走优雅停机。
	// 注意 SIGKILL 和 SIGSTOP 在 POSIX 下无法被进程捕获，这里不再注册。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	for {
		sig := <-sigCh

		switch sig {
		case syscall.SIGHUP:
			err := server.Stop()
			if err != nil {
				logger.Warning("stop server err:", err)
			}
			server = web.NewServer()
			global.SetWebServer(server)
			err = server.Start()
			if err != nil {
				log.Println(err)
				return
			}
		default:
			// SIGINT / SIGTERM：停机前必须关闭 sing-box 子进程（Server.Stop 内部链式调用）。
			if err := server.Stop(); err != nil {
				logger.Warning("stop server err:", err)
			}
			return
		}
	}
}

func resetSetting() {
	err := database.InitDB(config.GetDBPath())
	if err != nil {
		fmt.Println(err)
		return
	}

	settingService := service.SettingService{}
	err = settingService.ResetSettings()
	if err != nil {
		fmt.Println("reset setting failed:", err)
	} else {
		fmt.Println("reset setting success")
	}
}

// showSetting 打印当前面板设置。
//
// 密码字段永远不会被打印：数据库里存的是 bcrypt 哈希，把它当"密码"回显既
// 无法用于登录，又等于把可离线爆破的哈希输出到终端与运维日志里。
// 用户忘记密码时的正确路径是 `x-ui setting -username X -password Y` 重设。
func showSetting(show bool) {
	if !show {
		return
	}
	settingService := service.SettingService{}
	port, err := settingService.GetPort()
	if err != nil {
		fmt.Println("get current port failed, error info:", err)
	}
	userService := service.UserService{}
	userModel, err := userService.GetFirstUser()
	if err != nil {
		fmt.Println("get current user info failed, error info:", err)
		return
	}
	if userModel.Username == "" || userModel.Password == "" {
		fmt.Println("current username or password is empty")
	}
	fmt.Println("current panel settings as follows:")
	fmt.Println("username:", userModel.Username)
	fmt.Println("password: ******** (bcrypt hash, not recoverable)")
	fmt.Println("port:", port)
}

// updateTgbotEnableSts 开关 Telegram 通知，由 `x-ui setting -enabletgbot` 驱动。
func updateTgbotEnableSts(status bool) {
	settingService := service.SettingService{}
	currentTgSts, err := settingService.GetTgbotenabled()
	if err != nil {
		fmt.Println(err)
		return
	}
	logger.Infof("current enabletgbot status[%v],need update to status[%v]", currentTgSts, status)
	if currentTgSts == status {
		return
	}
	if err := settingService.SetTgbotenabled(status); err != nil {
		fmt.Println(err)
		return
	}
	logger.Infof("SetTgbotenabled[%v] success", status)
}

func updateTgbotSetting(tgBotToken string, tgBotChatid int, tgBotRuntime string) {
	err := database.InitDB(config.GetDBPath())
	if err != nil {
		fmt.Println(err)
		return
	}

	settingService := service.SettingService{}

	if tgBotToken != "" {
		err := settingService.SetTgBotToken(tgBotToken)
		if err != nil {
			fmt.Println(err)
			return
		} else {
			logger.Info("updateTgbotSetting tgBotToken success")
		}
	}

	if tgBotRuntime != "" {
		err := settingService.SetTgbotRuntime(tgBotRuntime)
		if err != nil {
			fmt.Println(err)
			return
		} else {
			logger.Infof("updateTgbotSetting tgBotRuntime[%s] success", tgBotRuntime)
		}
	}

	if tgBotChatid != 0 {
		err := settingService.SetTgBotChatId(tgBotChatid)
		if err != nil {
			fmt.Println(err)
			return
		} else {
			logger.Info("updateTgbotSetting tgBotChatid success")
		}
	}
}

func updateSetting(port int, username string, password string) {
	err := database.InitDB(config.GetDBPath())
	if err != nil {
		fmt.Println(err)
		return
	}

	settingService := service.SettingService{}

	if port > 0 {
		err := settingService.SetPort(port)
		if err != nil {
			fmt.Println("set port failed:", err)
		} else {
			fmt.Printf("set port %v success", port)
		}
	}
	if username != "" || password != "" {
		userService := service.UserService{}
		err := userService.UpdateFirstUser(username, password)
		if err != nil {
			fmt.Println("set username and password failed:", err)
		} else {
			fmt.Println("set username and password success")
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		runWebServer()
		return
	}

	var showVersion bool
	flag.BoolVar(&showVersion, "v", false, "show version")

	runCmd := flag.NewFlagSet("run", flag.ExitOnError)

	settingCmd := flag.NewFlagSet("setting", flag.ExitOnError)
	var port int
	var username string
	var password string
	var tgbottoken string
	var tgbotchatid int
	var enabletgbot bool
	var tgbotRuntime string
	var reset bool
	var show bool
	settingCmd.BoolVar(&reset, "reset", false, "reset all settings")
	settingCmd.BoolVar(&show, "show", false, "show current settings")
	settingCmd.IntVar(&port, "port", 0, "set panel port")
	settingCmd.StringVar(&username, "username", "", "set login username")
	settingCmd.StringVar(&password, "password", "", "set login password")
	settingCmd.StringVar(&tgbottoken, "tgbottoken", "", "set telegrame bot token")
	settingCmd.StringVar(&tgbotRuntime, "tgbotRuntime", "", "set telegrame bot cron time")
	settingCmd.IntVar(&tgbotchatid, "tgbotchatid", 0, "set telegrame bot chat id")
	settingCmd.BoolVar(&enabletgbot, "enabletgbot", false, "enable telegram bot notify")

	oldUsage := flag.Usage
	flag.Usage = func() {
		oldUsage()
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("    run            run web panel")
		fmt.Println("    setting        set settings")
	}

	flag.Parse()
	if showVersion {
		fmt.Println(config.GetVersion())
		return
	}

	switch os.Args[1] {
	case "run":
		err := runCmd.Parse(os.Args[2:])
		if err != nil {
			fmt.Println(err)
			return
		}
		runWebServer()
	case "setting":
		err := settingCmd.Parse(os.Args[2:])
		if err != nil {
			fmt.Println(err)
			return
		}
		if reset {
			resetSetting()
		} else {
			updateSetting(port, username, password)
		}
		if show {
			showSetting(show)
		}
		if (tgbottoken != "") || (tgbotchatid != 0) || (tgbotRuntime != "") {
			updateTgbotSetting(tgbottoken, tgbotchatid, tgbotRuntime)
		}
		// -enabletgbot 之前只被解析、从不生效；现在真正落库。
		if enabletgbot {
			updateTgbotEnableSts(true)
		}
	default:
		fmt.Println("except 'run' or 'setting' subcommands")
		fmt.Println()
		runCmd.Usage()
		fmt.Println()
		settingCmd.Usage()
	}
}
