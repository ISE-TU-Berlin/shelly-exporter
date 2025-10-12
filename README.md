# Shelly Exporter

A small Prometheus exporter for Shelly smart-plugs (and similar Shelly devices) that discovers devices via mDNS (zeroconf), polls their power state via the device RPC API, and exposes metrics on an HTTP endpoint.

## What this does

- Discovers Shelly devices on the local network using mDNS/zeroconf on a single network interface.
- Periodically polls each discovered (and optionally statically-configured) device for power state via the Shelly RPC endpoint.
- Exposes Prometheus-style metrics on an HTTP endpoint (default path `/metrics`) and a `/health` endpoint for health checks.

## Build

You need Go installed (1.18+ recommended). From the repository root:

```bash
go build -o shelly-exporter ./
```

Or run directly during development:

```bash
go run ./
```

## Run

By default the program reads `config.yaml` from the current working directory. You can override the path with the environment variable `SHELLY_EXPORTER_CONFIG`.

Example:

```bash
SHELLY_EXPORTER_CONFIG=/etc/shelly-exporter/config.yaml ./shelly-exporter
```

When running, the exporter will:

- Discover devices using the network interface configured in `config.yaml`.
- Add any devices declared under the `devices:` section in the configuration.
- Start an HTTP server at the `metrics_endpoint` configured (for example `:9181`) and serve `/metrics` and `/health`.

Press Ctrl+C to stop the exporter gracefully.

## Metrics exposed

The exporter writes simple numeric metrics (Prometheus text exposition format). For each observed device instance it emits:

- `shelly_apower_watts{instance="<instance>"}` — active power in watts
- `shelly_voltage_volts{instance="<instance>"}` — voltage in volts
- `shelly_current_amps{instance="<instance>"}` — current in amperes
- `shelly_frequency_hz{instance="<instance>"}` — frequency in hertz

The `instance` label corresponds to the device's mDNS instance name (or the value you set in the config). Prometheus can scrape the configured `metrics_endpoint` at the `metrics` path.

## Configuration (`config.yaml`)

The exporter is configured via a YAML file. Below are the supported top-level fields and their meaning. Do not include actual values from your existing `config.yaml` here — use your own values when creating a file.

Top-level fields

- `metrics_endpoint` (string, required)
  - The network address the internal HTTP server binds to. Typical examples: `:9181` (all interfaces, port 9181) or `127.0.0.1:9181`.

- `sampling_freq` (duration, optional)
  - How often each device is polled for its power state. Uses Go duration syntax (for example `30s`, `1m`, `500ms`). Default: `30s`.

- `discovery_freq` (duration, optional)
  - How often the application re-runs mDNS discovery to find new devices. Uses Go duration syntax (for example `10m`). Default: `10m`.

- `interface` (string, optional)
  - Network interface name used for mDNS browsing (e.g., `eth0`, `en0`). The exporter selects this single interface for zeroconf discovery. Default: `eth0` if not set.

- `devices` (array of device objects, optional)
  - Static device entries to add at startup. Each device object supports the following fields:
    - `name` (string): the service name (optional, informational).
    - `instance` (string): the device instance name (used as the Prometheus label). This should match or uniquely identify the device.
    - `ip` (string): IPv4 address of the device. If empty the device will not be added (discovery via mDNS is the recommended way to find devices).
    - `port` (int): TCP port the Shelly device is listening on (e.g., `80`).

- `log_level` (string, optional)
  - Logging level. Uses the standard logrus levels: `panic`, `fatal`, `error`, `warn`, `info`, `debug`, `trace`. Default: `error`.

Notes and constraints

- Device `ip` is required for statically-configured devices. If `ip` is empty the exporter will skip adding that entry.
- The exporter also discovers devices automatically using mDNS and will append discovered devices unless a device with the same `instance` already exists.
- Durations must be valid Go duration strings. Examples: `30s`, `1m30s`, `5m`, `500ms`.

Example configuration (skeleton)

```yaml
# Path: config.yaml

# The address to bind the HTTP metrics server to
metrics_endpoint: ":9181"

# How often to poll each device for power state
sampling_freq: "30s"

# How often to re-run mDNS discovery
discovery_freq: "10m"

# Network interface used for mDNS discovery
interface: "eth0"

# Optional static devices to add at startup
devices:
  - name: "shellyplug"
    instance: "shellyplug-01"
    ip: "192.0.2.10"
    port: 80

# Logging level
log_level: "info"
```

## Troubleshooting

- If the exporter fails to start with an error about the network interface, confirm the `interface` name exists on the host (use `ip link` on Linux or `ifconfig`/`networksetup` on macOS).
- If discovery finds no devices, ensure mDNS (multicast) traffic can flow on that interface and that the Shelly devices are powered and connected.
- If metrics show zero values, check device reachability (ping the `ip`), and ensure the Shelly device supports the RPC endpoint used by this exporter.

## Prometheus scrape configuration example

Add a scrape job in Prometheus that scrapes the exporter instance:

```yaml
scrape_configs:
  - job_name: 'shelly-exporter'
    static_configs:
      - targets: ['<host-running-exporter>:9181']
```

Replace `<host-running-exporter>` with the address where you run the exporter.

## License

Provided as-is. Check repository license for details.
