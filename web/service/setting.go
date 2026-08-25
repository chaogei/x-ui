package service

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"x-ui/core/singbox"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/logger"
	"x-ui/util/common"
	"x-ui/util/random"
	"x-ui/util/reflect_util"
	"x-ui/web/entity"
)

// coreTemplateConfig 是全新安装时写入 settings 的 sing-box 配置模板。
//
// 这里直接引用 core/singbox 的常量，而不是再嵌一份 config.json：
// 两份内容曾经逐字节相同，改一处忘另一处时，新装面板拿到的模板会与
// 内核代码的假设（例如 v2ray_api 的监听端口）悄悄对不上。
var coreTemplateConfig = singbox.DefaultTemplate

// secretKey 是存放 session cookie store 密钥的 settings 键。
// 它不属于 defaultValueMap：默认值必须是 CSPRNG 产物且只生成一次，
// 参见 GetSecret 的惰性生成 + 持久化逻辑。
const secretKey = "secret"

// secretBytes 是 session cookie 密钥的字节长度。
const secretBytes = 32

var defaultValueMap = map[string]string{
	"coreTemplateConfig": coreTemplateConfig,
	"webListen":          "",
	"webPort":            "54321",
	"webCertFile":        "",
	"webKeyFile":         "",
	"webBasePath":        "/",
	"webTrustedProxies":  "",
	"timeLocation":       "Asia/Shanghai",
	"tgBotEnable":        "false",
	"tgBotToken":         "",
	"tgBotChatId":        "0",
	"tgRunTime":          "",
}

// secretMu 串行化 GetSecret 的「读-生成-写」竞态：
// 多个请求同时首次访问时，只允许一个生成并落库。
var secretMu sync.Mutex

type SettingService struct {
}

func (s *SettingService) GetAllSetting() (*entity.AllSetting, error) {
	db := database.GetDB()
	settings := make([]*model.Setting, 0)
	err := db.Model(model.Setting{}).Find(&settings).Error
	if err != nil {
		return nil, err
	}
	allSetting := &entity.AllSetting{}
	t := reflect.TypeOf(allSetting).Elem()
	v := reflect.ValueOf(allSetting).Elem()
	fields := reflect_util.GetFields(t)

	setSetting := func(key, value string) (err error) {
		defer func() {
			panicErr := recover()
			if panicErr != nil {
				err = errors.New(fmt.Sprint(panicErr))
			}
		}()

		var found bool
		var field reflect.StructField
		for _, f := range fields {
			if f.Tag.Get("json") == key {
				field = f
				found = true
				break
			}
		}

		if !found {
			// 有些设置自动生成，不需要返回到前端给用户修改
			return nil
		}

		fieldV := v.FieldByName(field.Name)
		switch t := fieldV.Interface().(type) {
		case int:
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			fieldV.SetInt(n)
		case string:
			fieldV.SetString(value)
		case bool:
			fieldV.SetBool(value == "true")
		default:
			return common.NewErrorf("unknown field %v type %v", key, t)
		}
		return
	}

	keyMap := map[string]bool{}
	for _, setting := range settings {
		err := setSetting(setting.Key, setting.Value)
		if err != nil {
			return nil, err
		}
		keyMap[setting.Key] = true
	}

	for key, value := range defaultValueMap {
		if keyMap[key] {
			continue
		}
		err := setSetting(key, value)
		if err != nil {
			return nil, err
		}
	}

	return allSetting, nil
}

func (s *SettingService) ResetSettings() error {
	db := database.GetDB()
	return db.Where("1 = 1").Delete(model.Setting{}).Error
}

func (s *SettingService) getSetting(key string) (*model.Setting, error) {
	db := database.GetDB()
	setting := &model.Setting{}
	err := db.Model(model.Setting{}).Where("key = ?", key).First(setting).Error
	if err != nil {
		return nil, err
	}
	return setting, nil
}

func (s *SettingService) saveSetting(key string, value string) error {
	setting, err := s.getSetting(key)
	db := database.GetDB()
	if database.IsNotFound(err) {
		return db.Create(&model.Setting{
			Key:   key,
			Value: value,
		}).Error
	} else if err != nil {
		return err
	}
	setting.Key = key
	setting.Value = value
	return db.Save(setting).Error
}

