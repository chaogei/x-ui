package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestGlassThemeStaysTranslucent 锁住磨砂面板的不变量。
//
// UI 打磨很容易把卡片改成实心底、拿掉 backdrop-filter，或者把 aurora 底图
// 换成每页各自的不透明色。这些回归肉眼在 CI 里看不见，所以对着源码钉死。
func TestGlassThemeStaysTranslucent(t *testing.T) {
	css := readRepoFile(t, filepath.Join("frontend", "src", "style.css"))
	theme := readRepoFile(t, filepath.Join("frontend", "src", "theme.ts"))

	for _, needle := range []string{
		".xui-glass {",
		"backdrop-filter: var(--xui-glass-blur)",
		"-webkit-backdrop-filter: var(--xui-glass-blur)",
		".ant-card {",
		"prefers-reduced-transparency",
		"prefers-reduced-motion",
		"@supports not ((-webkit-backdrop-filter: blur(1px)) or (backdrop-filter: blur(1px)))",
		"@keyframes xui-drift",
		"body::before",
		"radial-gradient",
	} {
		if !strings.Contains(css, needle) {
			t.Errorf("style.css no longer contains %q — the frosted glass contract drifted", needle)
		}
	}

	if !strings.Contains(css, "animation: none") {
		t.Error("prefers-reduced-motion must still cancel the aurora drift animation")
	}

	alpha := glassBgAlpha(t, css)
	if alpha <= 0 || alpha > 0.2 {
		t.Errorf("--xui-glass-bg alpha = %v, want (0, 0.2] so the panel stays translucent", alpha)
	}

	for _, opaque := range []string{
		`colorBgContainer: '#fff'`,
		`colorBgContainer: "#fff"`,
		`colorBgContainer: '#ffffff'`,
		`colorBgContainer: "#ffffff"`,
		`colorBgContainer: 'rgb(255, 255, 255)'`,
		`colorBgContainer: "rgb(255, 255, 255)"`,
		`colorBgContainer: 'white'`,
	} {
		if strings.Contains(theme, opaque) {
			t.Errorf("theme.ts pins an opaque white container: %s", opaque)
		}
	}

	if !strings.Contains(theme, "GLASS_FILL") || !strings.Contains(theme, "rgba(") {
		t.Error("theme.ts must keep translucent GLASS_FILL tokens")
	}
}

var glassBgRe = regexp.MustCompile(`--xui-glass-bg:\s*rgba\(\s*\d+\s*,\s*\d+\s*,\s*\d+\s*,\s*([0-9.]+)\s*\)`)

func glassBgAlpha(t *testing.T, css string) float64 {
	t.Helper()
	m := glassBgRe.FindStringSubmatch(css)
	if len(m) != 2 {
		t.Fatal("--xui-glass-bg is missing or not an rgba() color")
	}
	alpha, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("parse --xui-glass-bg alpha %q: %v", m[1], err)
	}
	return alpha
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}
