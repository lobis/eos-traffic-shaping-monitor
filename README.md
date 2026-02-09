# Installation

```shell
git clone https://github.com/lobis/eos-traffic-shaping-monitor

cd eos-traffic-shaping-monitor

go install .

/root/go/bin/eos_traffic_shaping_monitor --help
```

Service file (`/etc/systemd/system/eos-traffic-shaping-monitor.service`)

```ini
[Unit]
Description=EOS Traffic Shaping Monitor
After=network.target

[Service]
Type=simple
User=root
ExecStart=/root/go/bin/eos_traffic_shaping_monitor --grpc-host lobisapa-dev-al9.cern.ch --grpc-port 50051 --prometheus-port 9987
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```
