package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"
	"time"
	"x-ui/core/singbox"
	"x-ui/logger"
	"x-ui/util/sys"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

type ProcessState string

const (
	Running ProcessState = "running"
	Stop    ProcessState = "stop"
	Error   ProcessState = "error"
)

// Status 是面板右上角"系统状态"卡片的完整快照，
// core 字段始终反映底层 sing-box 内核进程的运行状态。
type Status struct {
	T   time.Time `json:"-"`
	Cpu float64   `json:"cpu"`
	Mem struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"mem"`
	Swap struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"swap"`
	Disk struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"disk"`
	Core struct {
		State    ProcessState `json:"state"`
		ErrorMsg string       `json:"errorMsg"`
		Version  string       `json:"version"`
	} `json:"core"`
	Uptime   uint64    `json:"uptime"`
	Loads    []float64 `json:"loads"`
	TcpCount int       `json:"tcpCount"`
	UdpCount int       `json:"udpCount"`
	NetIO    struct {
		Up   uint64 `json:"up"`
		Down uint64 `json:"down"`
	} `json:"netIO"`
	NetTraffic struct {
		Sent uint64 `json:"sent"`
		Recv uint64 `json:"recv"`
	} `json:"netTraffic"`
}

type Release struct {
	TagName string `json:"tag_name"`
}

// releaseAsset 是 GitHub Releases API 中单个 release 资产的相关字段。
//
// digest 是 GitHub 于 2025 年为 release 资产补上的字段，形如
// "sha256:<64 hex>"，由 GitHub 在上传时计算。sing-box 从不发布 .sha256
// 侧车文件，所以这是唯一可用的权威校验源。
type releaseAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type releaseDetail struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

// githubAPIBase / githubDownloadBase 是可被测试替换的端点前缀。
//
// 生产取值固定指向 GitHub；用例把它们指到 httptest 服务器，
// 从而在完全离线的环境下覆盖"摘要匹配 / 摘要不符 / 无摘要"三条路径。
var (
	githubAPIBase      = "https://api.github.com"
	githubDownloadBase = "https://github.com"
)

// ErrCoreChecksumUnavailable 表示既拿不到 release 资产摘要，也没有 .sha256 侧车。
//
// 这不是"暂时性故障"，而是拒绝安装的终态：无法校验完整性的内核二进制
// 一律不落盘。
var ErrCoreChecksumUnavailable = errors.New("sing-box archive integrity could not be verified")

// ErrCoreChecksumMismatch 表示下载内容与权威摘要不一致。
var ErrCoreChecksumMismatch = errors.New("sing-box archive sha256 mismatch")

type ServerService struct {
	coreService CoreService
}

func (s *ServerService) GetStatus(lastStatus *Status) *Status {
	now := time.Now()
	status := &Status{T: now}

	if percents, err := cpu.Percent(0, false); err != nil {
		logger.Warning("get cpu percent failed:", err)
	} else if len(percents) > 0 {
		status.Cpu = percents[0]
	}

	if upTime, err := host.Uptime(); err != nil {
		logger.Warning("get uptime failed:", err)
	} else {
		status.Uptime = upTime
	}

	if memInfo, err := mem.VirtualMemory(); err != nil {
		logger.Warning("get virtual memory failed:", err)
	} else {
		status.Mem.Current = memInfo.Used
		status.Mem.Total = memInfo.Total
	}

	if swapInfo, err := mem.SwapMemory(); err != nil {
		logger.Warning("get swap memory failed:", err)
	} else {
		status.Swap.Current = swapInfo.Used
		status.Swap.Total = swapInfo.Total
	}

	if distInfo, err := disk.Usage("/"); err != nil {
		logger.Warning("get disk usage failed:", err)
	} else {
		status.Disk.Current = distInfo.Used
		status.Disk.Total = distInfo.Total
	}

	if avgState, err := load.Avg(); err != nil {
		logger.Warning("get load avg failed:", err)
	} else {
		status.Loads = []float64{avgState.Load1, avgState.Load5, avgState.Load15}
	}

	if ioStats, err := net.IOCounters(false); err != nil {
		logger.Warning("get io counters failed:", err)
	} else if len(ioStats) > 0 {
		ioStat := ioStats[0]
		status.NetTraffic.Sent = ioStat.BytesSent
		status.NetTraffic.Recv = ioStat.BytesRecv

		if lastStatus != nil {
			duration := now.Sub(lastStatus.T)
			seconds := float64(duration) / float64(time.Second)
			up := uint64(float64(status.NetTraffic.Sent-lastStatus.NetTraffic.Sent) / seconds)
			down := uint64(float64(status.NetTraffic.Recv-lastStatus.NetTraffic.Recv) / seconds)
			status.NetIO.Up = up
			status.NetIO.Down = down
		}
	} else {
		logger.Warning("can not find io counters")
	}

	var err error
	status.TcpCount, err = sys.GetTCPCount()
	if err != nil {
		logger.Warning("get tcp connections failed:", err)
	}
	status.UdpCount, err = sys.GetUDPCount()
	if err != nil {
		logger.Warning("get udp connections failed:", err)
	}

	if s.coreService.IsCoreRunning() {
		status.Core.State = Running
		status.Core.ErrorMsg = ""
	} else {
		if err := s.coreService.GetCoreErr(); err != nil {
			status.Core.State = Error
		} else {
			status.Core.State = Stop
		}
		status.Core.ErrorMsg = s.coreService.GetCoreResult()
	}
	status.Core.Version = s.coreService.GetCoreVersion()

	return status
}

// GetCoreVersions 拉取 SagerNet/sing-box 的最新 release 列表。
func (s *ServerService) GetCoreVersions() ([]string, error) {
	releasesURL := githubAPIBase + "/repos/SagerNet/sing-box/releases"
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(releasesURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list sing-box releases failed: HTTP %d", resp.StatusCode)
	}

	buffer := bytes.NewBuffer(make([]byte, 0, 16384))
	// 上限 4 MiB，避免恶意/异常响应把面板内存打爆。
	if _, err = buffer.ReadFrom(io.LimitReader(resp.Body, 4<<20)); err != nil {
		return nil, err
	}
	releases := make([]Release, 0)
	if err = json.Unmarshal(buffer.Bytes(), &releases); err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(releases))
	for _, release := range releases {
		versions = append(versions, release.TagName)
	}
	return versions, nil
}

// coreVersionPattern 限定用户可提交的 sing-box 版本号形态。
//
// 该字符串同时进入下载 URL 与本地文件名，必须先做白名单校验，
// 否则 `../../etc/cron.d/x` 这类输入会造成任意路径写入 + SSRF。
var coreVersionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+([.-][0-9A-Za-z.]+)*$`)

