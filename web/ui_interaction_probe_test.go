package web

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestUIInteractionContracts 用源码探针钉住键盘/空态/窄屏交互，
// 避免下一次重构把操作列改回不可聚焦的 <a>、或拿掉 skip-link。
func TestUIInteractionContracts(t *testing.T) {
	login := readRepoFile(t, filepath.Join("frontend", "src", "views", "LoginView.vue"))
	shell := readRepoFile(t, filepath.Join("frontend", "src", "components", "AppShell.vue"))
	status := readRepoFile(t, filepath.Join("frontend", "src", "views", "StatusView.vue"))
	inbounds := readRepoFile(t, filepath.Join("frontend", "src", "views", "InboundsView.vue"))
	settings := readRepoFile(t, filepath.Join("frontend", "src", "views", "SettingView.vue"))
	css := readRepoFile(t, filepath.Join("frontend", "src", "style.css"))

	for _, needle := range []string{
		`class="xui-login"`,
		`autocomplete="username"`,
		`autocomplete="current-password"`,
		`autocomplete="one-time-code"`,
		`role="alert"`,
		`class="xui-link-btn"`,
		`:disabled="!canSubmit"`,
	} {
		if !strings.Contains(login, needle) {
			t.Errorf("LoginView.vue missing %q", needle)
		}
	}
	if !strings.Contains(login, `t('login_totp_hint')`) {
		t.Error("login is missing the two-factor hint control")
	}

	for _, needle := range []string{
		`class="xui-skip"`,
		`aria-expanded`,
		`aria-controls`,
		`Escape`,
		`class="xui-scrim"`,
		`role="navigation"`,
		`role="banner"`,
	} {
		if !strings.Contains(shell, needle) {
			t.Errorf("AppShell.vue missing %q", needle)
		}
	}

	for _, needle := range []string{
		`hydrated`,
		`aria-live="polite"`,
		`role="alert"`,
		`confirm_install_core`,
	} {
		if !strings.Contains(status, needle) {
			t.Errorf("StatusView.vue missing %q", needle)
		}
	}

	for _, needle := range []string{
		`max-width: 768px`,
		`class="xui-link-btn"`,
		`inbound_filter_ph`,
		`action_retry`,
		`loadError`,
	} {
		if !strings.Contains(inbounds, needle) {
			t.Errorf("InboundsView.vue missing %q", needle)
		}
	}
	if strings.Contains(inbounds, `<a @click`) {
		t.Error("inbounds row actions must not regress to href-less <a @click>")
	}

	for _, needle := range []string{
		`unsaved_changes`,
		`restarting_panel`,
		`action_retry`,
	} {
		if !strings.Contains(settings, needle) {
			t.Errorf("SettingView.vue missing %q", needle)
		}
	}

	if !strings.Contains(css, "(hover: hover)") {
		t.Error("tile/card hover must be gated on hover:hover so touch screens do not stick a lift state")
	}
}
