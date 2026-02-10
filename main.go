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
// Removed IOPS metrics as requested
var (
	readBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "eos_io_read_bytes_per_second",
			Help: "Current read throughput in bytes/sec",
		},
		[]string{"entity_type", "id", "estimator"},
	)
	writeBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "eos_io_write_bytes_per_second",
			Help: "Current write throughput in bytes/sec",
		},
		[]string{"entity_type", "id", "estimator"},
	)
)

func init() {
	// Register metrics with Prometheus
	prometheus.MustRegister(readBytes, writeBytes)
}

func main() {
	eosGrpcHost := flag.String("grpc-host", "localhost", "EOS MGM gRPC Host")
	eosGrpcPort := flag.String("grpc-port", "50051", "EOS MGM gRPC Port")
	prometheusPort := flag.String("prometheus-port", "9987", "Prometheus HTTP Port")
	topN := flag.Uint("n", 1000, "Top N entries to request")
	flag.Parse()

	// 1. Start Prometheus Server (Background)
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Printf("Prometheus metrics available at :%s/metrics", *prometheusPort)
		log.Fatal(http.ListenAndServe(":"+*prometheusPort, nil))
	}()

	// 2. Connect to EOS MGM
	var mgmHost = fmt.Sprintf("%s:%s", *eosGrpcHost, *eosGrpcPort)
	conn, err := grpc.NewClient(mgmHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewRateReportingServiceClient(conn)

	// 3. Start Streaming Loop
	runMonitor(client, uint32(*topN))
}

func runMonitor(client pb.RateReportingServiceClient, topN uint32) {
	req := &pb.RateRequest{
		Estimators: []pb.RateRequest_Estimators{
			pb.RateRequest_EMA_5_SECONDS,
			pb.RateRequest_EMA_1_MINUTES,
			pb.RateRequest_EMA_5_MINUTES,
			pb.RateRequest_SMA_5_SECONDS,
			pb.RateRequest_SMA_1_MINUTES,
			pb.RateRequest_SMA_5_MINUTES,
		},
		IncludeTypes: []pb.RateRequest_EntityType{
			pb.RateRequest_ENTITY_APP,
			pb.RateRequest_ENTITY_UID,
			pb.RateRequest_ENTITY_GID, // Added GID support
		},
		TopN:            &topN,
		SortByEstimator: pb.RateRequest_SMA_1_MINUTES.Enum(),
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

		// Clear console (ANSI escape)
		fmt.Print("\033[H\033[2J")
		fmt.Printf("EOS IO Monitor | Last Update: %s\n\n", time.UnixMilli(report.TimestampMs).Format(time.RFC3339))

		// Reset Metrics to avoid stale data
		readBytes.Reset()
		writeBytes.Reset()

		// Process & Print
		printAndExportApps(report.AppStats)
		printAndExportUsers(report.UserStats)   // Assuming standard proto mapping (uid_stats -> UidStats)
		printAndExportGroups(report.GroupStats) // Added Groups
	}
}

// --- Helper: App Stats ---
func printAndExportApps(stats []*pb.AppRateEntry) {
	if len(stats) == 0 {
		return
	}
	fmt.Println("--- Top Applications ---")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "App\tEstimator\tRead/s\tWrite/s") // Removed IOPS columns

	for _, entry := range stats {
		for _, s := range entry.Stats {
			estimatorName := s.Window.String()

			exportMetric("app", entry.AppName, estimatorName, s)

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				entry.AppName,
				estimatorName,
				humanizeBytes(s.BytesReadPerSec),
				humanizeBytes(s.BytesWrittenPerSec),
			)
		}
	}
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
	fmt.Fprintln(w, "UID\tWindow\tRead/s\tWrite/s") // Removed IOPS columns

	for _, entry := range stats {
		uidStr := strconv.Itoa(int(entry.Uid))

		for _, s := range entry.Stats {
			winName := s.Window.String()

			exportMetric("user", uidStr, winName, s)

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				uidStr,
				winName,
				humanizeBytes(s.BytesReadPerSec),
				humanizeBytes(s.BytesWrittenPerSec),
			)
		}
	}
	w.Flush()
	fmt.Println()
}

// --- Helper: Group Stats (NEW) ---
func printAndExportGroups(stats []*pb.GroupRateEntry) {
	if len(stats) == 0 {
		return
	}
	fmt.Println("--- Top Groups ---")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "GID\tWindow\tRead/s\tWrite/s")

	for _, entry := range stats {
		gidStr := strconv.Itoa(int(entry.Gid))

		for _, s := range entry.Stats {
			winName := s.Window.String()

			exportMetric("group", gidStr, winName, s)

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				gidStr,
				winName,
				humanizeBytes(s.BytesReadPerSec),
				humanizeBytes(s.BytesWrittenPerSec),
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
