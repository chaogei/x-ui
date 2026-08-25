package service

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"x-ui/core/singbox"
)

// TestValidateCoreVersionAcceptsRealTags 用 sing-box 实际发布过的 tag 形态做正向用例。
func TestValidateCoreVersionAcceptsRealTags(t *testing.T) {
	valid := []string{
		"v1.11.0",
		"1.11.0",
		"v1.10.7",
		"v1.12.0-beta.1",
		"v1.12.0-rc.3",
		"v1.9.0-alpha.13",
		"v1.8.14",
	}
	for _, v := range valid {
		t.Run(v, func(t *testing.T) {
			got, err := ValidateCoreVersion(v)
			if err != nil {
				t.Fatalf("version %q must be accepted, got %v", v, err)
			}
			if got != v {
				t.Errorf("normalized version = %q, want %q", got, v)
			}
		})
	}
}

func TestValidateCoreVersionTrimsWhitespace(t *testing.T) {
	got, err := ValidateCoreVersion("  v1.11.0\n")
	if err != nil {
		t.Fatalf("padded version must be accepted: %v", err)
	}
	if got != "v1.11.0" {
		t.Errorf("normalized version = %q, want %q", got, "v1.11.0")
	}
}

// TestValidateCoreVersionRejectsInjection 是 H-2 的回归护栏。
//
// 修复前 version 会同时拼进下载 URL 和 os.Create 的文件名，所以
// `../../etc/cron.d/x` 能写任意路径，`@evil.tld/` 能把下载重定向到别处。
func TestValidateCoreVersionRejectsInjection(t *testing.T) {
	invalid := []string{
		"",
		"   ",
		"../../etc/cron.d/x",
		"v1.11.0/../../../root/.ssh/authorized_keys",
		"v1.11.0/..",
		"..",
		"/etc/passwd",
		"v1.11.0;rm -rf /",
		"v1.11.0 && curl evil.tld",
		"v1.11.0`id`",
		"v1.11.0$(id)",
		"v1.11.0\nv1.11.1",
		"v1.11.0\x00",
		"@evil.tld/x",
		"https://evil.tld/payload.tar.gz",
		"latest",
		"main",
		"v1.11",
		"v1",
		"v1.11.0 ../x",
		strings.Repeat("1.2.3", 40),
	}
	for _, v := range invalid {
		t.Run(strings.ReplaceAll(v, "/", "_"), func(t *testing.T) {
			if _, err := ValidateCoreVersion(v); err == nil {
				t.Fatalf("version %q must be rejected", v)
			} else if !errors.Is(err, ErrInvalidCoreVersion) {
				t.Errorf("error = %v, want it to wrap ErrInvalidCoreVersion", err)
			}
		})
	}
}

// TestCoreDownloadURLStaysOnGithub 保证即便校验被绕过，URL 也不会跳出白名单主机。
func TestCoreDownloadURLStaysOnGithub(t *testing.T) {
	u, err := url.Parse(coreDownloadURL("v1.11.0"))
	if err != nil {
		t.Fatalf("download URL is not parseable: %v", err)
	}
	if u.Scheme != "https" {
		t.Errorf("scheme = %q, want https", u.Scheme)
	}
	if u.Host != "github.com" {
		t.Errorf("host = %q, want github.com", u.Host)
	}
	if !strings.Contains(u.Path, "/SagerNet/sing-box/releases/download/v1.11.0/") {
		t.Errorf("path = %q, want the sing-box release download path", u.Path)
	}
}

func TestCoreArchiveNameDropsVPrefix(t *testing.T) {
	want := "sing-box-1.11.0-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz"
	if got := coreArchiveName("v1.11.0"); got != want {
		t.Errorf("archive name = %q, want %q", got, want)
	}
	if got := coreArchiveName("1.11.0"); got != want {
		t.Errorf("archive name without v prefix = %q, want %q", got, want)
	}
}

// TestDownloadClientRefusesOffAllowlistRedirect 验证 302 到任意主机会被拒绝。
func TestDownloadClientRefusesOffAllowlistRedirect(t *testing.T) {
	client := newDownloadClient()

	cases := []struct {
		name    string
		target  string
		allowed bool
	}{
		{name: "github release", target: "https://github.com/SagerNet/sing-box/releases/download/v1.11.0/x.tar.gz", allowed: true},
		{name: "github object store", target: "https://objects.githubusercontent.com/blob", allowed: true},
		{name: "attacker host", target: "https://evil.tld/payload", allowed: false},
		{name: "lookalike host", target: "https://github.com.evil.tld/payload", allowed: false},
		{name: "link-local metadata", target: "http://169.254.169.254/latest/meta-data/", allowed: false},
		{name: "plain http github", target: "http://github.com/x", allowed: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tc.target, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			err = client.CheckRedirect(req, nil)
			if tc.allowed && err != nil {
				t.Errorf("redirect to %s must be allowed, got %v", tc.target, err)
			}
			if !tc.allowed && err == nil {
				t.Errorf("redirect to %s must be refused", tc.target)
			}
		})
	}
}