// ErrInvalidCoreVersion 表示版本号未通过白名单校验。
var ErrInvalidCoreVersion = errors.New("invalid sing-box version")

// allowedDownloadHosts 是 UpdateCore 允许连接的主机白名单。
// GitHub release 下载会 302 到 objects.githubusercontent.com，两者都需放行；
// 任何跳出白名单的重定向都会被拒绝，避免被篡改的 release 元数据把请求引向他处。
var allowedDownloadHosts = map[string]bool{
	"github.com":                           true,
	"api.github.com":                       true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
	"codeload.github.com":                  true,
}

// coreDownloadTimeout 是整个内核下载过程（含重定向）的上限。
const coreDownloadTimeout = 10 * time.Minute

// ValidateCoreVersion 校验版本号并返回规整后的值。
// 导出以便控制器在真正发起下载之前就返回 400 级错误。
func ValidateCoreVersion(version string) (string, error) {
	v := strings.TrimSpace(version)
	if v == "" || len(v) > 64 {
		return "", fmt.Errorf("%w: %q", ErrInvalidCoreVersion, version)
	}
	if !coreVersionPattern.MatchString(v) {
		return "", fmt.Errorf("%w: %q", ErrInvalidCoreVersion, version)
	}
	return v, nil
}

// newDownloadClient 构造受限的 HTTP 客户端：
// 带整体超时，且每一跳都必须落在 allowedDownloadHosts 内。
func newDownloadClient() *http.Client {
	return &http.Client{
		Timeout: coreDownloadTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if !allowedDownloadHosts[req.URL.Hostname()] {
				return fmt.Errorf("refusing redirect to untrusted host %q", req.URL.Hostname())
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("refusing non-https redirect to %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

// UpdateCore 下载指定版本的 sing-box 归档并替换 bin 目录下的二进制。
//
// 归档约定：`sing-box-<version>-<os>-<arch>.tar.gz`
// （Windows 归档为 .zip，本项目仅在 Linux/macOS 服务器部署，不做处理）。
//
// 安全约定：
//   - version 必须先过 ValidateCoreVersion 白名单，杜绝路径穿越与 URL 注入；
//   - 归档写入 os.CreateTemp 生成的临时文件，绝不用用户可控的相对路径；
//   - HTTP 客户端带超时且限制重定向目标主机。
//
// 完整性校验是强制的，不存在"校验不了就放行"的分支：
// 面板以 root 运行，未经校验的内核二进制等同于远程代码执行入口。
// 详见 verifyCoreChecksum。
func (s *ServerService) UpdateCore(version string) error {
	version, err := ValidateCoreVersion(version)
	if err != nil {
		return err
	}

	archivePath, computedSum, err := s.downloadCore(version)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	if err := s.verifyCoreChecksum(version, archivePath, computedSum); err != nil {
		return err
	}

	_ = s.coreService.StopCore()
	defer func() {
		if err := s.coreService.RestartCore(true); err != nil {
			logger.Error("start sing-box failed:", err)
		}
	}()

	return extractSingBoxBinary(archivePath, singbox.GetBinaryPath())
}

// coreArchiveName 返回给定版本的归档文件名。version 必须已通过校验。
func coreArchiveName(version string) string {
	// sing-box 归档文件名里版本号不含 "v" 前缀，但 release tag 含 "v"。
	rawVersion := strings.TrimPrefix(version, "v")
	return fmt.Sprintf("sing-box-%s-%s-%s.tar.gz", rawVersion, runtime.GOOS, runtime.GOARCH)
}

// coreDownloadURL 返回归档下载地址。version 必须已通过校验。
func coreDownloadURL(version string) string {
	return fmt.Sprintf("%s/SagerNet/sing-box/releases/download/%s/%s",
		githubDownloadBase, url.PathEscape(version), url.PathEscape(coreArchiveName(version)))
}

// coreReleaseAPIURL 返回该 tag 的 GitHub Releases API 地址。
func coreReleaseAPIURL(version string) string {
	return fmt.Sprintf("%s/repos/SagerNet/sing-box/releases/tags/%s",
		githubAPIBase, url.PathEscape(version))
}

// downloadCore 下载归档并同时计算 SHA256，返回（临时文件路径，sha256 hex，错误）。
func (s *ServerService) downloadCore(version string) (string, string, error) {
	resp, err := newDownloadClient().Get(coreDownloadURL(version))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download sing-box failed: HTTP %d", resp.StatusCode)
	}

	// 固定的进程临时目录 + 随机后缀，文件名与用户输入完全解耦。
	file, err := os.CreateTemp("", "x-ui-singbox-*.tar.gz")
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	hasher := sha256.New()
	// 同时写入文件与 hasher，避免再读一次磁盘算哈希。
	if _, err = io.Copy(io.MultiWriter(file, hasher), resp.Body); err != nil {
		os.Remove(file.Name())
		return "", "", err
	}
	return file.Name(), hex.EncodeToString(hasher.Sum(nil)), nil
}

// verifyCoreChecksum 强制校验归档 SHA256，无法完成校验即视为失败。
//
// 为什么必须 fail-closed：这个归档解压出来的二进制会被面板以 root 身份执行。
// 历史实现在 release 缺少 `.sha256` 侧车时只打一行 warning 就继续安装——
// 而 sing-box 从来不发布侧车文件，所以那条"尽力校验"分支等于永远不校验：
// 任何能中间人劫持 objects.githubusercontent.com 的攻击者都能换掉内核。
//
// 校验源按优先级：
//  1. GitHub Releases API 的资产 digest 字段（形如 "sha256:<hex>"）。
//     这是 sing-box 唯一提供的权威摘要，与归档走不同主机、不同 TLS 会话。
//  2. `{archive_url}.sha256` 侧车文件，兼容将来可能补上侧车的 release，
//     以及自建镜像。
//
// 两者都拿不到 → 返回 ErrCoreChecksumUnavailable，调用方不得解压安装。
func (s *ServerService) verifyCoreChecksum(version, archivePath, computedSum string) error {
	digest, apiErr := fetchReleaseAssetDigest(version, coreArchiveName(version))
	if apiErr == nil && digest != "" {
		if !strings.EqualFold(digest, computedSum) {
			return fmt.Errorf("%w: release asset digest %s, downloaded %s",
				ErrCoreChecksumMismatch, digest, computedSum)
		}
		logger.Infof("sing-box %s verified against the GitHub release asset digest (sha256=%s)", version, computedSum)
		return nil
	}
	if apiErr != nil {
		logger.Warningf("sing-box %s: release asset digest unavailable (%v), falling back to the .sha256 sidecar", version, apiErr)
	}

	sidecar, sidecarErr := fetchChecksumSidecar(coreDownloadURL(version) + ".sha256")
	if sidecarErr == nil && sidecar != "" {
		if !strings.EqualFold(sidecar, computedSum) {
			return fmt.Errorf("%w: sidecar says %s, downloaded %s",
				ErrCoreChecksumMismatch, sidecar, computedSum)
		}
		logger.Infof("sing-box %s verified against the .sha256 sidecar (sha256=%s)", version, computedSum)
		return nil
	}

	// 到这里说明两条校验路径都没能给出一个可比对的摘要。
	// 明确拒绝，并把两边的原因都写进错误信息，便于运维判断是网络问题还是资产缺失。
	return fmt.Errorf("%w for %s (downloaded sha256=%s): release API: %v; sidecar: %v",
		ErrCoreChecksumUnavailable, version, computedSum,
		orNoDigest(apiErr), orNoDigest(sidecarErr))
}

// orNoDigest 把"请求成功但没有摘要"的 nil error 表述为一句人话。
func orNoDigest(err error) error {
	if err == nil {
		return errors.New("no digest published")
	}
	return err
}

// fetchReleaseAssetDigest 从 GitHub Releases API 取出指定资产的 sha256 摘要。
//
// 返回 ("", nil) 表示接口正常但该资产没有 digest 字段（老 release 尚未回填）。
// 复用 newDownloadClient：同一份主机白名单、同样只允许 https 重定向。
func fetchReleaseAssetDigest(version, assetName string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, coreReleaseAPIURL(version), nil)
	if err != nil {
		return "", err
	}
	// GitHub API 拒绝没有 User-Agent 的请求，并用 Accept 头锁定响应 schema 版本。
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "x-ui")

	resp, err := newDownloadClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release API returned HTTP %d", resp.StatusCode)
	}

	// 上限 8 MiB：sing-box 的 release 资产列表约几十 KB，留足余量的同时挡住异常响应。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	detail := &releaseDetail{}
	if err := json.Unmarshal(body, detail); err != nil {
		return "", fmt.Errorf("release API response is not valid JSON: %w", err)
	}
	for _, asset := range detail.Assets {
		if asset.Name != assetName {
			continue
		}
		digest := parseSHA256Digest(asset.Digest)
		if digest == "" {
			return "", fmt.Errorf("asset %s carries no usable sha256 digest (%q)", assetName, asset.Digest)
		}
		return digest, nil
	}
	return "", fmt.Errorf("release %s has no asset named %s", version, assetName)
}

