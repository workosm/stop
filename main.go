package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

type ProcessInfo struct {
	PID  int32
	Name string
	CPU  float64
	Mem  float32
}

var (
	filterText string
	filterMu   sync.Mutex
)

// Progress bar
func drawBar(percent float64, width int) string {
	if percent < 0 { percent = 0 }
	if percent > 100 { percent = 100 }
	filled := int((percent / 100) * float64(width))
	color := "green"
	if percent > 85 { color = "red" } else if percent > 50 { color = "yellow" }
	
	bar := "[" + "[" + color + "]"
	for i := 0; i < width; i++ {
		if i < filled { bar += "|" } else { bar += " " }
	}
	return bar + "[-]]"
}

func main() {
	app := tview.NewApplication()
	app.EnableMouse(true)

	// Widgets
	sysInfo := tview.NewTextView().SetDynamicColors(true)
	sysInfo.SetBorder(true).SetTitle(" 🖥️ System & Network ")

	cpuView := tview.NewTextView().SetDynamicColors(true)
	cpuView.SetBorder(true).SetTitle(" 🧠 CPU & Thermal ")

	procTable := tview.NewTable().SetBorders(false).SetSelectable(true, false)
	procTable.SetBorder(true).SetTitle(" 📋 Processes (k:Kill, s:Stop, c:Cont, /:Find) ")
	procTable.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorBlue).Foreground(tcell.ColorWhite).Attributes(tcell.AttrBold))

	searchField := tview.NewInputField().SetLabel(" 🔍 Search: ").SetFieldWidth(30)
	searchField.SetChangedFunc(func(text string) {
		filterMu.Lock()
		filterText = strings.ToLower(text)
		filterMu.Unlock()
	})

	// UI
	header := tview.NewFlex().
		AddItem(sysInfo, 0, 1, false).
		AddItem(cpuView, 0, 1, false)

	mainFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 10, 1, false).
		AddItem(searchField, 3, 1, false).
		AddItem(procTable, 0, 2, true)

	// Control
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := procTable.GetSelection()
		var pid int
		if row > 0 {
			pid, _ = strconv.Atoi(procTable.GetCell(row, 0).Text)
		}

		switch event.Rune() {
		case '/': app.SetFocus(searchField); return nil
		case 'k': // KILL
			if pid != 0 {
				if p, err := os.FindProcess(pid); err == nil { p.Kill() }
			}
		case 's': // STOP
			if pid != 0 && runtime.GOOS != "windows" {
				exec.Command("kill", "-STOP", strconv.Itoa(pid)).Run()
			}
		case 'c': // CONTINUE
			if pid != 0 && runtime.GOOS != "windows" {
				exec.Command("kill", "-CONT", strconv.Itoa(pid)).Run()
			}
		}
		if event.Key() == tcell.KeyEsc || event.Key() == tcell.KeyEnter {
			app.SetFocus(procTable)
		}
		return event
	})

	oldNet, _ := net.IOCounters(false)
	go func() {
		hInfo, _ := host.Info()
		pkgs := ""
		if runtime.GOOS == "linux" {
			for _, m := range []string{"dpkg", "pacman", "rpm"} {
				if _, err := exec.LookPath(m); err == nil {
					out, _ := exec.Command(m, map[string]string{"dpkg": "-l", "pacman": "-Q", "rpm": "-qa"}[m]).Output()
					pkgs = fmt.Sprintf(" | [blue]Pkgs:[-] %d", strings.Count(string(out), "\n"))
					break
				}
			}
		}

		for {
			v, _ := mem.VirtualMemory()
			l, _ := load.Avg()
			
			// Speed
			newNet, _ := net.IOCounters(false)
			rx := float64(newNet[0].BytesRecv-oldNet[0].BytesRecv) / 1024 / 1024
			tx := float64(newNet[0].BytesSent-oldNet[0].BytesSent) / 1024 / 1024
			oldNet = newNet

			sysStr := fmt.Sprintf(
				" [yellow]Host:[-] %s\n [yellow]OS:[-]   %s %s%s\n [yellow]Load:[-] %.2f %.2f\n [yellow]Net:[-]  ⬇ %.2f MB/s | ⬆ %.2f MB/s\n [yellow]RAM:[-]  %s %.1f%%",
				hInfo.Hostname, hInfo.Platform, hInfo.PlatformVersion, pkgs, l.Load1, l.Load5, rx, tx, drawBar(v.UsedPercent, 10), v.UsedPercent,
			)

			// CPU & Temp
			cpuPercents, _ := cpu.Percent(0, true)
			tempStr := ""
			if runtime.GOOS == "linux" {
				temps, _ := host.SensorsTemperatures()
				for _, t := range temps {
					if strings.Contains(strings.ToLower(t.SensorKey), "package") || strings.Contains(t.SensorKey, "core") {
						tempStr = fmt.Sprintf(" [red]%.1f°C[-]", t.Temperature)
						break
					}
				}
			}

			cpuStr := ""
			for i, p := range cpuPercents {
				cpuStr += fmt.Sprintf(" C%2d %s %5.1f%%", i, drawBar(p, 10), p)
				if i == 0 { cpuStr += tempStr }
				cpuStr += "\n"
			}

			// Proc
			procs, _ := process.Processes()
			var procList []ProcessInfo
			filterMu.Lock()
			currFilter := filterText
			filterMu.Unlock()

			for _, p := range procs {
				name, _ := p.Name()
				if currFilter != "" && !strings.Contains(strings.ToLower(name), currFilter) { continue }
				c, _ := p.CPUPercent()
				m, _ := p.MemoryPercent()
				procList = append(procList, ProcessInfo{p.Pid, name, c, m})
			}
			sort.Slice(procList, func(i, j int) bool { return procList[i].CPU > procList[j].CPU })

			// UI Update
			app.QueueUpdateDraw(func() {
				sysInfo.SetText(sysStr)
				cpuView.SetText(cpuStr)
				
				selRow, _ := procTable.GetSelection()
				procTable.Clear()
				headers := []string{"PID", "CPU%", "MEM%", "NAME"}
				for i, h := range headers {
					procTable.SetCell(0, i, tview.NewTableCell(h).SetTextColor(tcell.ColorYellow).SetAttributes(tcell.AttrBold))
				}
				for i, p := range procList {
					if i >= 45 { break }
					color := tcell.ColorWhite
					if p.CPU > 70 { color = tcell.ColorRed }
					procTable.SetCell(i+1, 0, tview.NewTableCell(fmt.Sprintf("%d", p.PID)).SetTextColor(tcell.ColorGray))
					procTable.SetCell(i+1, 1, tview.NewTableCell(fmt.Sprintf("%.1f", p.CPU)).SetTextColor(color))
					procTable.SetCell(i+1, 2, tview.NewTableCell(fmt.Sprintf("%.1f", p.Mem)))
					procTable.SetCell(i+1, 3, tview.NewTableCell(p.Name))
				}
				if selRow < procTable.GetRowCount() { procTable.Select(selRow, 0) }
			})
			time.Sleep(1 * time.Second)
		}
	}()

	if err := app.SetRoot(mainFlex, true).Run(); err != nil { panic(err) }
}
