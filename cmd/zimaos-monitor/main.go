package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"zimaos-monitor/internal/collector"
	"zimaos-monitor/internal/config"
	mqttclient "zimaos-monitor/internal/mqtt"
)

var version = "dev"

type zimaosInfo struct {
	InstalledVersion string `json:"installed_version"`
	LatestVersion    string `json:"latest_version,omitempty"`
	ReleaseURL       string `json:"release_url,omitempty"`
}

type metrics struct {
	CPUTemp        float64                        `json:"cpu_temp"`
	CPUWatts       float64                        `json:"cpu_watts"`
	CPUUsagePct    float64                        `json:"cpu_usage_pct"`
	CPUCorePct     []float64                      `json:"cpu_core_pct"`
	LoadAvg1       float64                        `json:"load_avg_1"`
	LoadAvg5       float64                        `json:"load_avg_5"`
	LoadAvg15      float64                        `json:"load_avg_15"`
	UptimeSeconds  uint64                         `json:"uptime_seconds"`
	RAMUsedPct     float64                        `json:"ram_used_pct"`
	RAMAvailableGB float64                        `json:"ram_available_gb"`
	RAMTotalGB     float64                        `json:"ram_total_gb"`
	Disks          map[string]collector.DiskStats `json:"disks"`
	ZimaOS         zimaosInfo                     `json:"zimaos"`
}

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	dryRun := flag.Bool("dry-run", false, "print metrics to stdout without publishing to MQTT")
	flag.Parse()

	log.Printf("zimaos-monitor %s starting", version)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	zimaosVersion := collector.ZimaOSVersion()
	log.Printf("zimaos version: %q", zimaosVersion)

	cfg.Device.SWVersion = "zimaos-monitor " + version
	log.Printf("device id=%q name=%q serial=%q sw=%q", cfg.Device.ID, cfg.Device.Name, cfg.Device.SerialNumber, cfg.Device.SWVersion)

	if len(cfg.Disks) == 0 {
		cfg.Disks = collector.DiscoverDisks()
		log.Printf("auto-discovered %d disk(s)", len(cfg.Disks))
	}

	numCores := collector.NumLogicalCores()
	log.Printf("logical cpu cores: %d", numCores)

	cpu, err := collector.NewCPUCollector()
	if err != nil {
		log.Printf("warn: cpu collector init: %v", err)
	}

	var client *mqttclient.Client
	if !*dryRun {
		client, err = mqttclient.NewClient(cfg)
		if err != nil {
			log.Fatalf("mqtt: %v", err)
		}
		defer client.Disconnect()

		if err := client.PublishDiscovery(cfg.Disks, numCores, true); err != nil {
			log.Printf("warn: publish discovery: %v", err)
		}
		log.Printf("connected to %s, publishing every %s", cfg.MQTT.Broker, cfg.Interval)
	} else {
		log.Println("dry-run mode: printing to stdout")
	}

	var upstream *collector.UpstreamChecker
	if cfg.Updates.IsEnabled() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		upstream = collector.NewUpstreamChecker(cfg.Updates.CheckInterval, "zimaos-monitor/"+version)
		upstream.Start(ctx)
	}

	discoveryCounter := 0
	stateTopic := fmt.Sprintf("%s/state", cfg.Device.ID)
	updateTopic := fmt.Sprintf("%s/update", cfg.Device.ID)

	collect := func() {
		cpuTemp, cpuWatts, cpuUsagePct, corePcts := cpu.Collect()

		memStats, err := collector.CollectMemory()
		if err != nil {
			log.Printf("warn: memory: %v", err)
		}

		avgStat, err := load.Avg()
		if err != nil {
			log.Printf("warn: load avg: %v", err)
			avgStat = &load.AvgStat{}
		}

		uptimeSec, err := host.Uptime()
		if err != nil {
			log.Printf("warn: uptime: %v", err)
		}

		zi := zimaosInfo{InstalledVersion: zimaosVersion}
		if upstream != nil {
			zi.LatestVersion, zi.ReleaseURL = upstream.Latest()
		}

		m := metrics{
			CPUTemp:        cpuTemp,
			CPUWatts:       cpuWatts,
			CPUUsagePct:    cpuUsagePct,
			CPUCorePct:     corePcts,
			LoadAvg1:       avgStat.Load1,
			LoadAvg5:       avgStat.Load5,
			LoadAvg15:      avgStat.Load15,
			UptimeSeconds:  uptimeSec,
			RAMUsedPct:     memStats.UsedPct,
			RAMAvailableGB: memStats.AvailableGB,
			RAMTotalGB:     memStats.TotalGB,
			Disks:          collector.CollectDisks(cfg.Disks),
			ZimaOS:         zi,
		}

		payload, err := json.Marshal(m)
		if err != nil {
			log.Printf("error: marshal metrics: %v", err)
			return
		}

		if *dryRun {
			fmt.Println(string(payload))
			return
		}

		if err := client.Publish(stateTopic, payload, false); err != nil {
			log.Printf("error: publish state: %v", err)
		}

		updatePayload, err := json.Marshal(zi)
		if err != nil {
			log.Printf("error: marshal update payload: %v", err)
		} else if err := client.Publish(updateTopic, updatePayload, false); err != nil {
			log.Printf("error: publish update: %v", err)
		}

		// Re-publish discovery every 10 intervals so HA picks it up after restarts
		discoveryCounter++
		if discoveryCounter >= 10 {
			discoveryCounter = 0
			if err := client.PublishDiscovery(cfg.Disks, numCores, false); err != nil {
				log.Printf("warn: re-publish discovery: %v", err)
			}
		}
	}

	// First publish immediately, then on ticker
	collect()

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			collect()
		case sig := <-sigCh:
			log.Printf("received %s, shutting down", sig)
			return
		}
	}
}