// parseSHA256Digest 解析 GitHub 的 "sha256:<hex>" 摘要串。
// 非 sha256 算法或长度不对时返回空串——绝不把无法判定的值当成通过。
func parseSHA256Digest(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

// fetchChecksumSidecar 读取 `<archive>.sha256` 侧车文件里的第一个 sha256。
// 返回 ("", nil) 表示 404，即 release 未提供侧车。
func fetchChecksumSidecar(checksumURL string) (string, error) {
	resp, err := newDownloadClient().Get(checksumURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sidecar fetch returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	sum := parseFirstHex(string(body))
	if sum == "" {
		return "", errors.New("sidecar body carries no hex sha256")
	}
	return sum, nil
}

// parseFirstHex 从 checksum 文件内容中提取第一个 64 位十六进制串（SHA256）。
// 兼容 `<hex>` / `<hex>  <filename>` / `SHA256=<hex>` 等常见变体。
func parseFirstHex(s string) string {
	for _, field := range strings.Fields(s) {
		trimmed := strings.TrimPrefix(strings.ToLower(field), "sha256=")
		trimmed = strings.TrimPrefix(trimmed, "sha-256=")
		if len(trimmed) == 64 {
			if _, err := hex.DecodeString(trimmed); err == nil {
				return trimmed
			}
		}
	}
	return ""
}

// extractSingBoxBinary 从 sing-box 的 tar.gz 归档中抽取 sing-box 可执行文件到 dstPath。
//
// 归档结构为 sing-box-<version>-<os>-<arch>/sing-box[.exe]；
// 为避免把解压目录写死，搜索第一个 basename == "sing-box" 的条目。
func extractSingBoxBinary(archivePath, dstPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("sing-box binary not found in archive %s", archivePath)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := path.Base(hdr.Name)
		if base != "sing-box" && base != "sing-box.exe" {
			continue
		}
		if err := os.MkdirAll(path.Dir(dstPath), 0o700); err != nil {
			return err
		}
		os.Remove(dstPath)
		// 0700：owner 可执行即可，绝不下发 world-writable 的内核二进制。
		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, singbox.BinaryFilePerm)
		if err != nil {
			return err
		}
		if _, err = io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		// OpenFile 的 perm 会被 umask 削减，显式 Chmod 保证最终权限确定。
		return os.Chmod(dstPath, singbox.BinaryFilePerm)
	}
}
