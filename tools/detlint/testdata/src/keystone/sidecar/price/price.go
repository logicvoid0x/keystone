package price

import (
	"math/rand"
	"net/http"
	"time"
)

func median(xs []float64) float64 { return xs[len(xs)/2] }

func fetch() (*http.Response, time.Time, int) {
	go func() {}()
	resp, _ := http.Get("http://localhost")
	return resp, time.Now(), rand.Intn(2)
}
