package main

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

func fixtureScreen(device, name string, x, y, w, h, localX, localY, scale float64) arrangeScreen {
	var s arrangeScreen
	s.Key.Device, s.Name, s.Online = device, name, true
	s.Origin = canvasPoint{x, y}
	s.Size = canvasSize{w, h}
	s.Display.Bounds = canvasRect{canvasPoint{localX, localY}, canvasSize{w / scale, h / scale}}
	return s
}

func twoDesktops() arrangeLayout {
	var layout arrangeLayout
	layout.Revision = 7
	layout.Geometry.Screens = []arrangeScreen{
		fixtureScreen("aa", "Windows", 0, 0, 500, 300, 0, 0, 0.25),
		fixtureScreen("aa", "Windows", 500, -100, 400, 300, 2000, -400, 0.25),
		fixtureScreen("bb", "Mac", 1500, 700, 400, 300, 0, 0, 0.5),
		fixtureScreen("bb", "Mac", 1100, 800, 400, 300, -800, 200, 0.5),
	}
	return layout
}

func TestArrangePlacesRealEdgesAndRetainsDeviceScale(t *testing.T) {
	for _, test := range []struct {
		vertical bool
		want     canvasPoint
	}{{false, canvasPoint{1300, -200}}, {true, canvasPoint{50, 300}}} {
		groups, err := arrangeDevices(twoDesktops(), []string{"Windows", "Mac"}, test.vertical)
		if err != nil {
			t.Fatal(err)
		}
		if groups[0].current != groups[0].next {
			t.Fatal("first device moved")
		}
		want := placement{"bb", test.want, 0.5}
		if got := groups[1].next; got != want {
			t.Fatalf("vertical=%v: got %+v, want %+v", test.vertical, got, want)
		}
	}
}

func TestArrangeRejectsOverlapsAndAmbiguousSelection(t *testing.T) {
	layout := twoDesktops()
	// An unlisted offline computer retains its reserved position.
	third := fixtureScreen("cc", "Another", 950, -90, 400, 190, 0, 0, 1)
	third.Online = false
	layout.Geometry.Screens = append(layout.Geometry.Screens, third)
	if _, err := arrangeDevices(layout, []string{"Windows", "Mac"}, false); err == nil {
		t.Fatal("reserved screen would be covered")
	}
	layout.Geometry.Screens[4].Hidden = true
	if _, err := arrangeDevices(layout, []string{"Windows", "Mac"}, false); err != nil {
		t.Fatal(err)
	}
	layout.Geometry.Screens[4].Hidden = false
	layout.Geometry.Screens[4].Name = "Mac"
	if _, err := arrangeDevices(layout, []string{"Windows", "Mac"}, false); err == nil {
		t.Fatal("ambiguous name")
	}
	if _, err := arrangeDevices(layout, []string{"aa", "cc"}, false); err == nil {
		t.Fatal("offline selected")
	}
	if _, err := arrangeDevices(layout, []string{"aa", "Windows"}, false); err == nil {
		t.Fatal("duplicate selected")
	}
}

func TestArrangePreviewAndSingleCommit(t *testing.T) {
	for _, test := range []struct {
		name           string
		apply, changed bool
		wantCalls      []string
	}{
		{"preview", false, false, []string{"layout"}},
		{"save", true, false, []string{"layout", "layout", "call"}},
		{"changed", true, true, []string{"layout", "layout"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			layout := twoDesktops()
			var calls []string
			query := func(out any, args ...string) error {
				calls = append(calls, args[0])
				if args[0] == "call" {
					var params struct{ Placements []placement }
					if args[1] != "save_layout" {
						t.Fatal(args)
					}
					if err := json.Unmarshal([]byte(args[2]), &params); err != nil {
						t.Fatal(err)
					}
					if want := []placement{{"bb", canvasPoint{1300, -200}, 0.5}}; !reflect.DeepEqual(params.Placements, want) {
						t.Fatal(params)
					}
					layout.Revision++
				}
				if test.changed && len(calls) == 2 {
					layout.Revision++
				}
				*out.(*arrangeLayout) = layout
				return nil
			}
			args := []string{"row", "Windows", "Mac"}
			if test.apply {
				args = append([]string{"--apply"}, args...)
			}
			err := arrangeCommand(query, args, io.Discard)
			if (err != nil) != test.changed {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, test.wantCalls) {
				t.Fatal(strings.Join(calls, ","))
			}
		})
	}
}
