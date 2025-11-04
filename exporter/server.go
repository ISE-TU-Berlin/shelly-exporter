package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
	log "github.com/sirupsen/logrus"
)

// replace the placeholder fields with concrete types at package init
func init() {
	// no-op; fields are set in NewShellyExporter
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

	// use the resolver created in NewShellyExporter; it is stored in the resolver field
	err := se.resolver.Browse(ctx, "_http._tcp", "local.", entries)
	if err != nil {
		log.Errorf("Error during browsing: %v", err)
	}

	<-ctx.Done()
	if err := ctx.Err(); err != nil && err != context.DeadlineExceeded {
		log.Errorf("Error during browsing: %v", err)
	}
	after := se.devices.len()
	log.Debugf("Discovered %d new devices, total %d devices", after-before, after)
}

func (se *ShellyExporter) tick() {
	lastTick := time.Now()
	// ensure observation mutex is a real mutex
	for range se.ticker.C {
		for _, device := range se.devices.getAll() {
			powerState, err := se.getPowerState(device)
			powerState.Instance = device.Instance
			powerState.Name = device.Name
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
		fmt.Fprintf(w, "shelly_apower_watts{instance=\"%s\",node=\"%s\"} %f\n", instance, obs.Name, obs.APower)
		fmt.Fprintf(w, "shelly_voltage_volts{instance=\"%s\",node=\"%s\"} %f\n", instance, obs.Name, obs.Voltage)
		fmt.Fprintf(w, "shelly_current_amps{instance=\"%s\",node=\"%s\"} %f\n", instance, obs.Name, obs.Current)
		fmt.Fprintf(w, "shelly_frequency_hz{instance=\"%s\",node=\"%s\"} %f\n", instance, obs.Name, obs.Freq)
	}
}

func (se *ShellyExporter) validateDevice(device Device) error {
	if device.IP == "" {
		return fmt.Errorf("device %s has no IP", device.Instance)
	}
	return nil
}

func (se *ShellyExporter) DeviceList(w http.ResponseWriter, r *http.Request) {
	se.observationMutex.RLock()
	defer se.observationMutex.RUnlock()

	jsonMsg, err := json.MarshalIndent(se.devices.getAll(), "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error marshalling devices: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonMsg)
}

func (se *ShellyExporter) AddDeviceHandler(w http.ResponseWriter, r *http.Request) {
	var device Device
	err := json.NewDecoder(r.Body).Decode(&device)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error decoding device: %v", err), http.StatusBadRequest)
		return
	}
	if err := se.validateDevice(device); err != nil {
		http.Error(w, fmt.Sprintf("Invalid device: %v", err), http.StatusBadRequest)
		return
	}
	se.AddDevice(device)
	w.WriteHeader(http.StatusCreated)
}

func (se *ShellyExporter) UpdateDevice(w http.ResponseWriter, r *http.Request) {
	var device Device
	err := json.NewDecoder(r.Body).Decode(&device)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error decoding device: %v", err), http.StatusBadRequest)
		return
	}

	if err := se.validateDevice(device); err != nil {
		http.Error(w, fmt.Sprintf("Invalid device: %v", err), http.StatusBadRequest)
		return
	}

	se.observationMutex.Lock()
	defer se.observationMutex.Unlock()

	//find device
	for i, d := range se.devices.Devices {
		if d.Instance == device.Instance {
			//update
			se.devices.Devices[i] = device
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	http.Error(w, "Device not found", http.StatusNotFound)

}

func (se *ShellyExporter) Devices(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		se.DeviceList(w, r)
		return
	}

	if r.Method == http.MethodPost {
		se.AddDeviceHandler(w, r)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

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
	mux.HandleFunc("/devices", se.Devices)
	mux.HandleFunc("/devices/{instance}", se.UpdateDevice)
	se.srv = &http.Server{
		Addr:              se.MetricsEndpoint,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	if err := se.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Could not listen on %s: %v\n", se.MetricsEndpoint, err)
	}

}

func (se *ShellyExporter) Start() {
	se.ticker = time.NewTicker(se.SamplingFreq)
	go se.tick()

	se.Serve()
}

func (se *ShellyExporter) Stop() {
	if se.ticker != nil {
		se.ticker.Stop()
	}
	if se.srv != nil {
		se.srv.Shutdown(context.Background())
	}
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
