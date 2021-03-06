package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	_ "net/http/pprof"

	"github.com/Pallinder/go-randomdata"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	Version   string
	Branch    string
	GoVersion = runtime.Version()

	inFlightGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "in_flight_requests",
		Help: "A guage of requests currently being served by the wrapped handler",
	})

	counter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "requests_total",
		Help: "A counter for requests to the wrapped handler",
	},
		[]string{"code", "method"})

	duration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "request_duration_seconds",
			Help: "A histogram of latencies for requests.",
			// 50ms, 100ms, 200ms, 300ms, 500ms
			Buckets: []float64{.05, .1, .2, .3, .5},
		},
		[]string{"handler", "code", "method"},
	)

	buildInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "sla_build_info",
			Help: "A metric with a constant '1' value labeled by attributes from which sla was built.",
		},
		[]string{"version", "branch", "goversion"},
	)
)

func root(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	req := fmt.Sprintf("%s", r.URL.String())
	log.Println(name, base64.StdEncoding.EncodeToString([]byte(req)))
	if name == "" {
		name = randomdata.SillyName()
	}
	log.Println(name, req)
	start := time.Now()

	dep, err := base64.StdEncoding.DecodeString(r.URL.Query().Get("dep"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Fatal(err)
	}

	if string(dep) != "" {
		log.Printf("%s fetching dependency: %s", name, string(dep))
		res, err := http.Get(fmt.Sprintf("http://%s%s", r.Host, string(dep)))

		// OFTEN MISSING
		statusOK := res.StatusCode >= 200 && res.StatusCode < 300
		if !statusOK {
			http.Error(w, "not OK response", res.StatusCode)
			return
		}
		// OFTEN MISSING

		if err != nil {
			http.Error(w, err.Error(), res.StatusCode)
			// Prom crashes here
			return
		}
	}

	sleep, err := strconv.Atoi(r.URL.Query().Get("sleep"))
	if err == nil {
		// log.Println(fmt.Sprintf("%s sleeping: %d milliseconds", name, sleep))
		time.Sleep(time.Duration(sleep) * time.Millisecond)
	}
	code, err := strconv.Atoi(r.URL.Query().Get("code"))
	if err == nil {
		if code >= 200 {
			log.Println(fmt.Sprintf("Code: %d", code))
			w.WriteHeader(code)
		}
	}
	log.Printf("name %s dep %s code %d slept %d ms", name, string(dep), code, sleep)
	t := time.Now()
	fmt.Fprintln(w, fmt.Sprintf("Name: %s, Elapsed: %s ms, Slept: %d ms, with dep: %s", name, t.Sub(start), sleep, string(dep)))
}

func main() {
	buildInfo.WithLabelValues(Version, Branch, GoVersion).Set(1)

	// duration is partitioned by the HTTP method and handler. It uses custom
	// buckets based on the expected request duration.
	// https://prometheus.io/docs/practices/histograms/
	// sum(rate(request_duration_seconds_bucket{le="0.3"}[5m])) by (job)
	// /
	// sum(rate(request_duration_seconds_count[5m])) by (job)

	// Pprof server.
	// https://mmcloughlin.com/posts/your-pprof-is-showing
	// go func() {
	// 	log.Fatal(http.ListenAndServe(":8081", nil))
	// }()

	// Application server.
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	rootChain := promhttp.InstrumentHandlerInFlight(
		inFlightGauge,
		promhttp.InstrumentHandlerDuration(
			duration.MustCurryWith(prometheus.Labels{"handler": "root"}),
			promhttp.InstrumentHandlerCounter(counter, http.HandlerFunc(root))))

	mux.Handle("/", rootChain)

	log.Println("Listening on :" + os.Getenv("PORT"))
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), mux); err != nil {
		log.Fatal(err)
	}
}
