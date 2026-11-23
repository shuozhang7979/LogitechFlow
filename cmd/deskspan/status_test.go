package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func queryFixture(responses map[string]string) statusQuery {
	return func(out any, args ...string) error {
		key := strings.Join(args, " ")
		raw, found := responses[key]
		if !found {
			return fmt.Errorf("missing response for %q", key)
		}
		return json.Unmarshal([]byte(raw), out)
	}
}

func TestStatusMultipleDisplaysAndClipboardPolicy(t *testing.T) {
	// Same public serde fields as oink-agent v0.3.6. Extra fields are ignored.
	query := queryFixture(map[string]string{
		"call overview":        `{"this_device":{"id":"a","name":"Studio","os":"macos","app_version":"0.3.6"},"service":"running","sharing_enabled":true,"alerts":[]}`,
		"call group":           `{"members":[{"device":{"id":"a","name":"Studio","os":"macos"},"is_self":true,"state":{"type":"online"},"sharing":"active"},{"device":{"id":"b","name":"Windows","os":"windows"},"is_self":false,"state":{"type":"online"},"sharing":"active"}]}`,
		"layout show":          `{"geometry":{"screens":[{"device_name":"Studio","display":{"name":"Retina","pixel_width":3024,"pixel_height":1964,"is_primary":true},"origin":{"x":-120,"y":-30},"online":true},{"device_name":"Studio","display":{"name":"4K"},"origin":{"x":320,"y":30},"hidden":true,"online":true},{"device_name":"Windows","display":{"name":"Vertical"},"device_disabled":true,"online":false}]}}`,
		"files settings":       `{"send":false,"receive":true,"files":true}`,
		"file-provider status": `{"state":"not_enabled","detail":"PRIVATE DEBUG INFORMATION"}`,
	})
	var out strings.Builder
	if err := showStatus(query, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ID b", "3 块屏幕", "Retina", "3024×1964 px", "(-120.0, -30.0)", "主屏", "4K", "已隐藏", "Vertical", "已禁止进入", "离线", "发送 关闭", "接收 开启", "not_enabled"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in output\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "PRIVATE") {
		t.Error("printed provider diagnostics")
	}
}

func TestStatusWithoutBackendStillReportsNativeDisplays(t *testing.T) {
	offline := errors.New("backend offline")
	query := func(out any, args ...string) error {
		if len(args) == 1 && args[0] == "displays" {
			return json.Unmarshal([]byte(`[{"name":"USB-C","pixel_width":1920,"pixel_height":1080,"bounds":{"x":-1920,"y":0}}]`), out)
		}
		return offline
	}
	var out strings.Builder
	if err := showStatus(query, &out); !errors.Is(err, offline) {
		t.Fatalf("expected offline error, got %v", err)
	}
	if !strings.Contains(out.String(), `"USB-C"`) || !strings.Contains(out.String(), "系统坐标 (-1920, 0)") {
		t.Error(out.String())
	}
}

func TestStatusReportsActiveBidirectionalFileTransfers(t *testing.T) {
	query := queryFixture(map[string]string{
		"call overview":        `{"this_device":{"id":"a","name":"Studio","os":"macos","app_version":"0.3.6"},"service":"running","sharing_enabled":true,"alerts":[]}`,
		"call group":           `{"members":[{"device":{"id":"a","name":"Studio","os":"macos"},"is_self":true,"state":{"type":"online"},"sharing":"active"},{"device":{"id":"b","name":"Windows","os":"windows"},"is_self":false,"state":{"type":"online"},"sharing":"active"}]}`,
		"layout show":          `{"geometry":{"screens":[]}}`,
		"files settings":       `{"send":true,"receive":true,"files":true}`,
		"file-provider status": `{"state":"available"}`,
		"call file_transfers":  `[{"id":"tx:1","direction":"sending","kind":"drag_and_drop","from_name":"Windows","current_file":"dataset.tar","done":536870912,"total":1073741824,"files_done":1,"files":4,"speed_bps":134217728,"eta_secs":4,"phase":"downloading","message":null},{"id":"rx:2","direction":"receiving","kind":"clipboard","from_name":"Windows","current_file":"slides.pdf","done":10485760,"total":10485760,"files_done":1,"files":1,"speed_bps":0,"phase":"ready","message":null}]`,
	})
	var out strings.Builder
	if err := showStatus(query, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"文件传输 · 2 项",
		`发送 -> "Windows"`,
		"传输中 · dataset.tar · 1/4 个文件 · 512.0 MB / 1.00 GB (50%) · 128.0 MB/s · 剩余 4s",
		`接收 <- "Windows"`,
		"已完成 · slides.pdf · 10.0 MB / 10.0 MB (100%)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in output\n%s", want, out.String())
		}
	}
}
