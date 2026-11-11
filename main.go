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
	w := a.NewWindow("视频剪辑小助手 · 指定切分点，勾选保留片段")
	w.Resize(fyne.NewSize(880, 760))
	file := widget.NewEntry()
	file.SetPlaceHolder("选择原视频，或已经截取过的视频")
	dir := widget.NewEntry()
	dir.SetPlaceHolder("默认保存到原视频所在目录")
	name := widget.NewEntry()
	name.SetPlaceHolder("默认：原文件名_edited.mp4")
	videoInfo := widget.NewLabel("选择视频后自动读取时长，切分点使用播放器显示的时间。")
	videoInfo.Wrapping = fyne.TextWrapWord
	var duration float64
	var loadCancel context.CancelFunc
	var loadID int
	loadErr := fmt.Errorf("请先选择视频")
	points := widget.NewMultiLineEntry()
	points.SetMinRowsVisible(3)
	points.SetPlaceHolder("输入切分点，每行一个，也可用逗号分隔。例如：2:00, 3:00, 4:12.500, 4:14")
	preview := widget.NewLabel("")
	preview.Wrapping = fyne.TextWrapWord
	segmentList := container.NewVBox()
	var parts []span
	selected := make(map[span]bool)
	var splitErr error
	makePlan := func() ([]span, error) {
		if splitErr != nil {
			return nil, splitErr
		}
		return selectedPlan(parts, selected)
	}
	updatePreview := func() {
		p, err := makePlan()
		if err != nil {
			preview.SetText(err.Error())
			return
		}
		var b strings.Builder
		total := 0.0
		count := 0
		for _, r := range parts {
			if selected[r] {
				count++
			}
		}
		for i, r := range p {
			fmt.Fprintf(&b, "%d. %s → %s\n", i+1, stamp(r.Start), stamp(r.End))
			total += r.End - r.Start
		}
		preview.SetText(fmt.Sprintf("已保留 %d / %d 段，共 %s（相邻保留段自动连成一段）\n%s", count, len(parts), stamp(total), b.String()))
	}
	refresh := func() {
		var next []span
		err := loadErr
		if err == nil {
			next, err = splitAt(duration, points.Text)
		}
		splitErr = err
		segmentList.RemoveAll()
		if err != nil {
			parts = nil
			updatePreview()
			return
		}
		nextSelected := make(map[span]bool)
		for _, r := range next {
			keep, exists := selected[r]
			if !exists {
				keep = true
			}
			nextSelected[r] = keep
		}
		selected = nextSelected
		parts = next
		for i, r := range parts {
			r := r
			check := widget.NewCheck(fmt.Sprintf("保留第 %d 段　%s → %s　（%s）", i+1, stamp(r.Start), stamp(r.End), stamp(r.End-r.Start)), nil)
			check.SetChecked(selected[r])
			check.OnChanged = func(keep bool) { selected[r] = keep; updatePreview() }
			segmentList.Add(check)
		}
		updatePreview()
	}
	points.OnChanged = func(string) { refresh() }
	file.OnChanged = func(path string) {
		if loadCancel != nil {
			loadCancel()
			loadCancel = nil
		}
		loadID++
		id := loadID
		selected = make(map[span]bool)
		duration = 0
		loadErr = fmt.Errorf("请先选择有效的视频文件")
		path = strings.TrimSpace(path)
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			videoInfo.SetText("请选择有效的视频文件，软件会自动读取完整时长。")
			refresh()
			return
		}
		tool, err := findTool("ffprobe")
		if err != nil {
			loadErr = err
			videoInfo.SetText(err.Error())
			refresh()
			return
		}
		loadErr = fmt.Errorf("正在读取视频时长，请稍候…")
		videoInfo.SetText(loadErr.Error())
		refresh()
		ctx, stop := context.WithCancel(context.Background())
		loadCancel = stop
		go func() {
			defer stop()
			info, err := probe(ctx, tool, path)
			fyne.Do(func() {
				if id != loadID {
					return
				}
				loadCancel = nil
				loadErr = err
				if err != nil {
					videoInfo.SetText(err.Error())
				} else {
					duration = info.Duration
					videoInfo.SetText("视频时长：" + stamp(duration) + " · 切分点使用原视频时间")
				}
				refresh()
			})
		}()
	}
	setAll := func(keep bool) {
		for _, obj := range segmentList.Objects {
			obj.(*widget.Check).SetChecked(keep)
		}
	}
	selectionButtons := container.NewHBox(widget.NewButton("全部保留", func() { setAll(true) }), widget.NewButton("全部取消", func() { setAll(false) }))
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
		rangeEnd := duration
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
		if loadCancel != nil {
			loadCancel()
		}
		w.SetCloseIntercept(nil)
		w.Close()
	})
	form := widget.NewForm(widget.NewFormItem("视频文件", container.NewBorder(nil, nil, nil, browse, file)), widget.NewFormItem("保存目录", container.NewBorder(nil, nil, nil, folder, dir)), widget.NewFormItem("输出文件名", name))
	help := widget.NewLabel("输入切分点 2:00、3:00，会生成“开头 → 2:00”“2:00 → 3:00”“3:00 → 结尾”三段。取消中间一段，即可合并首尾。\n修改切分点后自动更新列表；新生成的段默认保留，请重新检查勾选。")
	help.Wrapping = fyne.TextWrapWord
	body := container.NewVBox(widget.NewLabelWithStyle("指定切分点，勾选要保留的段", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), help, form, videoInfo, widget.NewLabelWithStyle("切分时间点 · 支持毫秒，自动排序去重", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), points, widget.NewSeparator(), widget.NewLabelWithStyle("勾选保留片段（显示原视频时间）", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), selectionButtons, segmentList, widget.NewSeparator(), preview)
	footer := container.NewVBox(fastCheck, modeHelp, container.NewGridWithColumns(2, export, cancelBtn), bar, status)
	w.SetContent(container.NewPadded(container.NewBorder(nil, footer, nil, nil, container.NewVScroll(body))))
	refresh()
	w.ShowAndRun()
}
