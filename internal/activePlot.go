package internal

import (
	"bufio"
	"fmt"
	"github.com/miketweaver/go-disk-usage/du"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const KB = uint64(1024)

const (
	PlotRunning = iota
	PlotError
	PlotFinished
)

type ActivePlot struct {
	PlotId      int64
	StartTime   time.Time
	EndTime     time.Time
	TargetDir   string
	PlotDir     string
	Fingerprint string

	Phase string
	Tail  []string
	State int
	lock  sync.RWMutex
	Id    string
}

func (ap *ActivePlot) String(showLog bool) string {
	ap.lock.RLock()
	state := "Unknown"
	switch ap.State {
	case PlotRunning:
		state = "Running"
	case PlotError:
		state = "Errored"
	case PlotFinished:
		state = "Finished"
	}
	s := fmt.Sprintf("Plot [%s] - %s, Phase: %s, Start Time: %s, Duration: %s, Tmp Dir: %s, Dst Dir: %s\n", ap.Id, state, ap.Phase, ap.StartTime.Format("2006-01-02 15:04:05"), time.Now().Sub(ap.StartTime).String(), ap.PlotDir, ap.TargetDir)
	if showLog {
		for _, l := range ap.Tail {
			s += fmt.Sprintf("\t%s", l)
		}
	}
	ap.lock.RUnlock()
	return s
}

func (ap *ActivePlot) CheckSpace() bool {
	plot := du.NewDiskUsage(ap.PlotDir)
	target := du.NewDiskUsage(ap.TargetDir)
	if plot.Available() < 360*KB*KB*KB {
		log.Printf("Not enough Plot directory space [%s]: %dGB", ap.PlotDir, plot.Available()/(KB*KB*KB))
		return false
	}
	if target.Available() < 360*KB*KB*KB {
		log.Printf("Not enough Target directory space [%s]: %dGB", ap.TargetDir, target.Available()/(KB*KB*KB))
		return false
	}
	return true
}

func (ap *ActivePlot) RunPlot() {
	ap.StartTime = time.Now()
	defer func() {
		ap.EndTime = time.Now()
	}()
	args := []string{
		"plots", "create", "-k32", "-n1", "-b6000", "-u128",
		"-t" + ap.PlotDir,
		"-d" + ap.TargetDir,
		"-a" + ap.Fingerprint,
	}
	cmd := exec.Command("chia", args...)
	ap.State = PlotRunning
	if stderr, err := cmd.StderrPipe(); err != nil {
		ap.State = PlotError
		log.Printf("Failed to start Plotting: %s", err)
		return
	} else {
		go ap.processLogs(stderr)
	}
	if stdout, err := cmd.StdoutPipe(); err != nil {
		ap.State = PlotError
		log.Printf("Failed to start Plotting: %s", err)
		return
	} else {
		go ap.processLogs(stdout)
	}
	if err := cmd.Run(); err != nil {
		ap.State = PlotError
		log.Printf("Plotting Exit with Error: %s", err)
		return
	}
	ap.State = PlotFinished
	return
}

func (ap *ActivePlot) processLogs(in io.ReadCloser) {
	reader := bufio.NewReader(in)
	for {
		if s, err := reader.ReadString('\n'); err != nil {
			break
		} else {
			if strings.HasPrefix(s, "Starting phase ") {
				ap.Phase = s[15:18]
			}
			if strings.HasPrefix(s, "ID: ") {
				ap.Id = strings.TrimSuffix(s[4:], "\n")
			}
			ap.lock.Lock()
			ap.Tail = append(ap.Tail, s)
			if len(ap.Tail) > 10 {
				ap.Tail = ap.Tail[len(ap.Tail)-10:]
			}
			ap.lock.Unlock()
		}
	}
	return
}
