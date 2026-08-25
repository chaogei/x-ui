package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-ui/core/singbox"
	"x-ui/testutil"
)

// 这一组用例覆盖"内核升级必须先通过完整性校验"这条硬约束。
//
// 全部走 httptest，不碰真实网络：CI 里既不能依赖 GitHub 可达，
// 也不该在跑测试时真的去下载几十兆的内核。

const testCoreVersion = "v1.11.0"

// fakeGitHub 同时扮演 release 下载站与 Releases API。
type fakeGitHub struct {
	t *testing.T

	// archive 是 /SagerNet/sing-box/releases/download/... 返回的字节。
	archive []byte
	// digest 非空时 API 会给资产带上 "sha256:<digest>"。
	digest string
	// assetName 覆盖 API 返回的资产名，用于制造"名字对不上"的场景。
	assetName string
	// releaseStatus 非零时 API 直接返回该状态码。
	releaseStatus int
	// sidecar 非空时 `<archive>.sha256` 返回该内容，否则 404。
	sidecar string

	server *httptest.Server

	apiHits     int
	sidecarHits int
}

func newFakeGitHub(t *testing.T, archive []byte) *fakeGitHub {
	t.Helper()

	g := &fakeGitHub{t: t, archive: archive}
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/SagerNet/sing-box/releases/tags/", func(w http.ResponseWriter, r *http.Request) {
		g.apiHits++
		if g.releaseStatus != 0 {
			w.WriteHeader(g.releaseStatus)
			return
		}
		name := g.assetName
		if name == "" {
			name = coreArchiveName(testCoreVersion)
		}
		asset := releaseAsset{Name: name}
		if g.digest != "" {
			asset.Digest = "sha256:" + g.digest
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(releaseDetail{
			TagName: testCoreVersion,
			// 掺一个无关资产，确保匹配是按名字而不是按下标。
			Assets: []releaseAsset{
				{Name: "sing-box-1.11.0-windows-amd64.zip", Digest: "sha256:" + strings.Repeat("0", 64)},
				asset,
			},
		})
	})

	mux.HandleFunc("/SagerNet/sing-box/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			g.sidecarHits++
			if g.sidecar == "" {
				// sing-box 的真实行为：从不发布侧车文件。
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(g.sidecar))
			return
		}
		_, _ = w.Write(g.archive)
	})

	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)

	oldAPI, oldDownload := githubAPIBase, githubDownloadBase
	githubAPIBase = g.server.URL
	githubDownloadBase = g.server.URL
	t.Cleanup(func() {
		githubAPIBase, githubDownloadBase = oldAPI, oldDownload
	})
	return g
}

