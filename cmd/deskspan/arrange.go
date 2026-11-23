package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
)

const arrangeUsage = "deskspan arrange [--apply] row|column DEVICE DEVICE [DEVICE ...]"

// The local agent reports both the shared canvas (mm) and the OS desktop
// (native points or pixels). A device has one translation and uniform scale.
type canvasPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type canvasSize struct {
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type canvasRect struct {
	canvasPoint
	canvasSize
}

type arrangeScreen struct {
	Key struct {
		Device  string
		Display string
	}
	Name     string `json:"device_name"`
	Origin   canvasPoint
	Size     canvasSize
	Display  struct{ Bounds canvasRect }
	Hidden   bool
	Disabled bool `json:"device_disabled"`
	Online   bool
}

type arrangeLayout struct {
	Revision uint64
	Geometry struct{ Screens []arrangeScreen }
}

type placement struct {
	Device string      `json:"device"`
	Origin canvasPoint `json:"origin"`
	Scale  float64     `json:"scale"`
}

type desktopGroup struct {
	id, name string
	online   bool
	screens  []canvasRect
	current  placement
	next     placement
}

func runArrange(agent Agent, args []string, out io.Writer) error {
	return arrangeCommand(agent.Query, args, out)
}

func arrangeCommand(query func(any, ...string) error, args []string, out io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, arrangeUsage)
		fmt.Fprintln(out, "按设备名称或完整 ID 排列。第一台电脑固定。row 从左到右，column 从上到下。加 --apply 即可保存。")
		return nil
	}
	apply := len(args) != 0 && args[0] == "--apply"
	if apply {
		args = args[1:]
	}
	if len(args) < 3 || (args[0] != "row" && args[0] != "column") {
		return errors.New(arrangeUsage)
	}
	var layout arrangeLayout
	if err := query(&layout, "layout", "show"); err != nil {
		return err
	}
	groups, err := arrangeDevices(layout, args[1:], args[0] == "column")
	if err != nil {
		return err
	}
	changes := previewArrangement(groups, out)
	if len(changes) == 0 {
		return nil
	}
	if !apply {
		fmt.Fprintln(out, "预览完成。要保存，请在 arrange 后加 --apply。")
		return nil
	}
	return commitArrangement(query, layout, changes, out)
}

func previewArrangement(groups []*desktopGroup, out io.Writer) []placement {
	var changes []placement
	for _, g := range groups {
		fmt.Fprintf(out, "%q · %d 块屏幕 · 桌面原点 (%.1f, %.1f) mm\n", g.name, len(g.screens), g.next.Origin.X, g.next.Origin.Y)
		if !near(g.current.Origin.X, g.next.Origin.X) || !near(g.current.Origin.Y, g.next.Origin.Y) {
			changes = append(changes, g.next)
		}
	}
	if len(changes) == 0 {
		fmt.Fprintln(out, "当前布局已符合指定顺序。")
	}
	return changes
}

func commitArrangement(query func(any, ...string) error, layout arrangeLayout, changes []placement, out io.Writer) error {
	payload, err := json.Marshal(struct {
		Placements []placement `json:"placements"`
	}{changes})
	if err != nil {
		return err
	}
	// Online state can change independently of the saved layout revision.
	// Recheck both right before submitting the single multi-device edit.
	var latest arrangeLayout
	if err := query(&latest, "layout", "show"); err != nil {
		return err
	}
	if !reflect.DeepEqual(layout, latest) {
		return errors.New("显示器或设备状态刚刚发生变化，请重新预览后应用")
	}
	var saved arrangeLayout
	if err := query(&saved, "call", "save_layout", string(payload)); err != nil {
		return err
	}
	if len(saved.Geometry.Screens) == 0 {
		return errors.New("后台返回的布局缺少屏幕，请在排列界面确认保存结果")
	}
	fmt.Fprintf(out, "设备组已保存布局（修订 %d）。\n", saved.Revision)
	return nil
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.00001 }

