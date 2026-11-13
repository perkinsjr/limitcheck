package brokenserver

import (
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestFixedWindowLeaksAndTokenDoesNot(t *testing.T) {
	rate := spec.Rate{Count: 3, Window: time.Second}
	fixed, err := Start(rate, "fixed")
	if err != nil {
		t.Fatal(err)
	}
	defer fixed.Close()
	token, err := Start(rate, "token")
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()

	boundary := time.Unix(0, (time.Now().UnixNano()/int64(time.Second)+1)*int64(time.Second))
	waitUntil(t, boundary.Add(-80*time.Millisecond))
	if got := blast(t, fixed.URL(), 3); got != 3 {
		t.Fatalf("fixed pre %d", got)
	}
	if got := blast(t, token.URL(), 3); got != 3 {
		t.Fatalf("token pre %d", got)
	}
	waitUntil(t, boundary.Add(30*time.Millisecond))
	if got := blast(t, fixed.URL(), 3); got != 3 {
		t.Fatalf("fixed post %d, want the leak", got)
	}
	if got := blast(t, token.URL(), 3); got != 0 {
		t.Fatalf("token allowed %d across the edge", got)
	}
}

func TestRaceAdmitsTheWholeBurst(t *testing.T) {
	s, err := Start(spec.Rate{Count: 3, Window: time.Second}, "race")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := blast(t, s.URL(), 8); got <= 3 {
		t.Fatalf("race allowed %d", got)
	}
}

func blast(t *testing.T, url string, n int) int {
	t.Helper()
	var wg sync.WaitGroup
	ok := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := http.Get(url)
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode >= 200 && res.StatusCode < 400 {
				ok[i] = 1
			}
		}(i)
	}
	wg.Wait()
	sum := 0
	for _, v := range ok {
		sum += v
	}
	return sum
}

func waitUntil(t *testing.T, at time.Time) {
	t.Helper()
	d := time.Until(at)
	if d > 0 {
		time.Sleep(d)
	}
}
