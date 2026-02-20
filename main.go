package main

//go:generate buf generate

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

	pb "eos_traffic_shaping_monitor/eos-grpc-proto/build"
)

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
	threadLoopMicros = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "eos_io_thread_loop_microseconds",
			Help: "Time taken to execute internal thread loops in microseconds",
		},
		[]string{"loop_name", "stat_type"}, // Labels: loop_name (fst_limits, estimators), stat_type (mean, min, max)
	)
)

func init() {
	prometheus.MustRegister(readBytes, writeBytes, threadLoopMicros)
}

func main() {
	eosGrpcHost := flag.String("grpc-host", "localhost", "EOS MGM gRPC Host")
	eosGrpcPort := flag.String("grpc-port", "50051", "EOS MGM gRPC Port")
	prometheusPort := flag.String("prometheus-port", "9987", "Prometheus HTTP Port")
	prometheusDisable := flag.Bool("enable-prometheus", false, "Disable Prometheus metrics endpoint")
	topN := flag.Uint("n", 1000, "Top N entries to request")
	flag.Parse()

	if !*prometheusDisable {
		log.Println("Prometheus metrics endpoint enabled.")

		go func() {
			http.Handle("/metrics", promhttp.Handler())
			log.Printf("Prometheus metrics available at :%s/metrics", *prometheusPort)
			log.Fatal(http.ListenAndServe(":"+*prometheusPort, nil))
		}()
	} else {
		log.Println("Prometheus metrics endpoint disabled.")
	}

	var mgmHost = fmt.Sprintf("%s:%s", *eosGrpcHost, *eosGrpcPort)
	conn, err := grpc.NewClient(mgmHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewEosClient(conn)

	runMonitor(client, uint32(*topN))
}

func runMonitor(client pb.EosClient, topN uint32) {
	req := &pb.TrafficShapingRateRequest{
		Estimators: []pb.TrafficShapingRateRequest_Estimators{
			pb.TrafficShapingRateRequest_EMA_1_SECONDS,
			pb.TrafficShapingRateRequest_EMA_5_SECONDS,
			pb.TrafficShapingRateRequest_SMA_1_SECONDS,
			pb.TrafficShapingRateRequest_SMA_5_SECONDS,
			pb.TrafficShapingRateRequest_SMA_1_MINUTES,
			pb.TrafficShapingRateRequest_SMA_5_MINUTES,
		},
		IncludeTypes: []pb.TrafficShapingRateRequest_EntityType{
			pb.TrafficShapingRateRequest_ENTITY_APP,
			pb.TrafficShapingRateRequest_ENTITY_UID,
			pb.TrafficShapingRateRequest_ENTITY_GID,
		},
		TopN:            &topN,
		SortByEstimator: pb.TrafficShapingRateRequest_SMA_1_MINUTES.Enum(),
	}

	stream, err := client.TrafficShapingRate(context.Background(), req)
	if err != nil {
		log.Fatalf("Error opening stream: %v", err)
	}

	log.Println("Connected to EOS IO Stream...")

	for {
		report, err := stream.Recv()
		if err != nil {
			log.Fatalf("Stream closed: %v", err)
		}

		// 1. Clear console and print headers FIRST
		fmt.Print("\033[H\033[2J")
		fmt.Printf("EOS IO Monitor | Last Update: %s\n\n", time.UnixMilli(report.TimestampMs).Format(time.RFC3339))

		// 2. Safely extract and print Thread Loop Stats
		if fst := report.FstLimitsUpdateThreadLoopStats; fst != nil {
			fmt.Printf("FST Limits Update | Mean: %s | Min: %s | Max: %s\n",
				time.Duration(fst.MeanElapsedTimeMicroSec)*time.Microsecond,
				time.Duration(fst.MinElapsedTimeMicroSec)*time.Microsecond,
				time.Duration(fst.MaxElapsedTimeMicroSec)*time.Microsecond,
			)

			// Export to Prometheus
			threadLoopMicros.WithLabelValues("fst_limits", "mean").Set(float64(fst.MeanElapsedTimeMicroSec))
			threadLoopMicros.WithLabelValues("fst_limits", "min").Set(float64(fst.MinElapsedTimeMicroSec))
			threadLoopMicros.WithLabelValues("fst_limits", "max").Set(float64(fst.MaxElapsedTimeMicroSec))
		}

		if est := report.EstimatorsUpdateThreadLoopStats; est != nil {
			fmt.Printf("Estimators Update | Mean: %s | Min: %s | Max: %s\n",
				time.Duration(est.MeanElapsedTimeMicroSec)*time.Microsecond,
				time.Duration(est.MinElapsedTimeMicroSec)*time.Microsecond,
				time.Duration(est.MaxElapsedTimeMicroSec)*time.Microsecond,
			)

			// Export to Prometheus
			threadLoopMicros.WithLabelValues("estimators", "mean").Set(float64(est.MeanElapsedTimeMicroSec))
			threadLoopMicros.WithLabelValues("estimators", "min").Set(float64(est.MinElapsedTimeMicroSec))
			threadLoopMicros.WithLabelValues("estimators", "max").Set(float64(est.MaxElapsedTimeMicroSec))
		}
		fmt.Println()

		// 3. Reset the vector metrics BEFORE processing the new batch
		readBytes.Reset()
		writeBytes.Reset()

		// 4. Process, Print, and Export the details LAST
		printAndExportApps(report.AppStats)
		printAndExportUsers(report.UserStats)
		printAndExportGroups(report.GroupStats)
	}
}

// --- Helper Functions ---
func printAndExportApps(stats []*pb.AppRateEntry) {
	if len(stats) == 0 {
		return
	}
	fmt.Println("--- Top Applications ---")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "App\tEstimator\tRead/s\tWrite/s")

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

func printAndExportUsers(stats []*pb.UserRateEntry) {
	if len(stats) == 0 {
		return
	}
	fmt.Println("--- Top Users ---")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "UID\tWindow\tRead/s\tWrite/s")

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

func exportMetric(eType, id, win string, s *pb.RateStats) {
	readBytes.WithLabelValues(eType, id, win).Set(s.BytesReadPerSec)
	writeBytes.WithLabelValues(eType, id, win).Set(s.BytesWrittenPerSec)
}

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