func (s *SettingService) getString(key string) (string, error) {
	setting, err := s.getSetting(key)
	if database.IsNotFound(err) {
		value, ok := defaultValueMap[key]
		if !ok {
			return "", common.NewErrorf("key <%v> not in defaultValueMap", key)
		}
		return value, nil
	} else if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (s *SettingService) setString(key string, value string) error {
	return s.saveSetting(key, value)
}

func (s *SettingService) getBool(key string) (bool, error) {
	str, err := s.getString(key)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(str)
}

func (s *SettingService) setBool(key string, value bool) error {
	return s.setString(key, strconv.FormatBool(value))
}

func (s *SettingService) getInt(key string) (int, error) {
	str, err := s.getString(key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(str)
}

func (s *SettingService) setInt(key string, value int) error {
	return s.setString(key, strconv.Itoa(value))
}

// GetCoreTemplateConfig 返回面板设置中保存的 sing-box 配置模板 JSON。
func (s *SettingService) GetCoreTemplateConfig() (string, error) {
	return s.getString("coreTemplateConfig")
}

func (s *SettingService) GetListen() (string, error) {
	return s.getString("webListen")
}

func (s *SettingService) GetTgBotToken() (string, error) {
	return s.getString("tgBotToken")
}

func (s *SettingService) SetTgBotToken(token string) error {
	return s.setString("tgBotToken", token)
}

func (s *SettingService) GetTgBotChatId() (int, error) {
	return s.getInt("tgBotChatId")
}

func (s *SettingService) SetTgBotChatId(chatId int) error {
	return s.setInt("tgBotChatId", chatId)
}

func (s *SettingService) SetTgbotenabled(value bool) error {
	return s.setBool("tgBotEnable", value)
}

func (s *SettingService) GetTgbotenabled() (bool, error) {
	return s.getBool("tgBotEnable")
}

func (s *SettingService) SetTgbotRuntime(time string) error {
	return s.setString("tgRunTime", time)
}

func (s *SettingService) GetTgbotRuntime() (string, error) {
	return s.getString("tgRunTime")
}

func (s *SettingService) GetPort() (int, error) {
	return s.getInt("webPort")
}

func (s *SettingService) SetPort(port int) error {
	return s.setInt("webPort", port)
}

func (s *SettingService) GetCertFile() (string, error) {
	return s.getString("webCertFile")
}

func (s *SettingService) GetKeyFile() (string, error) {
	return s.getString("webKeyFile")
}

// GetSecret 返回 session cookie store 的密钥。
//
// 首次调用时用 crypto/rand 生成 32 字节并持久化到 settings 表；
// 之后固定复用该值，保证面板重启后已登录会话仍然有效。
// 绝不会在 package init 阶段用弱随机源预生成。
func (s *SettingService) GetSecret() ([]byte, error) {
	secretMu.Lock()
	defer secretMu.Unlock()

	setting, err := s.getSetting(secretKey)
	if err == nil && setting.Value != "" {
		return []byte(setting.Value), nil
	}
	if err != nil && !database.IsNotFound(err) {
		return nil, err
	}

	generated, err := random.SecretString(secretBytes)
	if err != nil {
		return nil, err
	}
	if err := s.saveSetting(secretKey, generated); err != nil {
		return nil, err
	}
	logger.Info("generated a new session secret")
	return []byte(generated), nil
}

// GetTrustedProxies 返回被信任的反向代理 CIDR 列表。
//
// 默认为空：此时 gin 完全不信任 X-Forwarded-For / X-Real-IP，
// c.ClientIP() 退化为 TCP 对端地址，登录限流无法被伪造头绕过。
// 只有运维显式配置了前置代理网段，XFF 才会被采信。
func (s *SettingService) GetTrustedProxies() ([]string, error) {
	raw, err := s.getString("webTrustedProxies")
	if err != nil {
		return nil, err
	}
	return ParseTrustedProxies(raw), nil
}

// ParseTrustedProxies 把逗号/空白分隔的 CIDR 列表规整为切片，忽略空项。
func ParseTrustedProxies(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func (s *SettingService) GetBasePath() (string, error) {
	basePath, err := s.getString("webBasePath")
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}
	return basePath, nil
}

func (s *SettingService) GetTimeLocation() (*time.Location, error) {
	l, err := s.getString("timeLocation")
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(l)
	if err != nil {
		defaultLocation := defaultValueMap["timeLocation"]
		logger.Errorf("location <%v> not exist, using default location: %v", l, defaultLocation)
		return time.LoadLocation(defaultLocation)
	}
	return location, nil
}

func (s *SettingService) UpdateAllSetting(allSetting *entity.AllSetting) error {
	if err := allSetting.CheckValid(); err != nil {
		return err
	}

	v := reflect.ValueOf(allSetting).Elem()
	t := reflect.TypeOf(allSetting).Elem()
	fields := reflect_util.GetFields(t)
	errs := make([]error, 0)
	for _, field := range fields {
		key := field.Tag.Get("json")
		fieldV := v.FieldByName(field.Name)
		value := fmt.Sprint(fieldV.Interface())
		err := s.saveSetting(key, value)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return common.Combine(errs...)
}
