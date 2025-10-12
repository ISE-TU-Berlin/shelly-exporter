package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
	"go.yaml.in/yaml/v2"

	log "github.com/sirupsen/logrus"
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
	resolver *zeroconf.Resolver
	devices  DeviceSet

	SamplingFreq  time.Duration
	DiscoveryFreq time.Duration
	ticker        *time.Ticker

	observations     map[string]*PowerStateResponse
	observationMutex sync.RWMutex

	MetricsEndpoint string

	srv *http.Server
}

func (se *ShellyExporter) AddDevice(device Device) {
	if device.IP == "" {
		log.Warnf("Device %s has no IP, not adding", device.Instance)
		return
	}

	se.devices.append(device)
}

func (se *ShellyExporter) DiscoverDevices() {
	before := se.devices.len()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()

	entries := make(chan *zeroconf.ServiceEntry)

	go func() {
		for entry := range entries {
			if strings.Contains(entry.Instance, "shellyplugsg3") {
				if len(entry.AddrIPv4) == 0 {
					log.Warnf("No IPv4 address found for device %+v", entry)
					continue
				}
				device := Device{
					Name:     entry.Service,
					Instance: entry.Instance,
					IP:       entry.AddrIPv4[0].String(),
					Port:     entry.Port,
				}
				se.AddDevice(device)
				log.Debugf("Found device: %+v", device)
			}

		}
	}()

	se.resolver.Browse(ctx, "_http._tcp", "local.", entries)
	<-ctx.Done()
	if err := ctx.Err(); err != nil && err != context.DeadlineExceeded {
		log.Errorf("Error during browsing: %v", err)
	}
	after := se.devices.len()
	log.Debugf("Discovered %d new devices, total %d devices", after-before, after)
}

func (se *ShellyExporter) tick() {
	lastTick := time.Now()
	for range se.ticker.C {
		for _, device := range se.devices.getAll() {
			powerState, err := se.getPowerState(device)
			powerState.Instance = device.Instance
			// send to channel
			se.observationMutex.Lock()
			se.observations[device.Instance] = &powerState
			se.observationMutex.Unlock()
			if err != nil {
				log.Errorf("Error getting power state for device %s: %v", device.Instance, err)
				continue
			}
			log.Debugf("Device %s power state: %+v", device.Instance, powerState)
		}
		if time.Since(lastTick) > se.DiscoveryFreq {
			se.DiscoverDevices()
			lastTick = time.Now()
		}
	}
}

func (se *ShellyExporter) GetObservations(w http.ResponseWriter, r *http.Request) {
	se.observationMutex.RLock()
	defer se.observationMutex.RUnlock()

	for instance, obs := range se.observations {
		fmt.Fprintf(w, "shelly_apower_watts{instance=\"%s\"} %f\n", instance, obs.APower)
		fmt.Fprintf(w, "shelly_voltage_volts{instance=\"%s\"} %f\n", instance, obs.Voltage)
		fmt.Fprintf(w, "shelly_current_amps{instance=\"%s\"} %f\n", instance, obs.Current)
		fmt.Fprintf(w, "shelly_frequency_hz{instance=\"%s\"} %f\n", instance, obs.Freq)
	}
}

func (se *ShellyExporter) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (se *ShellyExporter) Serve() {
	log.Infof("Starting metrics server at %s", se.MetricsEndpoint)
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", se.GetObservations)
	mux.HandleFunc("/health", se.HealthCheck)

	srv := &http.Server{
		Addr:              se.MetricsEndpoint,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	se.srv = srv

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Could not listen on %s: %v\n", se.MetricsEndpoint, err)
	}

}

func (se *ShellyExporter) Start() {
	se.ticker = time.NewTicker(se.SamplingFreq)
	go se.tick()

	se.Serve()
}

func (se *ShellyExporter) Stop() {
	se.ticker.Stop()
	se.srv.Shutdown(context.Background())
}

