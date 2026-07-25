package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func main() {
	myApp := app.NewWithID("com.videocut.app")
	myWindow := myApp.NewWindow("🎥 极简视频剪辑器")
	myWindow.Resize(fyne.NewSize(550, 420))

	// 变量状态
	var currentDir string = "."
	var videoFiles []string

	// UI 组件定义
	dirEntry := widget.NewEntry()
	dirEntry.SetText(".")
	dirEntry.SetPlaceHolder("请选择或输入视频所在目录")

	videoSelect := widget.NewSelect([]string{}, func(selected string) {})
	videoSelect.PlaceHolder = "请选择视频文件..."

	refreshVideos := func(dir string) {
		fyne.Do(func() {
			currentDir = dir
			videoFiles = getVideosInDir(dir)
			if len(videoFiles) == 0 {
				videoSelect.Options = []string{}
				videoSelect.ClearSelected()
				videoSelect.PlaceHolder = "⚠️ 当前目录下未找到视频文件 (.mp4, .mov, .mkv, .avi)"
			} else {
				videoSelect.Options = videoFiles
				videoSelect.SetSelected(videoFiles[0])
			}
			videoSelect.Refresh()
		})
	}

	// 浏览文件夹按钮
	browseBtn := widget.NewButton("📁 浏览...", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			path := uri.Path()
			dirEntry.SetText(path)
			refreshVideos(path)
		}, myWindow)
	})

	dirEntry.OnChanged = func(path string) {
		dir := strings.TrimSpace(path)
		if dir == "" {
			dir = "."
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			refreshVideos(dir)
		}
	}

	startTimeEntry := widget.NewEntry()
	startTimeEntry.SetPlaceHolder("例: 00:00:05 或 5")

	endTimeEntry := widget.NewEntry()
	endTimeEntry.SetPlaceHolder("例: 00:01:20 或 80")

	suffixEntry := widget.NewEntry()
	suffixEntry.SetText("_cut")

	progressBar := widget.NewProgressBarInfinite()
	progressBar.Hide()

	statusLabel := widget.NewLabel("就绪")
	statusLabel.Alignment = fyne.TextAlignCenter

	var startBtn *widget.Button

	startBtn = widget.NewButton("✂️ 开始剪辑视频", func() {
		selectedVideo := videoSelect.Selected
		if selectedVideo == "" || strings.HasPrefix(selectedVideo, "⚠️") {
			dialog.ShowError(fmt.Errorf("请先选择一个有效的视频文件"), myWindow)
			return
		}

		startTime := strings.TrimSpace(startTimeEntry.Text)
		if startTime == "" {
			dialog.ShowError(fmt.Errorf("请输入开始时间"), myWindow)
			return
		}

		endTime := strings.TrimSpace(endTimeEntry.Text)
		if endTime == "" {
			dialog.ShowError(fmt.Errorf("请输入结束时间"), myWindow)
			return
		}

		suffix := strings.TrimSpace(suffixEntry.Text)
		inputFile := filepath.Join(currentDir, selectedVideo)
		ext := filepath.Ext(inputFile)
		baseName := strings.TrimSuffix(inputFile, ext)
		outputFile := fmt.Sprintf("%s%s%s", baseName, suffix, ext)

		// 禁用按钮，显示进度条
		startBtn.Disable()
		progressBar.Show()
		statusLabel.SetText("⏱️ 正在剪辑中，请稍候...")

		go func() {
			args := []string{
				"-y",
				"-ss", startTime,
				"-to", endTime,
				"-i", inputFile,
				"-c", "copy",
				outputFile,
			}

			cmd := exec.Command("ffmpeg", args...)
			output, err := cmd.CombinedOutput()

			// 切回 GUI 主线程更新 UI 状态
			fyne.Do(func() {
				startBtn.Enable()
				progressBar.Hide()
				if err != nil {
					statusLabel.SetText("❌ 剪辑失败")
					dialog.ShowError(fmt.Errorf("FFmpeg 报错:\n%s", string(output)), myWindow)
				} else {
					statusLabel.SetText("✨ 剪辑成功！")
					dialog.ShowInformation("成功", fmt.Sprintf("文件已保存至:\n%s", outputFile), myWindow)
				}
			})
		}()
	})
	startBtn.Importance = widget.HighImportance

	// 布局表单
	form := widget.NewForm(
		widget.NewFormItem("视频目录", container.NewBorder(nil, nil, nil, browseBtn, dirEntry)),
		widget.NewFormItem("选择视频", videoSelect),
		widget.NewFormItem("开始时间", startTimeEntry),
		widget.NewFormItem("结束时间", endTimeEntry),
		widget.NewFormItem("输出后缀", suffixEntry),
	)

	content := container.NewVBox(
		widget.NewLabelWithStyle("🎥 视频剪辑小助手", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		form,
		widget.NewSeparator(),
		startBtn,
		progressBar,
		statusLabel,
	)

	// 初始化读取默认目录
	refreshVideos(".")

	myWindow.SetContent(container.NewPadded(content))
	myWindow.ShowAndRun()
}

func getVideosInDir(dir string) []string {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	for _, e := range entries {
		if !e.IsDir() {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == ".mp4" || ext == ".mov" || ext == ".mkv" || ext == ".avi" {
				files = append(files, e.Name())
			}
		}
	}
	return files
}
