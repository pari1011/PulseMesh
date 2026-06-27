package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Backend struct {
	URL               *url.URL
	ReverseProxy      *httputil.ReverseProxy
	ActiveConns       int64
	TotalRequests     int64
	TotalResponseTime int64
	IsAlive           bool
	Weight            int
	AdminStatus       string // "Auto", "ForceOnline", "ForceOffline"
	mux               sync.RWMutex
}

func (b *Backend) SetAlive(alive bool) {
	b.mux.Lock()
	b.IsAlive = alive
	b.mux.Unlock()
}

func (b *Backend) SetAdminStatus(status string) {
	b.mux.Lock()
	b.AdminStatus = status
	b.mux.Unlock()
}

func (b *Backend) GetState() (alive bool, status string, weight int) {
	b.mux.RLock()
	alive = b.IsAlive
	status = b.AdminStatus
	weight = b.Weight
	b.mux.RUnlock()
	return
}

func (b *Backend) IsRoutable() bool {
	alive, status, _ := b.GetState()
	if status == "ForceOffline" {
		return false
	}
	if status == "ForceOnline" {
		return true
	}
	return alive // Auto mode relies on health check
}

type ServerPool struct {
	backends  []*Backend
	algorithm string
	rrIndex   uint64 // Counter for Round Robin logic
	mux       sync.RWMutex
}

func (s *ServerPool) AddBackend(b *Backend) {
	s.mux.Lock()
	s.backends = append(s.backends, b)
	s.mux.Unlock()
}

func (s *ServerPool) RemoveBackend(targetURL string) {
	s.mux.Lock()
	defer s.mux.Unlock()
	var newBackends []*Backend
	for _, b := range s.backends {
		if b.URL.String() != targetURL {
			newBackends = append(newBackends, b)
		}
	}
	s.backends = newBackends
}

func (s *ServerPool) GetBackends() []*Backend {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.backends
}

func (s *ServerPool) GetAlgorithm() string {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.algorithm
}

func (s *ServerPool) SetAlgorithm(algo string) {
	s.mux.Lock()
	s.algorithm = algo
	s.mux.Unlock()
}

