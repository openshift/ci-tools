package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	cluster := flag.String("cluster", "build09", "Cluster to return for all dispatch requests.")
	port := flag.Int("port", 8080, "Port to listen on.")
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Job string `json:"job"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("dispatch %s -> %s", req.Job, *cluster)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct {
			Cluster string `json:"cluster"`
		}{Cluster: *cluster}); err != nil {
			log.Printf("encode error: %v", err)
		}
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("staging-dispatcher listening on %s, always returning %q", addr, *cluster)
	log.Fatal(http.ListenAndServe(addr, nil))
}
