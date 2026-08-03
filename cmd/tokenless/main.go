// Command tokenless serves the gateway scenario server as a
// standalone binary for manual testing and terminal-automation harnesses.
// Go tests import the gateway package in-process instead.
//
// The first stdout line is the readiness protocol for harnesses:
//
//	tokenless listening on http://127.0.0.1:<port>
//
// One line per request is logged to stderr, including the resolved scenario
// and step from the X-Tokenless-* response headers.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/inference-gateway/tokenless/gateway"
)

func main() {
	host := flag.String("host", "127.0.0.1", "host/interface to bind (use 0.0.0.0 in a container)")
	port := flag.Int("port", 0, "port to listen on (0 picks a free port)")
	scenarios := flag.String("scenarios", "", "path to a scenarios YAML file (default: $TOKENLESS_SCENARIOS, then the built-in library)")
	model := flag.String("model", "", "override the OpenAI model id on /v1/models (use a bare id like gpt-4o when sitting behind a real gateway)")
	flag.Parse()

	defs, err := loadScenarios(*scenarios)
	if err != nil {
		log.Fatal(err)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", *host, *port))
	if err != nil {
		log.Fatalf("listening: %v", err)
	}

	fmt.Printf("tokenless listening on http://%s\n", ln.Addr())
	srv := gateway.New(defs)
	srv.Model = *model
	if err := http.Serve(ln, logRequests(srv)); err != nil {
		log.Fatalf("serving: %v", err)
	}
}

func loadScenarios(path string) (*gateway.ScenarioFile, error) {
	if path == "" {
		path = os.Getenv("TOKENLESS_SCENARIOS")
	}
	if path == "" {
		return gateway.Default(), nil
	}
	return gateway.LoadFile(path)
}

// logRequests prints one line per request to stderr so harnesses can follow
// which scenario and step each call resolved to.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		log.Printf("%s %s scenario=%q step=%s",
			r.Method, r.URL.Path,
			w.Header().Get("X-Tokenless-Scenario"),
			w.Header().Get("X-Tokenless-Step"))
	})
}