func TestDownloadClientRefusesRedirectLoops(t *testing.T) {
	client := newDownloadClient()
	req, err := http.NewRequest(http.MethodGet, "https://github.com/x", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	via := make([]*http.Request, 10)
	if err := client.CheckRedirect(req, via); err == nil {
		t.Fatal("a 10-hop redirect chain must be refused")
	}
}

func TestDownloadClientHasTimeout(t *testing.T) {
	if newDownloadClient().Timeout <= 0 {
		t.Fatal("the download client must carry an overall timeout")
	}
}

func TestParseFirstHex(t *testing.T) {
	const sum = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	cases := map[string]struct {
		body string
		want string
	}{
		"bare":            {body: sum, want: sum},
		"coreutils style": {body: sum + "  sing-box-1.11.0-linux-amd64.tar.gz\n", want: sum},
		"labelled":        {body: "SHA256=" + strings.ToUpper(sum), want: sum},
		"uppercase":       {body: strings.ToUpper(sum), want: sum},
		"leading noise":   {body: "# checksum file\n" + sum + "  archive\n", want: sum},
		"empty":           {body: "", want: ""},
		"html error page": {body: "<html><body>404 Not Found</body></html>", want: ""},
		"wrong length":    {body: sum[:63], want: ""},
		"not hex":         {body: strings.Repeat("z", 64), want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := parseFirstHex(tc.body); got != tc.want {
				t.Errorf("parseFirstHex(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// writeSingBoxArchive 造一个结构与官方 release 一致的 tar.gz。
func writeSingBoxArchive(t *testing.T, entries map[string]string) string {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), "sing-box.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o777,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if strings.HasSuffix(name, "/") {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %s: %v", name, err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(content)); err != nil {
				t.Fatalf("write tar body %s: %v", name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return archivePath
}

// TestExtractSingBoxBinaryPermissions 是 C-2 的回归护栏：
// 归档里的 0777 绝不能原样落到磁盘上的内核二进制。
func TestExtractSingBoxBinaryPermissions(t *testing.T) {
	archive := writeSingBoxArchive(t, map[string]string{
		"sing-box-1.11.0-linux-amd64/":         "",
		"sing-box-1.11.0-linux-amd64/LICENSE":  "GPL",
		"sing-box-1.11.0-linux-amd64/sing-box": "#!/bin/sh\necho fake-kernel\n",
	})

	dst := filepath.Join(t.TempDir(), "bin", singbox.GetBinaryName())
	if err := extractSingBoxBinary(archive, dst); err != nil {
		t.Fatalf("extract sing-box binary: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat extracted binary: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != singbox.BinaryFilePerm {
		t.Errorf("binary mode = %#o, want %#o (never world-writable)", mode, singbox.BinaryFilePerm)
	}
	if mode&0o002 != 0 {
		t.Errorf("binary mode = %#o is world-writable", mode)
	}

	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if !strings.Contains(string(content), "fake-kernel") {
		t.Errorf("extracted content = %q, want the archived binary", content)
	}
}

func TestExtractSingBoxBinaryReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "sing-box")
	if err := os.WriteFile(dst, []byte("old"), 0o700); err != nil {
		t.Fatalf("seed existing binary: %v", err)
	}

	archive := writeSingBoxArchive(t, map[string]string{"pkg/sing-box": "new"})
	if err := extractSingBoxBinary(archive, dst); err != nil {
		t.Fatalf("extract sing-box binary: %v", err)
	}
	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if string(content) != "new" {
		t.Errorf("content = %q, want the freshly extracted binary", content)
	}
}

func TestExtractSingBoxBinaryFailsWhenAbsent(t *testing.T) {
	archive := writeSingBoxArchive(t, map[string]string{
		"sing-box-1.11.0-linux-amd64/LICENSE": "GPL",
		"sing-box-1.11.0-linux-amd64/README":  "docs",
	})
	dst := filepath.Join(t.TempDir(), "sing-box")

	err := extractSingBoxBinary(archive, dst)
	if err == nil {
		t.Fatal("an archive without the binary must fail")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want it to say the binary is missing", err)
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Error("no file should be written when extraction fails")
	}
}

func TestExtractSingBoxBinaryRejectsCorruptArchive(t *testing.T) {
	corrupt := filepath.Join(t.TempDir(), "corrupt.tar.gz")
	if err := os.WriteFile(corrupt, []byte("this is not a gzip stream"), 0o600); err != nil {
		t.Fatalf("write corrupt archive: %v", err)
	}
	if err := extractSingBoxBinary(corrupt, filepath.Join(t.TempDir(), "sing-box")); err == nil {
		t.Fatal("a corrupt archive must be rejected")
	}
}

// TestUpdateCoreRejectsBadVersionBeforeAnyIO 确认非法版本在触网之前就被挡下，
// 也就是说这条用例不需要网络也能稳定跑。
func TestUpdateCoreRejectsBadVersionBeforeAnyIO(t *testing.T) {
	s := &ServerService{}
	if err := s.UpdateCore("../../etc/cron.d/x"); !errors.Is(err, ErrInvalidCoreVersion) {
		t.Fatalf("error = %v, want ErrInvalidCoreVersion", err)
	}
}
