package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ISE-TU-Berlin/shelly-exporter/exporter"
	log "github.com/sirupsen/logrus"
)

func main() {
	se := exporter.LoadShellyExporter()

	// initial discovery
	se.DiscoverDevices()

	if se.DeviceCount() == 0 {
		log.Warn("No devices found, exiting")
		os.Exit(1)
	}

	// register Ctrl+C handler to exit gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	fmt.Println("Exit using ^C")
	go func() {
		<-c
		log.Println("Exiting...")
		se.Stop()
		os.Exit(0)
	}()

	se.Start()
}
