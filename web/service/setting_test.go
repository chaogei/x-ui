package service

import (
	"encoding/base64"
	"reflect"
	"sync"
	"testing"

	"x-ui/testutil"
)

// TestGetSecretIsGeneratedOnceAndPersisted 覆盖 C-1 的两个要点：
// 密钥必须在首次使用时才生成（不是包初始化时的弱随机），且必须持久化，
// 否则面板每次重启都会让所有已登录会话失效。
func TestGetSecretIsGeneratedOnceAndPersisted(t *testing.T) {
	testutil.InitDB(t)
	s := &SettingService{}

	first, err := s.GetSecret()
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	second, err := s.GetSecret()
	if err != nil {
		t.Fatalf("get secret again: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("secret changed between calls: %q then %q", first, second)
	}

	stored, err := s.getSetting(secretKey)
	if err != nil {
		t.Fatalf("secret was not persisted: %v", err)
	}
	if stored.Value != string(first) {
		t.Errorf("stored secret = %q, want %q", stored.Value, first)
	}
}

// TestGetSecretHasFullEntropy 确认密钥确实是 32 字节 CSPRNG 输出，
// 而不是旧实现里 math/rand + UnixNano 那种可按启动时间穷举的串。
func TestGetSecretHasFullEntropy(t *testing.T) {
	testutil.InitDB(t)
	s := &SettingService{}

	secret, err := s.GetSecret()
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(string(secret))
	if err != nil {
		t.Fatalf("secret %q is not raw-url base64: %v", secret, err)
	}
	if len(raw) != secretBytes {
		t.Errorf("secret decodes to %d bytes, want %d", len(raw), secretBytes)
	}
}

// TestGetSecretIsNotInDefaultValueMap 防止有人"顺手"把 secret 加回默认值表——
// 那等于给所有部署发同一把 cookie 签名密钥。
func TestGetSecretIsNotInDefaultValueMap(t *testing.T) {
	if _, ok := defaultValueMap[secretKey]; ok {
		t.Fatal("secret must never have a compile-time default value")
	}
}

// TestGetSecretConcurrent 覆盖多个请求同时首次访问的竞态：
// 必须只生成一把密钥，否则先拿到旧值的会话会在写库后立刻失效。
func TestGetSecretConcurrent(t *testing.T) {
	testutil.InitDB(t)
	s := &SettingService{}

	const goroutines = 16
	results := make([]string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			secret, err := s.GetSecret()
			if err != nil {
				t.Errorf("get secret: %v", err)
				return
			}
			results[i] = string(secret)
		}(i)
	}
	wg.Wait()

	for i, got := range results {
		if got != results[0] {
			t.Fatalf("goroutine %d observed secret %q, want %q — the secret was generated twice", i, got, results[0])
		}
	}
}

// TestSettingRoundTripThroughReflection 覆盖 AllSetting 的反射读写：
// 每个字段写进 settings 表后必须能原样读回来，包括 int 与 bool 的转换。
func TestSettingRoundTripThroughReflection(t *testing.T) {
	testutil.InitDB(t)
	s := &SettingService{}

	original, err := s.GetAllSetting()
	if err != nil {
		t.Fatalf("read default settings: %v", err)
	}

	original.WebListen = "127.0.0.1"
	original.WebPort = 8443
	original.WebBasePath = "/panel/"
	original.WebTrustedProxies = "10.0.0.0/8, 192.168.1.1"
	original.TgBotEnable = true
	original.TgBotToken = "123456:token"
	original.TgBotChatId = 987654
	original.TgRunTime = "@daily"
	original.TimeLocation = "UTC"

	if err := s.UpdateAllSetting(original); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	reloaded, err := s.GetAllSetting()
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	if !reflect.DeepEqual(original, reloaded) {
		t.Errorf("settings did not survive the round trip:\n got %+v\nwant %+v", reloaded, original)
	}
}

// TestUpdateAllSettingRejectsInvalid 确认写入前会跑 CheckValid，
// 否则一个打错的时区就能让面板下次启动崩在 LoadLocation 上。
func TestUpdateAllSettingRejectsInvalid(t *testing.T) {
	testutil.InitDB(t)
	s := &SettingService{}

	base, err := s.GetAllSetting()
	if err != nil {
		t.Fatalf("read default settings: %v", err)
	}

	cases := map[string]func(){
		"bad timezone":      func() { base.TimeLocation = "Mars/Olympus_Mons" },
		"port out of range": func() { base.WebPort = 70000 },
		"port zero":         func() { base.WebPort = 0 },
		"listen not an ip":  func() { base.WebListen = "not-an-ip" },
		"bad trusted proxy": func() { base.WebTrustedProxies = "10.0.0.0/8, not-a-cidr" },
		"bad core template": func() { base.CoreTemplateConfig = "{" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fresh, err := s.GetAllSetting()
			if err != nil {
				t.Fatalf("read settings: %v", err)
			}
			base = fresh
			mutate()
			if err := s.UpdateAllSetting(base); err == nil {
				t.Fatal("invalid settings must be rejected")
			}
		})
	}

	// 被拒绝的写入不得留下任何痕迹。
	after, err := s.GetAllSetting()
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	if after.TimeLocation != defaultValueMap["timeLocation"] {
		t.Errorf("timeLocation = %q, want the untouched default %q", after.TimeLocation, defaultValueMap["timeLocation"])
	}
}

func TestGetBasePathIsAlwaysBracketedBySlashes(t *testing.T) {
	testutil.InitDB(t)
	s := &SettingService{}

	cases := map[string]string{
		"/":           "/",
		"panel":       "/panel/",
		"/panel":      "/panel/",
		"panel/":      "/panel/",
		"/panel/":     "/panel/",
		"deep/nested": "/deep/nested/",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			if err := s.setString("webBasePath", input); err != nil {
				t.Fatalf("save base path: %v", err)
			}
			got, err := s.GetBasePath()
			if err != nil {
				t.Fatalf("get base path: %v", err)
			}
			if got != want {
				t.Errorf("GetBasePath(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

// TestGetTrustedProxiesDefaultsToNone 是 H-1 的默认姿态：
// 没配置代理时必须返回空列表，gin 才会完全无视 X-Forwarded-For。
func TestGetTrustedProxiesDefaultsToNone(t *testing.T) {
	testutil.InitDB(t)
	s := &SettingService{}

	proxies, err := s.GetTrustedProxies()
	if err != nil {
		t.Fatalf("get trusted proxies: %v", err)
	}
	if len(proxies) != 0 {
		t.Errorf("trusted proxies = %v, want none by default", proxies)
	}
}

func TestGetTimeLocationFallsBackToDefault(t *testing.T) {
	testutil.InitDB(t)
	s := &SettingService{}

	if err := s.setString("timeLocation", "Mars/Olympus_Mons"); err != nil {
		t.Fatalf("save time location: %v", err)
	}
	loc, err := s.GetTimeLocation()
	if err != nil {
		t.Fatalf("get time location: %v", err)
	}
	if loc.String() != defaultValueMap["timeLocation"] {
		t.Errorf("location = %q, want fallback to %q", loc, defaultValueMap["timeLocation"])
	}
}
