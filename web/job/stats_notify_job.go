package job

import (
	"fmt"
	"net"
	"os"

	"time"

	"x-ui/logger"
	"x-ui/util/common"
	"x-ui/web/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type LoginStatus byte

const (
	LoginSuccess LoginStatus = 1
	LoginFail    LoginStatus = 0
)

type StatsNotifyJob struct {
	coreService    service.CoreService
	inboundService service.InboundService
	settingService service.SettingService
}

func NewStatsNotifyJob() *StatsNotifyJob {
	return new(StatsNotifyJob)
}

// SendMsgToTgbot 同步发送一条 Telegram 通知。
//
// 首要约定：只有在设置里显式开启 tgBotEnable 时才会构造 bot 客户端。
// 关闭状态下直接返回，绝不发起任何网络请求。
func (j *StatsNotifyJob) SendMsgToTgbot(msg string) {
	enabled, err := j.settingService.GetTgbotenabled()
	if err != nil {
		logTgError("read tgBotEnable failed", err)
		return
	}
	if !enabled {
		return
	}

	tgBottoken, err := j.settingService.GetTgBotToken()
	if err != nil {
		logTgError("read tgBotToken failed", err)
		return
	}
	if tgBottoken == "" {
		logger.Warning("telegram: bot enabled but token is empty, skip notify")
		return
	}
	tgBotid, err := j.settingService.GetTgBotChatId()
	if err != nil {
		logTgError("read tgBotChatId failed", err)
		return
	}

	bot, err := getBot(tgBottoken)
	if err != nil {
		logTgError("init bot failed", err)
		return
	}
	if _, err := bot.Send(tgbotapi.NewMessage(int64(tgBotid), msg)); err != nil {
		logTgError("send message failed", err)
	}
}

// SendMsgToTgbotAsync 把通知交给有界队列，由固定的工作 goroutine 投递。
//
// 登录请求的 HTTP 路径绝不能因为 Telegram 不可达而阻塞：即便 bot.Send
// 卡到超时（客户端已设 10s Timeout），用户看到的登录响应也不受影响。
// 队列满时这条通知会被丢弃，见 notifyQueue 的说明。
func (j *StatsNotifyJob) SendMsgToTgbotAsync(msg string) {
	notifier.submit(func() { j.SendMsgToTgbot(msg) })
}

// Here run is a interface method of Job interface
func (j *StatsNotifyJob) Run() {
	if !j.coreService.IsCoreRunning() {
		return
	}
	var info string
	//get hostname
	name, err := os.Hostname()
	if err != nil {
		logTgError("get hostname failed", err)
		return
	}
	info = fmt.Sprintf("主机名称:%s\r\n", name)
	//get ip address
	var ip string
	netInterfaces, err := net.Interfaces()
	if err != nil {
		logTgError("list network interfaces failed", err)
		return
	}

	for i := 0; i < len(netInterfaces); i++ {
		if (netInterfaces[i].Flags & net.FlagUp) != 0 {
			addrs, _ := netInterfaces[i].Addrs()

			for _, address := range addrs {
				if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						ip = ipnet.IP.String()
						break
					} else {
						ip = ipnet.IP.String()
						break
					}
				}
			}
		}
	}
	info += fmt.Sprintf("IP地址:%s\r\n \r\n", ip)

	//get traffic
	inbouds, err := j.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("StatsNotifyJob run failed:", err)
		return
	}
	//NOTE:If there no any sessions here,need to notify here
	//TODO:分节点推送,自动转化格式
	for _, inbound := range inbouds {
		info += fmt.Sprintf("节点名称:%s\r\n端口:%d\r\n上行流量↑:%s\r\n下行流量↓:%s\r\n总流量:%s\r\n", inbound.Remark, inbound.Port, common.FormatTraffic(inbound.Up), common.FormatTraffic(inbound.Down), common.FormatTraffic((inbound.Up + inbound.Down)))
		if inbound.ExpiryTime == 0 {
			info += fmt.Sprintf("到期时间:无限期\r\n \r\n")
		} else {
			info += fmt.Sprintf("到期时间:%s\r\n \r\n", time.Unix((inbound.ExpiryTime/1000), 0).Format("2006-01-02 15:04:05"))
		}
	}
	j.SendMsgToTgbot(info)
}

// UserLoginNotify 推送面板登录提醒。
//
// 在做任何工作（含取主机名）之前先检查 tgBotEnable：登录是热路径，
// 未启用 Telegram 的部署不应为此付出任何代价。
func (j *StatsNotifyJob) UserLoginNotify(username string, ip string, time string, status LoginStatus) {
	if enabled, err := j.settingService.GetTgbotenabled(); err != nil || !enabled {
		return
	}
	if username == "" || ip == "" || time == "" {
		logger.Warning("UserLoginNotify failed,invalid info")
		return
	}
	var msg string
	//get hostname
	name, err := os.Hostname()
	if err != nil {
		logTgError("get hostname failed", err)
		return
	}
	if status == LoginSuccess {
		msg = fmt.Sprintf("面板登录成功提醒\r\n主机名称:%s\r\n", name)
	} else if status == LoginFail {
		msg = fmt.Sprintf("面板登录失败提醒\r\n主机名称:%s\r\n", name)
	}
	msg += fmt.Sprintf("时间:%s\r\n", time)
	msg += fmt.Sprintf("用户:%s\r\n", username)
	msg += fmt.Sprintf("IP:%s\r\n", ip)
	j.SendMsgToTgbotAsync(msg)
}
