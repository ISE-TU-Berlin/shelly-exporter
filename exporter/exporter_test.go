package exporter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConfigFillDefaults(t *testing.T) {
	var c Config
	c.FillDefaults()

	if c.SamplingFreq != 30*time.Second {
		t.Fatalf("expected SamplingFreq 30s, got %v", c.SamplingFreq)
	}
	if c.DiscoveryFreq != 10*time.Minute {
		t.Fatalf("expected DiscoveryFreq 10m, got %v", c.DiscoveryFreq)
	}
	if c.Interface != "eth0" {
		t.Fatalf("expected Interface eth0, got %q", c.Interface)
	}
	if c.LogLevel != "error" {
		t.Fatalf("expected LogLevel error, got %q", c.LogLevel)
	}
}

func TestDeviceSetAppendAndGet(t *testing.T) {
	ds := DeviceSet{}
	ds.append(Device{Instance: "dev-1", IP: "192.0.2.1"})
	ds.append(Device{Instance: "dev-1", IP: "192.0.2.1"}) // duplicate

	if ds.len() != 1 {
		t.Fatalf("expected len 1 after adding duplicate, got %d", ds.len())
	}
	all := ds.getAll()
	if len(all) != 1 || all[0].Instance != "dev-1" {
		t.Fatalf("unexpected devices: %+v", all)
	}
}

func TestAddDeviceAndDeviceCount(t *testing.T) {
	cfg := Config{MetricsEndpoint: ":0"}
	se := NewShellyExporter(nil, cfg)

	// adding device without IP should be ignored
	se.AddDevice(Device{Instance: "noip"})
	if se.DeviceCount() != 0 {
		t.Fatalf("expected 0 devices after adding device without IP, got %d", se.DeviceCount())
	}

	se.AddDevice(Device{Instance: "withip", IP: "192.0.2.5", Port: 80})
	if se.DeviceCount() != 1 {
		t.Fatalf("expected 1 device after adding with IP, got %d", se.DeviceCount())
	}
}

func TestPowerStateResponseJSONDecode(t *testing.T) {
	js := `{"instance":"inst-1","timestamp":"2025-10-12T12:00:00Z","apower":12.34,"voltage":230.0,"freq":50.0,"current":0.05}`
	var ps PowerStateResponse
	if err := json.Unmarshal([]byte(js), &ps); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if ps.Instance != "inst-1" {
		t.Fatalf("expected instance inst-1, got %q", ps.Instance)
	}
	if ps.APower <= 0 {
		t.Fatalf("expected APower > 0, got %v", ps.APower)
	}
}

func TestListDevices(t *testing.T) {
	cfg := Config{MetricsEndpoint: ":0"}
	se := NewShellyExporter(nil, cfg)

	se.AddDevice(Device{Instance: "dev-1", IP: "192.0.2.1", Port: 80, Name: "test-device"})

	req, err := http.NewRequest("GET", "/devices", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(se.DeviceList)

	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("unexpected status code: got %v, want %v", status, http.StatusOK)
	}
	var devices []Device
	if err := json.Unmarshal(rr.Body.Bytes(), &devices); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	if devices[0].Instance != "dev-1" {
		t.Fatalf("expected instance dev-1, got %q", devices[0].Instance)
	}
	if devices[0].IP != "192.0.2.1" {
		t.Fatalf("expected IP 192.0.2.1, got %q", devices[0].IP)
	}
}

func TestAddDeviceHandler(t *testing.T) {
	cfg := Config{MetricsEndpoint: ":0"}
	se := NewShellyExporter(nil, cfg)

	deviceJSON := `{"instance":"dev-2","ip":"192.168.2.149","port":80,"name":"new-device"}`
	req, err := http.NewRequest("POST", "/devices", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Body = io.NopCloser(strings.NewReader(deviceJSON))

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(se.AddDeviceHandler)

	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusCreated {
		t.Fatalf("unexpected status code: got %v, want %v", status, http.StatusCreated)
	}
	if se.DeviceCount() != 1 {
		t.Fatalf("expected 1 device after addition, got %d", se.DeviceCount())
	}
}

func TestUpdateDeviceHandler(t *testing.T) {
	cfg := Config{MetricsEndpoint: ":0"}
	se := NewShellyExporter(nil, cfg)

	se.AddDevice(Device{Instance: "dev-3", IP: "192.0.2.3", Port: 80})
	updateJSON := `{"instance":"dev-3","ip":"192.0.2.3","port":80, "name":"updated-device"}`
	req, err := http.NewRequest("PUT", "/devices/dev-3", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Body = io.NopCloser(strings.NewReader(updateJSON))

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(se.UpdateDevice)
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("unexpected status code: got %v, want %v", status, http.StatusOK)
	}

	// verify update
	devices := se.devices.getAll()
	if len(devices) != 1 {
		t.Fatalf("expected 1 device after update, got %d", len(devices))
	}
	if devices[0].Name != "updated-device" {
		t.Fatalf("expected updated name 'updated-device', got %q", devices[0].Name)
	}
}
