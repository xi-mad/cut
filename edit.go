package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type span struct{ Start, End float64 }

func parseTime(s string) (float64, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) > 3 || len(parts) == 0 {
		return 0, fmt.Errorf("时间格式应为 秒、分:秒 或 时:分:秒")
	}
	total := 0.0
	for i, p := range parts {
		if p == "" || (i < len(parts)-1 && strings.Contains(p, ".")) {
			return 0, fmt.Errorf("时间不能为空")
		}
		for _, c := range p {
			if (c < '0' || c > '9') && !(c == '.' && i == len(parts)-1) {
				return 0, fmt.Errorf("无效时间：%s", s)
			}
		}
		n, err := strconv.ParseFloat(p, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || (i > 0 && n >= 60) {
			return 0, fmt.Errorf("无效时间：%s", s)
		}
		total = total*60 + n
	}
	return total, nil
}
func stamp(t float64) string {
	ms := int64(math.Round(t * 1000))
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}
func seconds(t float64) string { return strconv.FormatFloat(t, 'f', 6, 64) }

// splitAt returns every interval, including the leading and trailing intervals.
func splitAt(end float64, text string) ([]span, error) {
	if end <= 0 {
		return nil, fmt.Errorf("无法获取有效的视频时长")
	}
	points := []float64{0, end}
	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		v, err := parseTime(token)
		if err != nil {
			return nil, fmt.Errorf("切分点 %q：%w", token, err)
		}
		if v < 0 || v > end {
			return nil, fmt.Errorf("切分点 %s 超出视频时长", token)
		}
		points = append(points, v)
	}
	sort.Float64s(points)
	var result []span
	cursor := points[0]
	for _, v := range points[1:] {
		if v > cursor {
			result = append(result, span{cursor, v})
			cursor = v
		}
	}
	return result, nil
}

// Adjacent selected intervals can be rendered in one pass without an artificial splice.
func selectedPlan(parts []span, selected map[span]bool) ([]span, error) {
	var result []span
	for _, r := range parts {
		if !selected[r] {
			continue
		}
		if len(result) > 0 && result[len(result)-1].End == r.Start {
			result[len(result)-1].End = r.End
		} else {
			result = append(result, r)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("请至少勾选一个要保留的片段")
	}
	return result, nil
}

func findTool(name string) (string, error) {
	if runtimePath, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(runtimePath), name)
		if p, err := exec.LookPath(candidate); err == nil {
			return p, nil
		}
		if p, err := exec.LookPath(candidate + ".exe"); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("./" + name); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("./" + name + ".exe"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("未找到 %s，请放在软件同目录或加入系统 PATH", name)
}

type mediaInfo struct {
	Duration float64
	Audio    bool
}

func probe(ctx context.Context, tool, input string) (mediaInfo, error) {
	data, err := exec.CommandContext(ctx, tool, "-v", "error", "-show_entries", "format=duration:stream=codec_type", "-of", "json", input).CombinedOutput()
	if err != nil {
		return mediaInfo{}, fmt.Errorf("无法读取视频：%s", data)
	}
	var info struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type string `json:"codec_type"`
		} `json:"streams"`
	}
	if err = json.Unmarshal(data, &info); err != nil {
		return mediaInfo{}, err
	}
	duration, err := strconv.ParseFloat(info.Format.Duration, 64)
	if err != nil || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return mediaInfo{}, fmt.Errorf("无法获取视频时长")
	}
	video, audio := false, false
	for _, s := range info.Streams {
		video = video || s.Type == "video"
		audio = audio || s.Type == "audio"
	}
	if !video {
		return mediaInfo{}, fmt.Errorf("文件中没有视频轨道")
	}
	return mediaInfo{duration, audio}, nil
}

// Process each retained interval with identical settings, then concatenate in source order.
// Fast mode copies video from the preceding keyframe; audio is encoded for MP4 compatibility.
// Accurate input seeking decodes from the preceding keyframe and discards frames
// before the requested time, without decoding the whole source for every interval.
func render(ctx context.Context, ffmpeg, input, output string, ranges []span, audio, fast bool, progress func(int, int)) error {
	temp, err := os.MkdirTemp(filepath.Dir(output), ".video-cut-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	run := func(args ...string) error {
		data, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if len(data) > 6000 {
				data = data[len(data)-6000:]
			}
			return fmt.Errorf("FFmpeg 执行失败：%w\n%s", err, data)
		}
		return nil
	}
	var manifest strings.Builder
	for i, r := range ranges {
		progress(i+1, len(ranges))
		name := fmt.Sprintf("part-%04d.mp4", i)
		seek := "-accurate_seek"
		if fast {
			seek = "-noaccurate_seek"
		}
		args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-ss", seconds(r.Start), seek, "-i", input, "-t", seconds(r.End - r.Start), "-map", "0:v:0"}
		if audio {
			args = append(args, "-map", "0:a:0", "-af", "apad", "-c:a", "aac")
		} else {
			args = append(args, "-an")
		}
		if fast {
			args = append(args, "-c:v", "copy", "-avoid_negative_ts", "make_zero")
		} else {
			args = append(args, "-c:v", "libx264", "-preset", "fast", "-crf", "18", "-pix_fmt", "yuv420p", "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2")
		}
		args = append(args, "-t", seconds(r.End-r.Start), filepath.Join(temp, name))
		if err := run(args...); err != nil {
			return err
		}
		fmt.Fprintf(&manifest, "file '%s'\n", name)
	}
	list := filepath.Join(temp, "segments.txt")
	if err := os.WriteFile(list, []byte(manifest.String()), 0600); err != nil {
		return err
	}
	staged := filepath.Join(temp, "result.mp4")
	if err := run("-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "concat", "-safe", "1", "-i", list, "-c", "copy", "-movflags", "+faststart", staged); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Exclusive creation avoids overwriting an existing video, even if it appeared during export.
	data, err := os.Open(staged)
	if err != nil {
		return err
	}
	defer data.Close()
	return publish(data, output)
}

func publish(src *os.File, path string) error {
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("无法保存（请确认文件名未被占用）：%w", err)
	}
	_, err = io.Copy(dst, src)
	closeErr := dst.Close()
	if err != nil || closeErr != nil {
		os.Remove(path)
		if err != nil {
			return err
		}
		return closeErr
	}
	return nil
}
