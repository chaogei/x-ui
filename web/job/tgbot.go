package job

import (
	"net/http"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"x-ui/config"
	"x-ui/logger"
)

// tgbot 缓存已经建立的 Telegram Bot 客户端。
//
// tgbotapi.NewBotAPI 内部会同步发起一次 getMe HTTP 请求。历史实现每次发通知都
// 重新构造，等价于每次登录都往 api.telegram.org 打一个阻塞请求：
// 在 Telegram 不可达的网络环境下，登录接口会被拖到 TCP 超时才返回。
var tgbot = struct {
	mu    sync.Mutex
	api   *tgbotapi.BotAPI
	token string
}{}

// tgSendTimeout 单条通知的发送上限，防止 Telegram 侧卡住占用 goroutine。
const tgSendTimeout = 10 * time.Second

// getBot 返回与 token 对应的 bot 客户端，token 变化时重建。
func getBot(token string) (*tgbotapi.BotAPI, error) {
	tgbot.mu.Lock()
	defer tgbot.mu.Unlock()

	if tgbot.api != nil && tgbot.token == token {
		return tgbot.api, nil
	}
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	// Debug 会把每次 API 请求/响应原样打印（含 chat 内容），只在调试模式开启。
	api.Debug = config.IsDebug()
	api.Client = &http.Client{Timeout: tgSendTimeout}
	tgbot.api = api
	tgbot.token = token
	return api, nil
}

// resetBotCache 清空缓存，供测试与 token 变更后强制重建。
func resetBotCache() {
	tgbot.mu.Lock()
	tgbot.api = nil
	tgbot.token = ""
	tgbot.mu.Unlock()
}

// logTgError 统一记录 Telegram 相关失败，避免各处 fmt.Println 到 stdout。
func logTgError(msg string, err error) {
	logger.Warningf("telegram: %s: %v", msg, err)
}
