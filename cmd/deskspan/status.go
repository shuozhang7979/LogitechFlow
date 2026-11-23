package main

import (
	"fmt"
	"io"
	"strings"
)

// These are the fields used from the public v0.3.6 agent API. Decode only
// metadata; clipboard contents, event streams and diagnostic logs are unnecessary.
type statusDevice struct {
	ID         string
	Name       string
	OS         string
	AppVersion string `json:"app_version"`
}

type statusOverview struct {
	Local     statusDevice `json:"this_device"`
	Service   string
	Sharing   bool          `json:"sharing_enabled"`
	FocusLock *statusDevice `json:"focus_lock"`
	Alerts    []struct {
		Kind struct{ Type string }
	}
}

type statusMember struct {
	Device  statusDevice
	IsSelf  bool `json:"is_self"`
	State   struct{ Type string }
	Sharing string
}

type statusDisplay struct {
	Name        string
	PixelWidth  uint32 `json:"pixel_width"`
	PixelHeight uint32 `json:"pixel_height"`
	Primary     bool   `json:"is_primary"`
	Bounds      struct{ X, Y float64 }
}

func (d statusDisplay) description() string {
	var fields []string
	if d.PixelWidth != 0 && d.PixelHeight != 0 {
		fields = append(fields, fmt.Sprintf("%d×%d px", d.PixelWidth, d.PixelHeight))
	}
	if d.Primary {
		fields = append(fields, "主屏")
	}
	return strings.Join(fields, " · ")
}

type statusScreen struct {
	DeviceName string `json:"device_name"`
	Display    statusDisplay
	Origin     struct{ X, Y float64 }
	Hidden     bool
	Disabled   bool `json:"device_disabled"`
	Online     bool
}

type statusFileTransfer struct {
	ID          string  `json:"id"`
	Direction   string  `json:"direction"`
	Kind        string  `json:"kind"`
	FromName    string  `json:"from_name"`
	CurrentFile string  `json:"current_file"`
	Done        uint64  `json:"done"`
	Total       uint64  `json:"total"`
	FilesDone   int     `json:"files_done"`
	Files       int     `json:"files"`
	SpeedBps    uint64  `json:"speed_bps"`
	EtaSecs     *uint64 `json:"eta_secs"`
	Phase       string  `json:"phase"`
	Message     string  `json:"message"`
}

func formatStatusBytes(bytes uint64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	case bytes < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", float64(bytes)/(1024*1024*1024))
	}
}

func formatStatusSpeed(bps uint64) string {
	switch {
	case bps == 0:
		return ""
	case bps >= 1024*1024*1024:
		return fmt.Sprintf("%.2f GB/s", float64(bps)/(1024*1024*1024))
	case bps >= 1024*1024:
		return fmt.Sprintf("%.1f MB/s", float64(bps)/(1024*1024))
	default:
		kb := float64(bps) / 1024
		if kb < 1 {
			kb = 1
		}
		return fmt.Sprintf("%.0f KB/s", kb)
	}
}

func formatStatusEta(eta *uint64) string {
	if eta == nil {
		return ""
	}
	secs := *eta
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm%ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%dh%dm", secs/3600, (secs%3600)/60)
	}
}

func statusTransferPhase(phase string) string {
	switch phase {
	case "pending":
		return "准备中"
	case "downloading":
		return "传输中"
	case "ready":
		return "已完成"
	case "failed":
		return "失败"
	case "cancelled":
		return "已取消"
	default:
		return phase
	}
}

func statusSwitch(on bool) string {
	if on {
		return "开启"
	}
	return "关闭"
}

type statusQuery func(out any, args ...string) error

