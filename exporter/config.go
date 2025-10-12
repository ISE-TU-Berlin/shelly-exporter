package exporter

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/grandcat/zeroconf"
	"go.yaml.in/yaml/v2"

	log "github.com/sirupsen/logrus"
)

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
