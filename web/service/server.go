package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
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
	url := "https://api.github.com/repos/SagerNet/sing-box/releases"
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	buffer := bytes.NewBuffer(make([]byte, 0, 16384))
	if _, err = buffer.ReadFrom(resp.Body); err != nil {
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

// UpdateCore 下载指定版本的 sing-box 归档并替换 bin 目录下的二进制。
//
// 归档约定：`sing-box-<version>-<os>-<arch>.tar.gz`
// （Windows 归档为 .zip，本项目仅在 Linux/macOS 服务器部署，不做处理）。
//
// 下载后会尽力尝试 SHA256 校验：
//   - 若 release 提供了 `{archive}.sha256` 侧车文件，则必须一致；不一致直接失败。
//   - 若 checksum 文件不存在（HTTP 404），则告警后继续（best-effort，避免阻断旧版）。
func (s *ServerService) UpdateCore(version string) error {
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

// downloadCore 下载归档并同时计算 SHA256，返回（路径，sha256 hex，错误）。
func (s *ServerService) downloadCore(version string) (string, string, error) {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	// sing-box 归档文件名里版本号不含 "v" 前缀，但 release tag 含 "v"。
	rawVersion := version
	if strings.HasPrefix(rawVersion, "v") {
		rawVersion = rawVersion[1:]
	}

	fileName := fmt.Sprintf("sing-box-%s-%s-%s.tar.gz", rawVersion, osName, arch)
	url := fmt.Sprintf("https://github.com/SagerNet/sing-box/releases/download/%s/%s", version, fileName)
	resp, err := http.Get(url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download sing-box failed: HTTP %d", resp.StatusCode)
	}

	os.Remove(fileName)
	file, err := os.Create(fileName)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	hasher := sha256.New()
	// 同时写入文件与 hasher，避免再读一次磁盘算哈希。
	if _, err = io.Copy(io.MultiWriter(file, hasher), resp.Body); err != nil {
		return "", "", err
	}
	return fileName, hex.EncodeToString(hasher.Sum(nil)), nil
}

// verifyCoreChecksum 尽力校验归档 SHA256。
//
// 优先级：
//  1. `{archive_url}.sha256` 侧车文件：格式通常为 `<hex>  <filename>` 或纯 `<hex>`。
//  2. 若 404 则判定 release 未提供 checksum，记录 warning 并放行。
//  3. 其他下载错误视为临时故障，同样放行（不影响升级操作本身）。
func (s *ServerService) verifyCoreChecksum(version, archivePath, computedSum string) error {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	rawVersion := strings.TrimPrefix(version, "v")
	fileName := fmt.Sprintf("sing-box-%s-%s-%s.tar.gz", rawVersion, osName, arch)
	url := fmt.Sprintf("https://github.com/SagerNet/sing-box/releases/download/%s/%s.sha256", version, fileName)

	resp, err := http.Get(url)
	if err != nil {
		logger.Warningf("fetch sing-box checksum failed, skip verification: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		logger.Warningf("sing-box release %s has no .sha256 sidecar, skip verification", version)
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		logger.Warningf("fetch sing-box checksum returned HTTP %d, skip verification", resp.StatusCode)
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		logger.Warningf("read sing-box checksum body failed: %v", err)
		return nil
	}

	expected := parseFirstHex(string(body))
	if expected == "" {
		logger.Warningf("sing-box checksum body has no hex sha256, skip verification")
		return nil
	}
	if !strings.EqualFold(expected, computedSum) {
		return fmt.Errorf("sing-box archive sha256 mismatch: expected %s, got %s", expected, computedSum)
	}
	logger.Infof("sing-box archive %s sha256 verified", archivePath)
	return nil
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
		os.Remove(dstPath)
		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.ModePerm)
		if err != nil {
			return err
		}
		if _, err = io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}