func showStatus(query statusQuery, out io.Writer) error {
	var overview statusOverview
	if err := query(&overview, "call", "overview"); err != nil {
		// "displays" asks the OS directly and still works before pairing or
		// when the installed desktop backend is closed.
		var displays []statusDisplay
		if displayErr := query(&displays, "displays"); displayErr == nil {
			fmt.Fprintf(out, "操作系统检测到 %d 块本机显示器\n", len(displays))
			for _, d := range displays {
				fmt.Fprintf(out, "  %q  %s  系统坐标 (%.0f, %.0f)\n", d.Name, d.description(), d.Bounds.X, d.Bounds.Y)
			}
		}
		return fmt.Errorf("读取共享状态失败：%w", err)
	}
	if overview.Local.ID == "" || overview.Service == "" {
		return fmt.Errorf("Oink 返回的状态缺少本机标识或后台状态，请核对软件版本")
	}
	fmt.Fprintf(out, "本机 %q  %s  Oink %s\n", overview.Local.Name, overview.Local.OS, overview.Local.AppVersion)
	fmt.Fprintf(out, "共享 %s  后台 %s\n", statusSwitch(overview.Sharing), overview.Service)
	if !overview.Sharing {
		fmt.Fprintln(out, "  需要跨机操作时，请在 Oink 中恢复共享。")
	}
	if overview.FocusLock != nil {
		fmt.Fprintf(out, "  焦点锁定在 %q。解除焦点锁后可移动到其他电脑。\n", overview.FocusLock.Name)
	}
	if len(overview.Alerts) > 0 {
		fmt.Fprint(out, "  请在 Oink 中查看提醒")
		for _, alert := range overview.Alerts {
			fmt.Fprintf(out, "  %q", alert.Kind.Type)
		}
		fmt.Fprintln(out)
	}

	var group struct{ Members []statusMember }
	if err := query(&group, "call", "group"); err != nil {
		return fmt.Errorf("读取组内设备失败：%w", err)
	}
	fmt.Fprintln(out, "\n组内设备")
	var activePeers, peers int
	for _, m := range group.Members {
		role := "本机"
		if !m.IsSelf {
			role = "对端"
			peers++
			if m.Sharing == "active" {
				activePeers++
			}
		}
		fmt.Fprintf(out, "  %s  %q  %s  %s  共享链路 %s\n    ID %s\n", role, m.Device.Name, m.Device.OS, m.State.Type, m.Sharing, m.Device.ID)
	}
	switch {
	case peers == 0:
		fmt.Fprintln(out, "  在各台电脑上启动 Oink，在「设备」中选择另一台并核对配对码。")
	case activePeers == 0 && overview.Sharing:
		fmt.Fprintln(out, "  尚无可用的对端输入链路，请检查对端是否在线并已开启共享。")
	}

	var layout struct {
		Geometry struct{ Screens []statusScreen }
	}
	if err := query(&layout, "layout", "show"); err != nil {
		return fmt.Errorf("读取屏幕布局失败：%w", err)
	}
	fmt.Fprintf(out, "\n共享布局 · %d 块屏幕（坐标为布局画布左上角位置）\n", len(layout.Geometry.Screens))
	for _, s := range layout.Geometry.Screens {
		var flags []string
		if !s.Online {
			flags = append(flags, "离线")
		}
		if s.Hidden {
			flags = append(flags, "已隐藏")
		}
		if s.Disabled {
			flags = append(flags, "已禁止进入")
		}
		fmt.Fprintf(out, "  %q / %q  (%.1f, %.1f)  %s  %s\n", s.DeviceName, s.Display.Name, s.Origin.X, s.Origin.Y, s.Display.description(), strings.Join(flags, " · "))
	}

	var clip struct{ Send, Receive bool }
	if err := query(&clip, "files", "settings"); err != nil {
		return fmt.Errorf("读取本机剪贴板设置失败：%w", err)
	}
	fmt.Fprintf(out, "\n本机剪贴板  发送 %s  接收 %s\n", statusSwitch(clip.Send), statusSwitch(clip.Receive))
	if overview.Local.OS == "macos" {
		var provider struct{ State string }
		if err := query(&provider, "file-provider", "status"); err != nil {
			return fmt.Errorf("读取访达文件提供程序状态失败：%w", err)
		}
		fmt.Fprintf(out, "访达按需文件  %s\n", provider.State)
		if provider.State != "available" {
			fmt.Fprintln(out, "  在 Oink 的文件设置和访达中检查文件提供程序的启用状态。")
		}
	}

	var transfers []statusFileTransfer
	if err := query(&transfers, "call", "file_transfers"); err == nil && len(transfers) > 0 {
		fmt.Fprintf(out, "\n文件传输 · %d 项\n", len(transfers))
		for _, t := range transfers {
			dir := "接收 <-"
			if t.Direction == "sending" {
				dir = "发送 ->"
			}
			parts := []string{statusTransferPhase(t.Phase)}
			if t.CurrentFile != "" {
				parts = append(parts, t.CurrentFile)
			}
			if t.Files > 1 {
				parts = append(parts, fmt.Sprintf("%d/%d 个文件", t.FilesDone, t.Files))
			}
			if t.Total > 0 {
				done := t.Done
				if done > t.Total {
					done = t.Total
				}
				pct := done * 100 / t.Total
				if t.Phase == "ready" {
					pct = 100
				}
				parts = append(parts, fmt.Sprintf("%s / %s (%d%%)", formatStatusBytes(done), formatStatusBytes(t.Total), pct))
			} else if t.Done > 0 {
				parts = append(parts, formatStatusBytes(t.Done))
			}
			active := t.Phase == "pending" || t.Phase == "downloading"
			if speed := formatStatusSpeed(t.SpeedBps); active && speed != "" {
				parts = append(parts, speed)
			}
			if eta := formatStatusEta(t.EtaSecs); active && eta != "" {
				parts = append(parts, "剩余 "+eta)
			}
			if t.Phase == "failed" && t.Message != "" {
				parts = append(parts, t.Message)
			}
			fmt.Fprintf(out, "  %s %q  %s\n", dir, t.FromName, strings.Join(parts, " · "))
		}
	}
	return nil
}
