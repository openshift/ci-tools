package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

const defaultPullSpec = "quay.io/openshift-release-dev/ocp-release:4.21.25-multi"

func main() {
	pullSpec := flag.String("pullspec", defaultPullSpec, "pullSpec value returned by the mock release service")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"name":        "4.21.25",
			"phase":       "Accepted",
			"pullSpec":    *pullSpec,
			"downloadURL": "https://example.com/4.21.25",
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("http://%s/api/v1/releasestream\n", listener.Addr())

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "mock release server failed: %v\n", err)
			os.Exit(1)
		}
	}()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	<-signalCh
	if err := server.Shutdown(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown mock release server: %v\n", err)
		os.Exit(1)
	}
}
