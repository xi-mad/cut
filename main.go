package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

// getFFmpegPath 优先查找当前目录下的 ffmpeg，若无则查找系统 PATH
func getFFmpegPath() (string, error) {
	localFFmpeg := "./ffmpeg"
	if runtime.GOOS == "windows" {
		localFFmpeg = "./ffmpeg.exe"
	}

	// 1. 检查当前目录下是否有 ffmpeg
	if _, err := os.Stat(localFFmpeg); err == nil {
		return localFFmpeg, nil
	}

	// 2. 尝试从系统环境变量中查找
	path, err := exec.LookPath("ffmpeg")
	if err == nil {
		return path, nil
	}

	return "", fmt.Errorf("未找到 ffmpeg，请确保它在当前目录或系统 PATH 中")
}

func main() {
	myApp := app.NewWithID("com.videocut.app")
	myWindow := myApp.NewWindow("🎥 极简视频剪辑器")
	myWindow.Resize(fyne.NewSize(800, 600))

	// UI 组件定义
	fileEntry := widget.NewEntry()
	fileEntry.SetPlaceHolder("请选择或输入视频文件路径...")

	// 浏览文件按钮
// 浏览文件按钮
	browseBtn := widget.NewButton("📁 浏览...", func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil || reader == nil {
				return
			}
			path := reader.URI().Path()
			reader.Close() // 关闭文件流放在外层

			// 使用 fyne.Do 切换回主 UI 线程来修改文本框
			fyne.Do(func() {
				fileEntry.SetText(path)
			})
		}, myWindow)

		// 限制只能选择视频文件
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".mp4", ".mov", ".mkv", ".avi"}))
		fd.Show()
	})

	startTimeEntry := widget.NewEntry()
	startTimeEntry.SetPlaceHolder("例: 00:00:05 或 5")
	startTimeEntry.SetText("00:00:00")

	endTimeEntry := widget.NewEntry()
	endTimeEntry.SetPlaceHolder("例: 00:01:20 或 80")
	endTimeEntry.SetText("00:00:15")

	suffixEntry := widget.NewEntry()
	suffixEntry.SetText("_cut")

	progressBar := widget.NewProgressBarInfinite()
	progressBar.Hide()

	statusLabel := widget.NewLabel("就绪")
	statusLabel.Alignment = fyne.TextAlignCenter

	var startBtn *widget.Button

	startBtn = widget.NewButton("✂️ 开始剪辑视频", func() {
		inputFile := strings.TrimSpace(fileEntry.Text)
		if inputFile == "" {
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

		// 检查 FFmpeg 环境
		ffmpegPath, err := getFFmpegPath()
		if err != nil {
			dialog.ShowError(err, myWindow)
			return
		}

		suffix := strings.TrimSpace(suffixEntry.Text)
		ext := filepath.Ext(inputFile)
		baseName := strings.TrimSuffix(inputFile, ext)
		outputFile := fmt.Sprintf("%s%s%s", baseName, suffix, ext)

		// 禁用按钮，显示进度条
		startBtn.Disable()
		progressBar.Show()
		statusLabel.SetText(fmt.Sprintf("⏱️ 正在使用 %s 剪辑中...", filepath.Base(ffmpegPath)))

		go func() {
			args := []string{
				"-y",
				"-ss", startTime,
				"-to", endTime,
				"-i", inputFile,
				"-c:v", "copy", // 👈 视频流极速复制（保证无损且秒出）
				"-c:a", "aac",  // 👈 音频流转为 AAC（保证任何 MP4 格式都不报错）
				outputFile,
			}

			cmd := exec.Command(ffmpegPath, args...)
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
		widget.NewFormItem("视频文件", container.NewBorder(nil, nil, nil, browseBtn, fileEntry)),
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

	myWindow.SetContent(container.NewPadded(content))
	myWindow.ShowAndRun()
}