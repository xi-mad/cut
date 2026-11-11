package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

func main() {
	a := app.NewWithID("com.videocut.app")
	w := a.NewWindow("视频剪辑小助手 · 删掉不要的，剩下自动合并")
	w.Resize(fyne.NewSize(880, 760))
	file := widget.NewEntry()
	file.SetPlaceHolder("选择原视频，或已经截取过的视频")
	dir := widget.NewEntry()
	dir.SetPlaceHolder("默认保存到原视频所在目录")
	name := widget.NewEntry()
	name.SetPlaceHolder("默认：原文件名_edited.mp4")
	start, end := widget.NewEntry(), widget.NewEntry()
	start.SetText("0")
	end.SetPlaceHolder("必填，例如 5:36")
	mode := widget.NewSelect([]string{"删除这些片段，合并剩余内容", "只保留这些片段，按时间合并"}, nil)
	mode.SetSelectedIndex(0)
	reference := widget.NewSelect([]string{"原视频时间（与播放器显示一致）", "总范围内时间（总范围起点算 0）"}, nil)
	reference.SetSelectedIndex(0)
	ranges := widget.NewMultiLineEntry()
	ranges.SetMinRowsVisible(5)
	ranges.SetPlaceHolder("每行一段，例如：\n2:00 - 3:00\n4:12.500 - 4:14\n删除模式留空时，导出整个总范围")
	preview := widget.NewLabel("")
	preview.Wrapping = fyne.TextWrapWord
	makePlan := func() ([]span, error) {
		s, err := parseTime(start.Text)
		if err != nil {
			return nil, err
		}
		e, err := parseTime(end.Text)
		if err != nil {
			return nil, fmt.Errorf("请填写有效的总范围结束时间")
		}
		return plan(s, e, ranges.Text, mode.SelectedIndex() == 1, reference.SelectedIndex() == 1)
	}
	refresh := func() {
		p, err := makePlan()
		if err != nil {
			preview.SetText(err.Error())
			return
		}
		var b strings.Builder
		total := 0.0
		for i, r := range p {
			fmt.Fprintf(&b, "%d. %s → %s\n", i+1, stamp(r.Start), stamp(r.End))
			total += r.End - r.Start
		}
		preview.SetText(fmt.Sprintf("将按原视频顺序合并 %d 段，共 %s\n%s", len(p), stamp(total), b.String()))
	}
	start.OnChanged = func(string) { refresh() }
	end.OnChanged = start.OnChanged
	ranges.OnChanged = start.OnChanged
	mode.OnChanged = start.OnChanged
	reference.OnChanged = start.OnChanged
	browse := widget.NewButton("选择视频", func() {
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if r == nil {
				return
			}
			file.SetText(r.URI().Path())
			r.Close()
		}, w)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".ts"}))
		d.Show()
		d.Resize(fyne.NewSize(700, 480))
	})
	folder := widget.NewButton("选择目录", func() {
		dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if u != nil {
				dir.SetText(u.Path())
			}
		}, w).Show()
	})
	status := widget.NewLabel("就绪 · 支持 秒、分:秒、时:分:秒，以及毫秒")
	status.Wrapping = fyne.TextWrapWord
	bar := widget.NewProgressBarInfinite()
	bar.Hide()
	modeHelp := widget.NewLabel("默认精确剪辑：重新编码，适合删除一两秒的片段；原视频不会被覆盖。")
	modeHelp.Wrapping = fyne.TextWrapWord
	fastCheck := widget.NewCheck("关键帧快速剪辑（切点可能有偏差）", func(fast bool) {
		if fast {
			modeHelp.SetText("快速模式：视频不重新编码，音频转 AAC。切点可能提前到上一关键帧，可能保留部分要删除的内容，实际时长可能与预览不同。")
		} else {
			modeHelp.SetText("默认精确剪辑：重新编码，适合删除一两秒的片段；原视频不会被覆盖。")
		}
	})
	var cancel context.CancelFunc
	cancelBtn := widget.NewButton("取消导出", func() {
		if cancel != nil {
			cancel()
			status.SetText("正在取消…")
		}
	})
	cancelBtn.Disable()
	var export *widget.Button
	export = widget.NewButton("合并导出 MP4", func() {
		p, err := makePlan()
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		input, err := filepath.Abs(strings.TrimSpace(file.Text))
		if err != nil || strings.TrimSpace(file.Text) == "" {
			dialog.ShowError(fmt.Errorf("请先选择视频"), w)
			return
		}
		if info, err := os.Stat(input); err != nil || !info.Mode().IsRegular() {
			dialog.ShowError(fmt.Errorf("视频文件不存在或不是普通文件"), w)
			return
		}
		ffmpeg, err := findTool("ffmpeg")
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		ffprobe, err := findTool("ffprobe")
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		outputDir := strings.TrimSpace(dir.Text)
		if outputDir == "" {
			outputDir = filepath.Dir(input)
		}
		if info, err := os.Stat(outputDir); err != nil || !info.IsDir() {
			dialog.ShowError(fmt.Errorf("输出目录不存在"), w)
			return
		}
		outputName := strings.TrimSpace(name.Text)
		if outputName == "" {
			outputName = strings.TrimSuffix(filepath.Base(input), filepath.Ext(input)) + "_edited.mp4"
		}
		if strings.ContainsAny(outputName, "/\\:") || outputName == "." || outputName == ".." {
			dialog.ShowError(fmt.Errorf("输出文件名不能包含路径或冒号"), w)
			return
		}
		if !strings.HasSuffix(strings.ToLower(outputName), ".mp4") {
			outputName += ".mp4"
		}
		output, err := filepath.Abs(filepath.Join(outputDir, outputName))
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			dialog.ShowError(fmt.Errorf("输出文件已存在或无法访问，请换一个文件名"), w)
			return
		}
		fast := fastCheck.Checked
		cutMode := "精确剪辑"
		if fast {
			cutMode = "关键帧快速剪辑"
		}
		rangeEnd, _ := parseTime(end.Text)
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		export.Disable()
		cancelBtn.Enable()
		bar.Show()
		status.SetText("正在检查视频…")
		go func() {
			defer stop()
			info, err := probe(ctx, ffprobe, input)
			if err == nil {
				if rangeEnd > info.Duration+0.001 {
					err = fmt.Errorf("片段超出视频时长 %s", stamp(info.Duration))
				}
			}
			if err == nil {
				err = render(ctx, ffmpeg, input, output, p, info.Audio, fast, func(i, n int) {
					fyne.Do(func() { status.SetText(fmt.Sprintf("正在%s第 %d / %d 段，请稍候…", cutMode, i, n)) })
				})
			}
			cancelled := ctx.Err() != nil
			fyne.Do(func() {
				cancel = nil
				export.Enable()
				cancelBtn.Disable()
				bar.Hide()
				if cancelled {
					status.SetText("已取消导出")
					return
				}
				if err != nil {
					status.SetText("导出失败，请检查输入")
					dialog.ShowError(err, w)
					return
				}
				status.SetText("已合并导出：" + output)
				dialog.ShowInformation("导出完成", "剩余片段已合并到：\n"+output, w)
			})
		}()
	})
	export.Importance = widget.HighImportance
	w.SetCloseIntercept(func() {
		if cancel != nil {
			cancel()
		}
		w.SetCloseIntercept(nil)
		w.Close()
	})
	form := widget.NewForm(widget.NewFormItem("视频文件", container.NewBorder(nil, nil, nil, browse, file)), widget.NewFormItem("保存目录", container.NewBorder(nil, nil, nil, folder, dir)), widget.NewFormItem("输出文件名", name), widget.NewFormItem("总范围开始", start), widget.NewFormItem("总范围结束", end), widget.NewFormItem("剪辑方式", mode), widget.NewFormItem("时间基准", reference))
	help := widget.NewLabel("例：总范围 1:36 → 5:36，删除 2:00 - 3:00，自动得到 1:36 → 2:00 和 3:00 → 5:36。\n如果打开的是已截取的小视频，直接使用小视频播放器上的时间。重叠片段会自动合并。")
	help.Wrapping = fyne.TextWrapWord
	body := container.NewVBox(widget.NewLabelWithStyle("删掉不要的，剩下连起来", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), help, form, widget.NewLabelWithStyle("片段清单 · 每行填写一个时间段", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), ranges, widget.NewSeparator(), widget.NewLabelWithStyle("合并预览（显示原视频时间）", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), preview)
	footer := container.NewVBox(fastCheck, modeHelp, container.NewGridWithColumns(2, export, cancelBtn), bar, status)
	w.SetContent(container.NewPadded(container.NewBorder(nil, footer, nil, nil, container.NewVScroll(body))))
	refresh()
	w.ShowAndRun()
}