func (se *ShellyExporter) getPowerState(device Device) (PowerStateResponse, error) {
	//call rcp api to get power state /rpc/Switch.GetStatus?id=0
	url := fmt.Sprintf("http://%s:%d/rpc/Switch.GetStatus?id=0", device.IP, device.Port)
	resp, err := http.Get(url)
	if err != nil {
		return PowerStateResponse{}, err
	}
	defer resp.Body.Close()

	var result PowerStateResponse
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return PowerStateResponse{}, err
	}
	result.Timestamp = time.Now().UTC()

	return result, nil
}

type Config struct {
	MetricsEndpoint string        `yaml:"metrics_endpoint"`
	SamplingFreq    time.Duration `yaml:"sampling_freq"`
	DiscoveryFreq   time.Duration `yaml:"discovery_freq"`
	Interface       string        `yaml:"interface"`

	Devices []Device `yaml:"devices"`

	LogLevel string `yaml:"log_level"`
}

func (c *Config) FillDefaults() {
	if c.DiscoveryFreq == 0 {
		c.DiscoveryFreq = time.Minute * 10
	}

	if c.SamplingFreq == 0 {
		c.SamplingFreq = time.Second * 30
	}

	if c.Interface == "" {
		c.Interface = "eth0"
	}

	if c.LogLevel == "" {
		c.LogLevel = "error"
	}
}

func LoadShellyExporter() *ShellyExporter {

	config := os.Getenv("SHELLY_EXPORTER_CONFIG")
	if config == "" {
		config = "config.yaml"
	}
	fmt.Printf("Using config file: %s\n", config)

	if _, err := os.Stat(config); os.IsNotExist(err) {
		fmt.Printf("Config file %s does not exist, using defaults\n", config)
		os.Exit(-1)
	}

	var cnf Config

	data, err := os.ReadFile(config)
	if err != nil {
		fmt.Printf("Error reading config file: %v\n", err)
		os.Exit(-1)
	}
	err = yaml.Unmarshal(data, &cnf)
	if err != nil {
		fmt.Printf("Error unmarshalling config file: %v\n", err)
		os.Exit(-1)
	}
	fmt.Printf("Config: %+v\n", cnf)

	cnf.FillDefaults()

	level, err := log.ParseLevel(cnf.LogLevel)
	if err != nil {
		fmt.Printf("Invalid log level %s, using debug\n", cnf.LogLevel)
		level = log.DebugLevel
	}
	log.SetLevel(level)

	iface, err := net.InterfaceByName(cnf.Interface)
	if err != nil {
		log.Fatalf("interface %s not found: %v", cnf.Interface, err)
	}

	// create resolver that only uses the specified interface
	resolver, err := zeroconf.NewResolver(zeroconf.SelectIfaces([]net.Interface{*iface}))
	if err != nil {
		log.Fatalf("failed to create resolver: %v", err)
	}

	shellyExporter := NewShellyExporter(resolver, cnf)

	if shellyExporter.MetricsEndpoint == "" {
		log.Error("Metrics endpoint not set, exiting")
		os.Exit(1)
	}

	// add devices from config
	for _, device := range cnf.Devices {
		shellyExporter.AddDevice(device)
	}

	return &shellyExporter
}

func main() {
	shellyExporter := LoadShellyExporter()

	shellyExporter.DiscoverDevices()

	if shellyExporter.devices.len() == 0 {
		log.Warn("No devices found, exiting")
		os.Exit(1)
	}

	log.Debugf("Found %d devices", shellyExporter.devices.len())

	// register Ctrl+C handler to exit gracefully
	c := make(chan os.Signal, 1)
	fmt.Println("Exit using ^C")
	go func() {
		<-c
		log.Println("Exiting...")
		shellyExporter.Stop()
		os.Exit(0)
	}()

	shellyExporter.Start()

}

func NewShellyExporter(resolver *zeroconf.Resolver, cnf Config) ShellyExporter {
	return ShellyExporter{
		resolver:        resolver,
		devices:         DeviceSet{Devices: make([]Device, 0)},
		SamplingFreq:    cnf.SamplingFreq,
		DiscoveryFreq:   cnf.DiscoveryFreq,
		observations:    map[string]*PowerStateResponse{},
		MetricsEndpoint: cnf.MetricsEndpoint,
	}
}
