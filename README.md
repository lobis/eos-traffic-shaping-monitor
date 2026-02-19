# Installation

```shell
git clone https://github.com/lobis/eos-traffic-shaping-monitor
cd eos-traffic-shaping-monitor
```

```shell
GOBIN=/usr/local/bin go install .
chmod +x /usr/local/bin/eos_traffic_shaping_monitor
chmod 755 /usr/local/bin/eos_traffic_shaping_monitor
restorecon -v /usr/local/bin/eos_traffic_shaping_monitor
/usr/local/bin/eos_traffic_shaping_monitor --help
```

Service file (`/etc/systemd/system/eos-traffic-shaping-monitor.service`)

```ini
[Unit]
Description=EOS Traffic Shaping Monitor
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/eos_traffic_shaping_monitor --grpc-host lobisapa-dev-al9.cern.ch --grpc-port 50051 --prometheus-port 9987
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## Generate protobuf code

```shell
protoc \
  --plugin=protoc-gen-go=$(go env GOPATH)/bin/protoc-gen-go \
  --plugin=protoc-gen-go-grpc=$(go env GOPATH)/bin/protoc-gen-go-grpc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  proto/TrafficShaping.proto
```


```shell
protoc \
  --plugin=protoc-gen-go=$(go env GOPATH)/bin/protoc-gen-go \
  --plugin=protoc-gen-go-grpc=$(go env GOPATH)/bin/protoc-gen-go-grpc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  proto/TrafficShapingMonitor.proto
```