package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	URL         string         `json:"url"`
	Concurrency int            `json:"concurrency"`
	Requests    int            `json:"requests"`
	Warmup      int            `json:"warmup"`
	Successes   int            `json:"successes"`
	Errors      int            `json:"errors"`
	StatusCodes map[string]int `json:"statusCodes"`
	ElapsedMS   float64        `json:"elapsedMs"`
	RequestsSec float64        `json:"requestsPerSecond"`
	P50MS       float64        `json:"p50Ms"`
	P95MS       float64        `json:"p95Ms"`
	P99MS       float64        `json:"p99Ms"`
	MaxMS       float64        `json:"maxMs"`
}

func main() {
	url := flag.String("url", "", "URL to benchmark")
	concurrency := flag.Int("concurrency", 1, "number of concurrent workers")
	requests := flag.Int("requests", 2000, "number of measured requests")
	warmup := flag.Int("warmup", 20, "warmup requests before measurement")
	timeout := flag.Duration("timeout", 5*time.Second, "per-request timeout")
	flag.Parse()

	if *url == "" || *concurrency < 1 || *requests < 1 || *warmup < 0 {
		fmt.Fprintln(os.Stderr, "url is required and concurrency/requests must be positive")
		os.Exit(2)
	}

	transport := &http.Transport{
		MaxIdleConns:        *concurrency * 2,
		MaxIdleConnsPerHost: *concurrency * 2,
		MaxConnsPerHost:     *concurrency * 2,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
	}
	client := &http.Client{Transport: transport, Timeout: *timeout}
	defer transport.CloseIdleConnections()

	for i := 0; i < *warmup; i++ {
		if err := request(client, *url); err != nil {
			fmt.Fprintf(os.Stderr, "warmup request failed: %v\n", err)
			os.Exit(1)
		}
	}

	durations := make([]time.Duration, *requests)
	statusCodes := make(map[int]int)
	var statusMu sync.Mutex
	var next atomic.Int64
	var successes atomic.Int64
	var errorsCount atomic.Int64

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(*concurrency)
	for worker := 0; worker < *concurrency; worker++ {
		go func() {
			defer wg.Done()
			for {
				index := int(next.Add(1) - 1)
				if index >= *requests {
					return
				}
				requestStart := time.Now()
				status, err := measuredRequest(client, *url)
				durations[index] = time.Since(requestStart)
				if err != nil {
					errorsCount.Add(1)
					continue
				}
				statusMu.Lock()
				statusCodes[status]++
				statusMu.Unlock()
				if status >= 200 && status < 300 {
					successes.Add(1)
				} else {
					errorsCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	encodedStatuses := make(map[string]int, len(statusCodes))
	for code, count := range statusCodes {
		encodedStatuses[strconv.Itoa(code)] = count
	}

	out := result{
		URL:         *url,
		Concurrency: *concurrency,
		Requests:    *requests,
		Warmup:      *warmup,
		Successes:   int(successes.Load()),
		Errors:      int(errorsCount.Load()),
		StatusCodes: encodedStatuses,
		ElapsedMS:   milliseconds(elapsed),
		RequestsSec: float64(*requests) / elapsed.Seconds(),
		P50MS:       milliseconds(percentile(sorted, 0.50)),
		P95MS:       milliseconds(percentile(sorted, 0.95)),
		P99MS:       milliseconds(percentile(sorted, 0.99)),
		MaxMS:       milliseconds(sorted[len(sorted)-1]),
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if out.Errors != 0 {
		os.Exit(3)
	}
}

func request(client *http.Client, url string) error {
	_, err := measuredRequest(client, url)
	return err
}

func measuredRequest(client *http.Client, url string) (int, error) {
	response, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	_, copyErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if copyErr != nil {
		return response.StatusCode, copyErr
	}
	if closeErr != nil {
		return response.StatusCode, closeErr
	}
	return response.StatusCode, nil
}

func percentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * fraction)
	return sorted[index]
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
