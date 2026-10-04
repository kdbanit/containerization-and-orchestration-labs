package main

import (
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"sync"
)

// Держим выделенную память, чтобы её не съел GC.
var (
	held   [][]byte
	heldMu sync.Mutex
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})

	// /eat?mb=N — выделяет N МиБ и держит их.
	mux.HandleFunc("/eat", func(w http.ResponseWriter, r *http.Request) {
		nStr := r.URL.Query().Get("mb")
		n, err := strconv.Atoi(nStr)
		if err != nil || n <= 0 {
			http.Error(w, "bad mb", http.StatusBadRequest)
			return
		}

		heldMu.Lock()
		defer heldMu.Unlock()

		// Выделяем по кускам и каждый кусок реально трогаем,
		// иначе ядро не выделит физические страницы.
		for i := 0; i < n; i++ {
			buf := make([]byte, 1<<20) // 1 MiB
			for j := range buf {
				buf[j] = byte(j) // касаемся каждой страницы
			}
			held = append(held, buf)
		}
		fmt.Fprintf(w, "held %d MiB, total %d MiB\n", n, len(held))
	})

	// /burn — жжёт одно ядро в бесконечном цикле.
	mux.HandleFunc("/burn", func(w http.ResponseWriter, r *http.Request) {
		go func() {
			var x uint64
			for {
				x++
				// немного "работы", чтобы компилятор не выкинул цикл
				if x == 0 {
					runtime.Gosched()
				}
			}
		}()
		fmt.Fprint(w, "burning\n")
	})

	log.Println("api listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
