package main

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlan(t *testing.T) {
	tests := []struct {
		name, text     string
		keep, relative bool
		want           []span
		bad            bool
	}{
		{"用户示例", "2:00 - 3:00", false, false, []span{{96, 120}, {180, 336}}, false},
		{"毫秒和相对时间", "0:24.500 - 0:25.500\n0:25 - 0:27", false, true, []span{{96, 120.5}, {123, 336}}, false},
		{"保留片段排序去重", "3:00 - 4:00\n1:36 - 2:00\n1:50 - 2:00", true, false, []span{{96, 120}, {180, 240}}, false},
		{"未删除", "", false, false, []span{{96, 336}}, false},
		{"删除全部", "1:36 - 5:36", false, false, nil, true},
		{"越界", "1:00 - 2:00", false, false, nil, true},
		{"倒序", "3:00 - 2:00", false, false, nil, true},
		{"空保留", "", true, false, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := plan(96, 336, tt.text, tt.keep, tt.relative)
			if (err != nil) != tt.bad {
				t.Fatalf("error = %v", err)
			}
			if !tt.bad && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}
func TestParseTime(t *testing.T) {
	for input, want := range map[string]float64{"1:36": 96, "5:36": 336, "1:02:03.125": 3723.125, "0.5": 0.5, "120": 120} {
		got, err := parseTime(input)
		if err != nil || got != want {
			t.Fatalf("%q: %v %v", input, got, err)
		}
	}
	for _, input := range []string{"", "NaN", "Inf", "-1", "1:60", "1::2", "1:2:3:4", "1e2", "1.5:00"} {
		if _, err := parseTime(input); err == nil {
			t.Errorf("accepted invalid %q", input)
		}
	}
}

func TestRenderIntegration(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	for _, audio := range []bool{true, false} {
		t.Run(map[bool]string{true: "with_audio", false: "silent"}[audio], func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "source.mp4")
			output := filepath.Join(dir, "result.mp4")
			args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x120:rate=25:duration=4"}
			if audio {
				args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=4", "-c:a", "aac")
			}
			args = append(args, "-c:v", "libx264", "-g", "100", "-pix_fmt", "yuv420p", input)
			if b, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %v %s", err, b)
			}
			ctx := context.Background()
			info, err := probe(ctx, ffprobe, input)
			if err != nil {
				t.Fatal(err)
			}
			if info.Audio != audio {
				t.Fatalf("audio detection: %v", info)
			}
			// Both cuts fall between keyframes; retained duration is 1.52 seconds.
			if err := render(ctx, ffmpeg, input, output, []span{{0.52, 1.04}, {2, 3}}, audio, false, func(int, int) {}); err != nil {
				t.Fatal(err)
			}
			result, err := probe(ctx, ffprobe, output)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(result.Duration-1.52) > 0.12 || result.Audio != audio {
				t.Fatalf("unexpected result: %+v", result)
			}
			fastOutput := filepath.Join(dir, "fast.mp4")
			if err := render(ctx, ffmpeg, input, fastOutput, []span{{0.52, 1.04}, {2, 3}}, audio, true, func(int, int) {}); err != nil {
				t.Fatal(err)
			}
			fastInfo, err := probe(ctx, ffprobe, fastOutput)
			if err != nil || fastInfo.Duration <= 0 || fastInfo.Audio != audio {
				t.Fatalf("fast output: %+v %v", fastInfo, err)
			}
			if b, err := exec.Command(ffmpeg, "-v", "error", "-i", fastOutput, "-f", "null", "-").CombinedOutput(); err != nil || len(b) != 0 {
				t.Fatalf("fast output decode: %v %s", err, b)
			}

			before, _ := os.ReadFile(output)
			if err := render(ctx, ffmpeg, input, output, []span{{0, 1}}, audio, false, func(int, int) {}); err == nil {
				t.Fatal("overwrote output")
			}
			after, _ := os.ReadFile(output)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("existing output changed")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			cancelOutput := filepath.Join(dir, "cancel.mp4")
			if err := render(cancelled, ffmpeg, input, cancelOutput, []span{{0, 1}}, audio, false, func(int, int) {}); err == nil {
				t.Fatal("expected cancellation")
			}
			if _, err := os.Stat(cancelOutput); !os.IsNotExist(err) {
				t.Fatal("cancelled output exists")
			}
			temps, _ := filepath.Glob(filepath.Join(dir, ".video-cut-*"))
			if len(temps) > 0 {
				t.Fatalf("temporary files leaked: %v", temps)
			}
		})
	}
}
