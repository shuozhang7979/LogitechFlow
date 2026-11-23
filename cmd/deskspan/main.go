package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const usage = `DeskSpan · 键鼠共享工作台

  deskspan status                   查看设备、显示器位置和共享开关
  deskspan setup                    根据桌面的设备顺序设置多屏布局
  deskspan arrange [--apply] row|column DEVICE DEVICE [DEVICE ...]
                                    预览横向或纵向排列，加 --apply 保存

可加 --agent PATH 指定 oink-agent 可执行文件。默认使用本机已安装的 Oink。
`

type Agent struct{ Path string }

// Query runs the installed CLI, which handles authenticated local IPC itself.
func (a Agent) Query(out any, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.Path, args...)
	cmd.Stderr = io.Discard // Upstream diagnostic output may include machine-specific data.
	data, err := cmd.Output()
	if ctx.Err() != nil {
		return errors.New("本机 Oink 后台响应超时")
	}
	if err != nil {
		return fmt.Errorf("Oink 命令执行失败（%w）；请确认应用已启动，并使用当前用户运行 DeskSpan", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("无法解析 Oink 返回的数据，请核对软件版本（%w）", err)
	}
	return nil
}

func findAgent(override string) (Agent, error) {
	if override != "" {
		path, err := filepath.Abs(override)
		if err != nil {
			return Agent{}, err
		}
		if path, err = exec.LookPath(path); err != nil {
			return Agent{}, fmt.Errorf("--agent 指定的文件无法执行（%w）", err)
		}
		return Agent{path}, nil
	}
	var paths []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramW6432", "ProgramFiles"} {
			if dir := os.Getenv(env); filepath.IsAbs(dir) {
				paths = append(paths, filepath.Join(dir, "Oink", "oink-agent.exe"))
			}
		}
	case "darwin":
		paths = append(paths, "/Applications/Oink.app/Contents/MacOS/oink-agent")
	}
	paths = append(paths, "oink-agent")
	for _, path := range paths {
		if resolved, err := exec.LookPath(path); err == nil {
			return Agent{resolved}, nil
		}
	}
	return Agent{}, errors.New("找不到 oink-agent。请安装 Oink，或用 --agent 指定其可执行文件")
}

func parseAgentFlag(args []string) (string, []string, error) {
	var remaining []string
	var path string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg != "--agent" && !strings.HasPrefix(arg, "--agent=") {
			remaining = append(remaining, arg)
			continue
		}
		if path != "" {
			return "", nil, errors.New("--agent 只能指定一次")
		}
		if arg == "--agent" {
			i++
			if i == len(args) {
				return "", nil, errors.New("--agent 需要一个可执行文件路径")
			}
			path = args[i]
		} else {
			path = strings.TrimPrefix(arg, "--agent=")
		}
		if path == "" {
			return "", nil, errors.New("--agent 路径为空")
		}
	}
	return path, remaining, nil
}

func run(args []string, out io.Writer) error {
	path, args, err := parseAgentFlag(args)
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, usage)
		return nil
	}
	switch args[0] {
	case "status":
		if len(args) > 1 {
			return errors.New("用法为 deskspan status [--agent PATH]")
		}
	case "setup":
		if len(args) > 1 {
			return errors.New("用法为 deskspan setup [--agent PATH]")
		}
	case "arrange":
		// Arrange parses its help and arguments before contacting the agent.
		if len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
			return runArrange(Agent{}, args[1:], out)
		}
	default:
		return fmt.Errorf("未知命令 %q，请运行 deskspan --help", args[0])
	}
	agent, err := findAgent(path)
	if err != nil {
		return err
	}
	if args[0] == "arrange" {
		return runArrange(agent, args[1:], out)
	}
	if args[0] == "setup" {
		return runSetup(agent, os.Stdin, out)
	}
	return showStatus(agent.Query, out)
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
