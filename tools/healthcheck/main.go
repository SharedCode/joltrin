// Command healthcheck hits a local HTTP endpoint and exits 0 on a 2xx
// response, non-zero otherwise. Built as its own static binary so the
// runtime image can carry it without a shell or curl/wget, the tools a
// distroless base deliberately leaves out.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

// probe issues the GET and returns an error unless the response is 2xx.
func probe(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unhealthy status %d", resp.StatusCode)
	}
	return nil
}

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/api/health", "endpoint to probe")
	timeout := flag.Duration("timeout", 3*time.Second, "request timeout")
	flag.Parse()

	client := &http.Client{Timeout: *timeout}
	if err := probe(client, *url); err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		os.Exit(1)
	}
}
