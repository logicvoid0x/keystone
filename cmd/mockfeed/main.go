// Command mockfeed serves a controllable BTC/USD feed for tests and localnet.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/OWNER/keystone/sidecar/price/mockfeed"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18080", "listen address")
	usd := flag.String("price", "100000", "initial BTC/USD price")
	flag.Parse()

	s, err := mockfeed.New(*usd, nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("mockfeed listening on %s at $%s", *listen, *usd)
	log.Fatal(http.ListenAndServe(*listen, s.Handler()))
}