func collectDesktops(layout arrangeLayout) (map[string]*desktopGroup, error) {
	groups := make(map[string]*desktopGroup)
	for _, s := range layout.Geometry.Screens {
		if s.Hidden || s.Disabled {
			continue
		}
		if s.Key.Device == "" || s.Size.W <= 0 || s.Size.H <= 0 || s.Display.Bounds.W <= 0 || s.Display.Bounds.H <= 0 {
			return nil, fmt.Errorf("%s 的显示器尺寸不完整，请刷新排列界面", s.Name)
		}
		scale := s.Size.W / s.Display.Bounds.W
		origin := canvasPoint{s.Origin.X - s.Display.Bounds.X*scale, s.Origin.Y - s.Display.Bounds.Y*scale}
		g := groups[s.Key.Device]
		if g == nil {
			p := placement{s.Key.Device, origin, scale}
			g = &desktopGroup{id: s.Key.Device, name: s.Name, current: p, next: p}
			groups[g.id] = g
		}
		g.online = g.online || s.Online
		g.screens = append(g.screens, canvasRect{s.Origin, s.Size})
	}
	return groups, nil
}

func arrangeDevices(layout arrangeLayout, names []string, vertical bool) ([]*desktopGroup, error) {
	if len(names) < 2 {
		return nil, errors.New(arrangeUsage)
	}
	groups, err := collectDesktops(layout)
	if err != nil {
		return nil, err
	}
	var ordered []*desktopGroup
	used := make(map[string]bool)
	for _, name := range names {
		match := groups[strings.ToLower(name)]
		if match == nil {
			for _, g := range groups {
				if strings.EqualFold(g.name, name) {
					if match != nil {
						return nil, fmt.Errorf("多台电脑名为 %q，请使用 status 列出的设备 ID", name)
					}
					match = g
				}
			}
		}
		if match == nil {
			return nil, fmt.Errorf("未找到具有可见屏幕的电脑 %q，请检查 status 中的名称", name)
		}
		if used[match.id] {
			return nil, fmt.Errorf("设备 %q 在同一布局中出现了两次", name)
		}
		if !match.online {
			return nil, fmt.Errorf("%s 已离线，请连接后再排列", name)
		}
		if len(ordered) > 0 {
			left := ordered[len(ordered)-1]
			end := edgePoint(left.screens, vertical, true)
			start := edgePoint(match.screens, vertical, false)
			match.next.Origin.X += end.X - start.X + left.next.Origin.X - left.current.Origin.X
			match.next.Origin.Y += end.Y - start.Y + left.next.Origin.Y - left.current.Origin.Y
		}
		ordered = append(ordered, match)
		used[match.id] = true
	}
	// Other devices keep their positions, including remembered offline ones.
	for _, moved := range ordered[1:] {
		for _, r := range moved.screens {
			r.X += moved.next.Origin.X - moved.current.Origin.X
			r.Y += moved.next.Origin.Y - moved.current.Origin.Y
			for _, other := range groups {
				if used[other.id] {
					continue
				}
				for _, fixed := range other.screens {
					if math.Min(r.X+r.W, fixed.X+fixed.W)-math.Max(r.X, fixed.X) > 0.00001 &&
						math.Min(r.Y+r.H, fixed.Y+fixed.H)-math.Max(r.Y, fixed.Y) > 0.00001 {
						return nil, fmt.Errorf("排列后 %s 与 %s 的屏幕重叠，请将两台电脑都列入排列或在图形界面调整", moved.name, other.name)
					}
				}
			}
		}
	}
	return ordered, nil
}

// Snap actual monitor edges, even in an L-shaped group whose bounding box
// has an empty corner. For tied edges choose the topmost/leftmost monitor.
func edgePoint(screens []canvasRect, vertical, far bool) canvasPoint {
	var edge, center float64
	for i, r := range screens {
		start, width, crossCenter := r.X, r.W, r.Y+r.H/2
		if vertical {
			start, width, crossCenter = r.Y, r.H, r.X+r.W/2
		}
		at := start
		if far {
			at += width
		}
		if i == 0 || (far && at > edge) || (!far && at < edge) || (at == edge && crossCenter < center) {
			edge, center = at, crossCenter
		}
	}
	if vertical {
		return canvasPoint{center, edge}
	}
	return canvasPoint{edge, center}
}
