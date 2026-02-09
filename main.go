package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	// UPDATE THIS PATH to match where you generated your Go proto files
	pb "eos_traffic_shaping_monitor/proto"
)

// --- Prometheus Metrics ---
var (
	readBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "eos_io_read_bytes_per_second",
			Help: "Current read throughput in bytes/sec",
		},
		[]string{"entity_type", "id", "window"},
	)
	writeBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "eos_io_write_bytes_per_second",
			Help: "Current write throughput in bytes/sec",
		},
		[]string{"entity_type", "id", "window"},
	)
	readOps = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "eos_io_read_ops_per_second",
			Help: "Current read IOPS",
		},
		[]string{"entity_type", "id", "window"},
	)
	writeOps = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "eos_io_write_ops_per_second",
			Help: "Current write IOPS",
		},
		[]string{"entity_type", "id", "window"},
	)
)

func init() {
	// Register metrics with Prometheus
	prometheus.MustRegister(readBytes, writeBytes, readOps, writeOps)
}

func main() {
	mgmHost := flag.String("mgm", "lobisapa-dev-al9:50051", "EOS MGM gRPC Host:Port")
	promPort := flag.String("port", "9090", "Prometheus HTTP Port")
	topN := flag.Uint("n", 10, "Top N entries to request")
	flag.Parse()

	// 1. Start Prometheus Server (Background)
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Printf("Prometheus metrics available at :%s/metrics", *promPort)
		log.Fatal(http.ListenAndServe(":"+*promPort, nil))
	}()

	// 2. Connect to EOS MGM
	conn, err := grpc.Dial(*mgmHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewRateReportingServiceClient(conn)

	// 3. Start Streaming Loop
	runMonitor(client, uint32(*topN))
}

func runMonitor(client pb.RateReportingServiceClient, topN uint32) {
	// Prepare Request
	// We ask for 5s (Spikes) and 1m (Avg) windows
	req := &pb.RateRequest{
		Windows: []pb.RateRequest_TimeWindow{
			pb.RateRequest_WINDOW_LIVE_5S,
			pb.RateRequest_WINDOW_AVG_1M,
		},
		IncludeTypes: []pb.RateRequest_EntityType{
			pb.RateRequest_ENTITY_APP,
			pb.RateRequest_ENTITY_UID,
		},
		TopN:         &topN,
		SortByWindow: pb.RateRequest_WINDOW_LIVE_5S.Enum(), // Sort by instant spikes
	}

	stream, err := client.StreamRates(context.Background(), req)
	if err != nil {
		log.Fatalf("Error opening stream: %v", err)
	}

	log.Println("Connected to EOS IO Stream...")

	for {
		report, err := stream.Recv()
		if err != nil {
			log.Fatalf("Stream closed: %v", err)
		}

		// Clear console (ANSI escape) for "top"-like effect
		fmt.Print("\033[H\033[2J")
		fmt.Printf("EOS IO Monitor | Last Update: %s\n\n", time.UnixMilli(report.TimestampMs).Format(time.RFC3339))

		// Reset Metrics to avoid stale data (optional, but good for "Top N" views)
		readBytes.Reset()
		writeBytes.Reset()

		// Process & Print
		printAndExportApps(report.AppStats)
		printAndExportUsers(report.UserStats)
	}
}

// --- Helper: App Stats ---
func printAndExportApps(stats []*pb.AppRateEntry) {
	if len(stats) == 0 {
		return
	}
	fmt.Println("--- Top Applications ---")

	// Init TabWriter: output, minwidth, tabwidth, padding, padchar, flags
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	// Print Header (Use \t for columns)
	fmt.Fprintln(w, "App\tWindow\tRead/s\tWrite/s\tR-IOPS\tW-IOPS")

	for _, entry := range stats {
		for _, s := range entry.Stats {
			winName := s.Window.String()

			// Export to Prometheus
			exportMetric("app", entry.AppName, winName, s)

			// Print Row
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.1f\t%.1f\n",
				entry.AppName,
				winName,
				humanizeBytes(s.BytesReadPerSec),
				humanizeBytes(s.BytesWrittenPerSec),
				s.IopsRead,
				s.IopsWrite,
			)
		}
	}
	// Flush buffer to stdout
	w.Flush()
	fmt.Println()
}

// --- Helper: User Stats ---
func printAndExportUsers(stats []*pb.UserRateEntry) {
	if len(stats) == 0 {
		return
	}
	fmt.Println("--- Top Users ---")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "UID\tWindow\tRead/s\tWrite/s\tR-IOPS\tW-IOPS")

	for _, entry := range stats {
		uidStr := strconv.Itoa(int(entry.Uid))

		for _, s := range entry.Stats {
			winName := s.Window.String()

			exportMetric("uid", uidStr, winName, s)

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.1f\t%.1f\n",
				uidStr,
				winName,
				humanizeBytes(s.BytesReadPerSec),
				humanizeBytes(s.BytesWrittenPerSec),
				s.IopsRead,
				s.IopsWrite,
			)
		}
	}
	w.Flush()
	fmt.Println()
}

// --- Common Export Logic ---
func exportMetric(eType, id, win string, s *pb.RateStats) {
	readBytes.WithLabelValues(eType, id, win).Set(s.BytesReadPerSec)
	writeBytes.WithLabelValues(eType, id, win).Set(s.BytesWrittenPerSec)
	readOps.WithLabelValues(eType, id, win).Set(s.IopsRead)
	writeOps.WithLabelValues(eType, id, win).Set(s.IopsWrite)
}

// --- Utils ---
func humanizeBytes(s float64) string {
	sizes := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	val := s
	for val >= 1024 && i < len(sizes)-1 {
		val /= 1024
		i++
	}
	return fmt.Sprintf("%.2f %s", val, sizes[i])
}
