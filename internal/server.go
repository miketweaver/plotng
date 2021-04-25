package internal

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"github.com/miketweaver/go-disk-usage/du"
	"log"
	"net/http"
	"sync"
	"time"
)

type Server struct {
	config               *PlotConfig
	active               map[int64]*ActivePlot
	archive              []*ActivePlot
	currentTemp          int
	currentTarget        int
	targetDelayStartTime time.Time
	lock                 sync.RWMutex
}

func (server *Server) ProcessLoop(configPath string, port int) {
	gob.Register(Msg{})
	gob.Register(ActivePlot{})
	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", port), server); err != nil {
			log.Fatalf("Failed to start webserver: %s", err)
		}
	}()

	server.config = &PlotConfig{
		ConfigPath: configPath,
	}
	server.active = map[int64]*ActivePlot{}
	server.createPlot(time.Now())
	ticker := time.NewTicker(time.Minute)
	for t := range ticker.C {
		server.createPlot(t)
	}
}

func (server *Server) createPlot(t time.Time) {
	server.config.ProcessConfig()
	if server.config.CurrentConfig != nil {
		server.config.Lock.RLock()
		if len(server.active) < server.config.CurrentConfig.NumberOfParallelPlots {
			server.createNewPlot(server.config.CurrentConfig)
		}
		server.config.Lock.RUnlock()
	}
	fmt.Printf("%s, %d Active Plots\n", t.Format("2006-01-02 15:04:05"), len(server.active))
	for _, plot := range server.active {
		fmt.Print(plot.String(server.config.CurrentConfig.ShowPlotLog))
		if plot.State == PlotFinished || plot.State == PlotError {
			server.archive = append(server.archive, plot)
			delete(server.active, plot.PlotId)
		}
	}
	fmt.Println(" ")
}

func (server *Server) createNewPlot(config *Config) {
	defer server.lock.Unlock()
	server.lock.Lock()
	if len(config.TempDirectory) == 0 || len(config.TargetDirectory) == 0 {
		return
	}
	if time.Now().Before(server.targetDelayStartTime) {
		return
	}

	if server.currentTarget >= len(config.TargetDirectory) {
		server.currentTarget = 0
		server.targetDelayStartTime = time.Now().Add(time.Duration(config.StaggeringDelay) * time.Minute)
		return
	}
	plotDir := config.TempDirectory[server.currentTemp]
	server.currentTemp++
	if server.currentTemp >= len(config.TempDirectory) {
		server.currentTemp = 0
	}
	targetDir := config.TargetDirectory[server.currentTarget]
	server.currentTarget++
	t := time.Now()
	plot := &ActivePlot{
		PlotId:      t.Unix(),
		TargetDir:   targetDir,
		PlotDir:     plotDir,
		Fingerprint: config.Fingerprint,
		Phase:       "NA",
		Tail:        nil,
		State:       PlotRunning,
	}
	server.active[plot.PlotId] = plot
	go plot.RunPlot()
}

func (server *Server) getDiskSpaceAvailable(path string) uint64 {
	d := du.NewDiskUsage(path)
	return d.Available()
}

func (server *Server) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	log.Printf("New query: %s", req.URL.String())
	defer server.lock.RUnlock()
	server.lock.RLock()

	var msg Msg
	msg.TargetDirs = map[string]uint64{}
	msg.TempDirs = map[string]uint64{}
	for _, v := range server.active {
		msg.Actives = append(msg.Actives, v)
	}
	for _, v := range server.archive {
		msg.Archived = append(msg.Archived, v)
	}
	if server.config.CurrentConfig != nil {
		for _, dir := range server.config.CurrentConfig.TargetDirectory {
			msg.TargetDirs[dir] = server.getDiskSpaceAvailable(dir)
		}
		for _, dir := range server.config.CurrentConfig.TempDirectory {
			msg.TempDirs[dir] = server.getDiskSpaceAvailable(dir)
		}
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(msg); err == nil {
		resp.WriteHeader(http.StatusOK)
		resp.Write(buf.Bytes())
	} else {
		resp.WriteHeader(http.StatusInternalServerError)
		log.Printf("Failed to encode message: %s", err)
	}
}

type Msg struct {
	Actives    []*ActivePlot
	Archived   []*ActivePlot
	TempDirs   map[string]uint64
	TargetDirs map[string]uint64
}
