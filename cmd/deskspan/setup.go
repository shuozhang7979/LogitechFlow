package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// The wizard keeps the snapshot used for the preview until commit, including
// while the user reads it. Both the CLI and wizard use the same layout edit.
func runSetup(agent Agent, input io.Reader, out io.Writer) error {
	return setupSession(agent.Query, input, out)
}

func setupSession(query func(any, ...string) error, input io.Reader, out io.Writer) error {
	fmt.Fprintln(out, "DeskSpan · 多屏排列")
	var layout arrangeLayout
	if err := query(&layout, "layout", "show"); err != nil {
		return err
	}
	all, err := collectDesktops(layout)
	if err != nil {
		return err
	}
	var choices []*desktopGroup
	for _, g := range all {
		if g.online {
			choices = append(choices, g)
		}
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].name == choices[j].name {
			return choices[i].id < choices[j].id
		}
		return choices[i].name < choices[j].name
	})
	for i, g := range choices {
		fmt.Fprintf(out, "  %d. %q（%d 屏，ID %.12s）\n", i+1, g.name, len(g.screens), g.id)
	}
	if len(choices) < 2 {
		return errors.New("排列需要至少两台在线且已启用屏幕的电脑，请在 Oink 中完成配对并开启共享")
	}
	scanner := bufio.NewScanner(input)
	fmt.Fprint(out, "排列方向 [1 横排 / 2 纵排]：")
	if !scanner.Scan() {
		return scanner.Err()
	}
	direction := strings.TrimSpace(scanner.Text())
	if direction != "1" && direction != "2" {
		return errors.New("请输入 1 或 2")
	}
	axis := "从左到右"
	if direction == "2" {
		axis = "从上到下"
	}
	fmt.Fprintf(out, "按%s的顺序输入设备编号，用空格分隔（例如 2 1）：", axis)
	if !scanner.Scan() {
		return scanner.Err()
	}
	var ids []string
	for _, entry := range strings.Fields(scanner.Text()) {
		n, err := strconv.Atoi(entry)
		if err != nil || n < 1 || n > len(choices) {
			return fmt.Errorf("未知设备编号 %q", entry)
		}
		ids = append(ids, choices[n-1].id)
	}
	if len(ids) < 2 {
		return errors.New("请至少选择两台电脑")
	}
	ordered, err := arrangeDevices(layout, ids, direction == "2")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%s的排列预览\n", axis)
	changes := previewArrangement(ordered, out)
	if len(changes) == 0 {
		return nil
	}
	fmt.Fprint(out, "按 Y 并回车保存到设备组，直接回车退出：")
	if !scanner.Scan() {
		return scanner.Err()
	}
	if !strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
		fmt.Fprintln(out, "已退出预览。")
		return nil
	}
	return commitArrangement(query, layout, changes, out)
}
