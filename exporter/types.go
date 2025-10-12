package exporter

import (
	"net/http"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

type Device struct {
	Name     string `yaml:"name"`
	Instance string `yaml:"instance"`
	IP       string `yaml:"ip"`
	Port     int    `yaml:"port"`
}

type DeviceSet struct {
	Devices []Device `yaml:"devices"`
}

func (ds *DeviceSet) append(device Device) {
	for _, d := range ds.Devices {
		if d.Instance == device.Instance {
			// already exists
			return
		}
	}
	ds.Devices = append(ds.Devices, device)
}

func (ds *DeviceSet) len() int {
	return len(ds.Devices)
}

func (ds *DeviceSet) getAll() []Device {
	return ds.Devices
}

type PowerStateResponse struct {
	Instance  string    `json:"instance"`
	Timestamp time.Time `json:"timestamp"`
	APower    float64   `json:"apower"`
	Voltage   float64   `json:"voltage"`
	Freq      float64   `json:"freq"`
	Current   float64   `json:"current"`
}

type ShellyExporter struct {
	SamplingFreq  time.Duration
	DiscoveryFreq time.Duration

	MetricsEndpoint string

	// internal fields (set by constructor)
	resolver *zeroconf.Resolver
	devices  DeviceSet

	ticker           *time.Ticker
	observations     map[string]*PowerStateResponse
	observationMutex sync.RWMutex

	srv *http.Server
}

// DeviceCount returns the number of known devices
func (se *ShellyExporter) DeviceCount() int {
	return se.devices.len()
}