func healthCheck(pool *ServerPool) {
	client := http.Client{Timeout: 2 * time.Second}
	for {
		for _, b := range pool.GetBackends() {
			resp, err := client.Get(b.URL.String())
			if err != nil || resp.StatusCode >= 500 {
				if alive, _, _ := b.GetState(); alive {
					log.Printf("🔴 Backend %s went DOWN!", b.URL.String())
					b.SetAlive(false)
				}
			} else {
				if alive, _, _ := b.GetState(); !alive {
					log.Printf("🟢 Backend %s came back UP!", b.URL.String())
					b.SetAlive(true)
				}
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
		time.Sleep(5 * time.Second)
	}
}

// Data structures for JSON API
type MetricsResponse struct {
	URL            string  `json:"url"`
	ActiveConns    int64   `json:"active_connections"`
	TotalRequests  int64   `json:"total_requests"`
	AvgResponseMs  float64 `json:"avg_response_time_ms"`
	IsAlive        bool    `json:"is_alive"`
	AdminStatus    string  `json:"admin_status"`
	Weight         int     `json:"weight"`
	IsRoutable     bool    `json:"is_routable"`
}

type DashboardResponse struct {
	Algorithm string            `json:"algorithm"`
	Backends  []MetricsResponse `json:"backends"`
}

func enableCors(w *http.ResponseWriter) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
	(*w).Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, DELETE")
	(*w).Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func main() {
	pool := &ServerPool{algorithm: "least_connections"}

	// Pre-add Server 1 (8081)
	parsed, _ := url.Parse("http://localhost:8081")
	pool.AddBackend(&Backend{
		URL:          parsed,
		ReverseProxy: httputil.NewSingleHostReverseProxy(parsed),
		IsAlive:      true,
		Weight:       1,
		AdminStatus:  "Auto",
	})

	go healthCheck(pool)

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		backends := pool.GetBackends()
		var routable []*Backend
		for _, b := range backends {
			if b.IsRoutable() {
				routable = append(routable, b)
			}
		}

		if len(routable) == 0 {
			http.Error(w, "All backend servers are offline!", http.StatusServiceUnavailable)
			return
		}

		var targetBackend *Backend
		algo := pool.GetAlgorithm()

		if algo == "round_robin" {
			idx := atomic.AddUint64(&pool.rrIndex, 1)
			targetBackend = routable[idx%uint64(len(routable))]
		} else if algo == "weighted_round_robin" {
			totalWeight := 0
			for _, b := range routable {
				_, _, weight := b.GetState()
				totalWeight += weight
			}
			idx := int(atomic.AddUint64(&pool.rrIndex, 1) % uint64(totalWeight))
			
			for _, b := range routable {
				_, _, weight := b.GetState()
				if idx < weight {
					targetBackend = b
					break
				}
				idx -= weight
			}
		} else { // least_connections
			var minConns int64 = -1
			for _, b := range routable {
				conns := atomic.LoadInt64(&b.ActiveConns)
				if minConns == -1 || conns < minConns {
					targetBackend = b
					minConns = conns
				}
			}
		}

		atomic.AddInt64(&targetBackend.ActiveConns, 1)
		atomic.AddInt64(&targetBackend.TotalRequests, 1)
		startTime := time.Now()

		defer func() {
			atomic.AddInt64(&targetBackend.ActiveConns, -1)
			duration := time.Since(startTime).Milliseconds()
			atomic.AddInt64(&targetBackend.TotalResponseTime, duration)
		}()

		targetBackend.ReverseProxy.ServeHTTP(w, r)
	})

	adminMux := http.NewServeMux()
	
	adminMux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		enableCors(&w)
		if r.Method == "OPTIONS" { return }

		backends := pool.GetBackends()
		var metricsList []MetricsResponse

		for _, b := range backends {
			reqs := atomic.LoadInt64(&b.TotalRequests)
			totalTime := atomic.LoadInt64(&b.TotalResponseTime)
			
			var avg float64 = 0
			if reqs > 0 {
				avg = float64(totalTime) / float64(reqs)
			}

			alive, status, weight := b.GetState()
			metricsList = append(metricsList, MetricsResponse{
				URL:           b.URL.String(),
				ActiveConns:   atomic.LoadInt64(&b.ActiveConns),
				TotalRequests: reqs,
				AvgResponseMs: avg,
				IsAlive:       alive,
				AdminStatus:   status,
				Weight:        weight,
				IsRoutable:    b.IsRoutable(),
			})
		}

		resp := DashboardResponse{
			Algorithm: pool.GetAlgorithm(),
			Backends:  metricsList,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	adminMux.HandleFunc("/api/add", func(w http.ResponseWriter, r *http.Request) {
		enableCors(&w)
		if r.Method == "OPTIONS" { return }
		
		serverURL := r.URL.Query().Get("url")
		weightStr := r.URL.Query().Get("weight")
		weight := 1
		if parsedW, err := strconv.Atoi(weightStr); err == nil && parsedW > 0 {
			weight = parsedW
		}

		parsed, err := url.Parse(serverURL)
		if err != nil || serverURL == "" {
			http.Error(w, "Invalid URL", http.StatusBadRequest)
			return
		}

		pool.AddBackend(&Backend{
			URL:          parsed,
			ReverseProxy: httputil.NewSingleHostReverseProxy(parsed),
			ActiveConns:  0,
			IsAlive:      true,
			Weight:       weight,
			AdminStatus:  "Auto",
		})
		log.Printf("🛠️ Added backend: %s (Weight: %d)", serverURL, weight)
		w.WriteHeader(http.StatusCreated)
	})

	adminMux.HandleFunc("/api/remove", func(w http.ResponseWriter, r *http.Request) {
		enableCors(&w)
		if r.Method == "OPTIONS" { return }
		
		serverURL := r.URL.Query().Get("url")
		pool.RemoveBackend(serverURL)
		log.Printf("🗑️ Removed backend: %s", serverURL)
		w.WriteHeader(http.StatusOK)
	})

	adminMux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		enableCors(&w)
		if r.Method == "OPTIONS" { return }
		
		serverURL := r.URL.Query().Get("url")
		status := r.URL.Query().Get("status")
		if status != "Auto" && status != "ForceOnline" && status != "ForceOffline" {
			http.Error(w, "Invalid status", http.StatusBadRequest)
			return
		}

		for _, b := range pool.GetBackends() {
			if b.URL.String() == serverURL {
				b.SetAdminStatus(status)
				log.Printf("🛡️ Updated status for %s to %s", serverURL, status)
				w.WriteHeader(http.StatusOK)
				return
			}
		}
	})

	adminMux.HandleFunc("/api/algorithm", func(w http.ResponseWriter, r *http.Request) {
		enableCors(&w)
		if r.Method == "OPTIONS" { return }
		
		algo := r.URL.Query().Get("algo")
		if algo != "least_connections" && algo != "round_robin" && algo != "weighted_round_robin" {
			http.Error(w, "Invalid algorithm", http.StatusBadRequest)
			return
		}

		pool.SetAlgorithm(algo)
		log.Printf("🔄 Switched algorithm to %s", algo)
		w.WriteHeader(http.StatusOK)
	})

	go func() {
		log.Println("Starting Admin Control Plane on :9001...")
		log.Fatal(http.ListenAndServe(":9001", adminMux))
	}()

	log.Println("Starting Load Balancer on :9000...")
	log.Fatal(http.ListenAndServe(":9000", proxyMux))
}