// singBoxArchiveBytes 造一个结构与官方 release 一致的 tar.gz。
func singBoxArchiveBytes(t *testing.T, marker string) []byte {
	t.Helper()

	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	body := "#!/bin/sh\necho " + marker + "\n"
	hdr := &tar.Header{
		Name:     "sing-box-1.11.0-linux-amd64/sing-box",
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// withTempBinaryPath 把进程工作目录切到临时目录并返回内核的安装路径。
//
// singbox.GetBinaryPath() 是相对路径 "bin/<name>"，所以只能靠切目录来
// 隔离；否则用例会往仓库工作区里写内核二进制。
func withTempBinaryPath(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return filepath.Join(dir, singbox.GetBinaryPath())
}

func TestVerifyCoreChecksumAcceptsMatchingReleaseDigest(t *testing.T) {
	archive := singBoxArchiveBytes(t, "kernel")
	g := newFakeGitHub(t, archive)
	g.digest = sha256Hex(archive)

	s := &ServerService{}
	if err := s.verifyCoreChecksum(testCoreVersion, "archive.tar.gz", sha256Hex(archive)); err != nil {
		t.Fatalf("a matching release digest must verify: %v", err)
	}
	if g.apiHits != 1 {
		t.Errorf("release API was hit %d times, want exactly 1", g.apiHits)
	}
	// 摘要已经权威，不该再多打一次侧车请求。
	if g.sidecarHits != 0 {
		t.Errorf("the sidecar was fetched %d times despite a valid digest", g.sidecarHits)
	}
}

func TestVerifyCoreChecksumRejectsMismatchedDigest(t *testing.T) {
	archive := singBoxArchiveBytes(t, "kernel")
	g := newFakeGitHub(t, archive)
	g.digest = strings.Repeat("a", 64)

	s := &ServerService{}
	err := s.verifyCoreChecksum(testCoreVersion, "archive.tar.gz", sha256Hex(archive))
	if err == nil {
		t.Fatal("a tampered archive was accepted")
	}
	if !errors.Is(err, ErrCoreChecksumMismatch) {
		t.Errorf("error = %v, want it to wrap ErrCoreChecksumMismatch", err)
	}
	// 摘要对不上是终态，不该悄悄退到侧车再试一次——那等于给攻击者第二次机会。
	if g.sidecarHits != 0 {
		t.Errorf("a digest mismatch fell through to the sidecar (%d fetches)", g.sidecarHits)
	}
}

// TestVerifyCoreChecksumFailsClosedWithoutAnyDigest 是这一整块修复的核心。
//
// sing-box 既不发布 .sha256 侧车，早期 release 也没有回填 digest。
// 历史实现在这种情况下打一行 warning 就继续安装——也就是说校验从未真正发生过。
func TestVerifyCoreChecksumFailsClosedWithoutAnyDigest(t *testing.T) {
	archive := singBoxArchiveBytes(t, "kernel")
	g := newFakeGitHub(t, archive) // digest 与 sidecar 都留空

	s := &ServerService{}
	err := s.verifyCoreChecksum(testCoreVersion, "archive.tar.gz", sha256Hex(archive))
	if err == nil {
		t.Fatal("an archive that could not be verified was accepted")
	}
	if !errors.Is(err, ErrCoreChecksumUnavailable) {
		t.Errorf("error = %v, want it to wrap ErrCoreChecksumUnavailable", err)
	}
	if g.sidecarHits == 0 {
		t.Error("the sidecar fallback was never tried")
	}
}

func TestVerifyCoreChecksumAcceptsSidecarWhenTheAPIHasNoDigest(t *testing.T) {
	archive := singBoxArchiveBytes(t, "kernel")
	sum := sha256Hex(archive)
	g := newFakeGitHub(t, archive)
	g.sidecar = sum + "  " + coreArchiveName(testCoreVersion) + "\n"

	s := &ServerService{}
	if err := s.verifyCoreChecksum(testCoreVersion, "archive.tar.gz", sum); err != nil {
		t.Fatalf("a valid sidecar must verify: %v", err)
	}
}

func TestVerifyCoreChecksumRejectsMismatchedSidecar(t *testing.T) {
	archive := singBoxArchiveBytes(t, "kernel")
	g := newFakeGitHub(t, archive)
	g.sidecar = strings.Repeat("b", 64)

	s := &ServerService{}
	err := s.verifyCoreChecksum(testCoreVersion, "archive.tar.gz", sha256Hex(archive))
	if !errors.Is(err, ErrCoreChecksumMismatch) {
		t.Errorf("error = %v, want ErrCoreChecksumMismatch", err)
	}
}

// TestVerifyCoreChecksumFailsWhenTheAssetIsAbsent 覆盖 GOOS/GOARCH 没有对应
// 归档的情形：不能因为"找不到资产"就当成校验通过。
func TestVerifyCoreChecksumFailsWhenTheAssetIsAbsent(t *testing.T) {
	archive := singBoxArchiveBytes(t, "kernel")
	g := newFakeGitHub(t, archive)
	g.assetName = "sing-box-1.11.0-plan9-mips.tar.gz"
	g.digest = sha256Hex(archive)

	s := &ServerService{}
	if err := s.verifyCoreChecksum(testCoreVersion, "archive.tar.gz", sha256Hex(archive)); !errors.Is(err, ErrCoreChecksumUnavailable) {
		t.Errorf("error = %v, want ErrCoreChecksumUnavailable", err)
	}
}

func TestVerifyCoreChecksumFailsWhenTheAPIIsDown(t *testing.T) {
	archive := singBoxArchiveBytes(t, "kernel")
	g := newFakeGitHub(t, archive)
	g.releaseStatus = http.StatusServiceUnavailable

	s := &ServerService{}
	if err := s.verifyCoreChecksum(testCoreVersion, "archive.tar.gz", sha256Hex(archive)); !errors.Is(err, ErrCoreChecksumUnavailable) {
		t.Errorf("error = %v, want ErrCoreChecksumUnavailable", err)
	}
}

func TestParseSHA256Digest(t *testing.T) {
	const sum = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	cases := map[string]struct {
		raw  string
		want string
	}{
		"github form":     {raw: "sha256:" + sum, want: sum},
		"uppercase hex":   {raw: "sha256:" + strings.ToUpper(sum), want: sum},
		"bare hex":        {raw: sum, want: sum},
		"padded":          {raw: "  sha256:" + sum + " ", want: sum},
		"empty":           {raw: "", want: ""},
		"sha512 rejected": {raw: "sha512:" + strings.Repeat("a", 128), want: ""},
		"truncated":       {raw: "sha256:" + sum[:60], want: ""},
		"not hex":         {raw: "sha256:" + strings.Repeat("zz", 32), want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := parseSHA256Digest(tc.raw); got != tc.want {
				t.Errorf("parseSHA256Digest(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestUpdateCoreInstallsOnlyVerifiedArchives 走完整流程：下载 → 校验 → 解压。
//
// 校验失败时磁盘上不能出现任何内核二进制，这是 fail-closed 的可观测定义。
func TestUpdateCoreInstallsOnlyVerifiedArchives(t *testing.T) {
	t.Run("verified archive is installed", func(t *testing.T) {
		archive := singBoxArchiveBytes(t, "verified-kernel")
		g := newFakeGitHub(t, archive)
		g.digest = sha256Hex(archive)
		// UpdateCore 成功后会尝试重启内核，那条路径要读设置表。
		testutil.InitDB(t)
		dst := withTempBinaryPath(t)

		s := &ServerService{}
		// RestartCore 会因为临时目录里的假内核不是真程序而失败，
		// 但那发生在解压之后，不影响本用例要断言的东西。
		_ = s.UpdateCore(testCoreVersion)

		content, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("the verified kernel was not installed: %v", err)
		}
		if !strings.Contains(string(content), "verified-kernel") {
			t.Errorf("installed content = %q", content)
		}
	})

	t.Run("unverifiable archive is refused", func(t *testing.T) {
		archive := singBoxArchiveBytes(t, "unverified-kernel")
		newFakeGitHub(t, archive) // 无 digest、无 sidecar
		dst := withTempBinaryPath(t)

		s := &ServerService{}
		err := s.UpdateCore(testCoreVersion)
		if !errors.Is(err, ErrCoreChecksumUnavailable) {
			t.Fatalf("error = %v, want ErrCoreChecksumUnavailable", err)
		}
		if _, statErr := os.Stat(dst); statErr == nil {
			t.Fatal("an unverified kernel binary was written to disk")
		}
	})

	t.Run("tampered archive is refused", func(t *testing.T) {
		archive := singBoxArchiveBytes(t, "evil-kernel")
		g := newFakeGitHub(t, archive)
		// 摘要来自另一份内容：这就是被中间人替换过的归档的样子。
		g.digest = sha256Hex(singBoxArchiveBytes(t, "the-real-kernel"))
		dst := withTempBinaryPath(t)

		s := &ServerService{}
		if err := s.UpdateCore(testCoreVersion); !errors.Is(err, ErrCoreChecksumMismatch) {
			t.Fatalf("error = %v, want ErrCoreChecksumMismatch", err)
		}
		if _, statErr := os.Stat(dst); statErr == nil {
			t.Fatal("a tampered kernel binary was written to disk")
		}
	})
}

// TestUpdateCoreLeavesNoTempArchiveBehind 升级失败也不能在 /tmp 里堆归档。
func TestUpdateCoreLeavesNoTempArchiveBehind(t *testing.T) {
	archive := singBoxArchiveBytes(t, "kernel")
	newFakeGitHub(t, archive)
	withTempBinaryPath(t)

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	s := &ServerService{}
	if err := s.UpdateCore(testCoreVersion); err == nil {
		t.Fatal("the unverifiable archive should not have installed")
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "x-ui-singbox-") {
			t.Errorf("left a temporary archive behind: %s", e.Name())
		}
	}
}

// TestCoreReleaseAPIURLTargetsGitHub 生产端点不能被测试用的替换值污染。
func TestCoreReleaseAPIURLTargetsGitHub(t *testing.T) {
	want := "https://api.github.com/repos/SagerNet/sing-box/releases/tags/v1.11.0"
	if got := coreReleaseAPIURL("v1.11.0"); got != want {
		t.Errorf("coreReleaseAPIURL = %q, want %q", got, want)
	}
	if !allowedDownloadHosts["api.github.com"] {
		t.Error("api.github.com must be on the download allowlist for the digest fetch to survive redirects")
	}
}
