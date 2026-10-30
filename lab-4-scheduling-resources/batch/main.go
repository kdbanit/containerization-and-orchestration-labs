// batch: burns CPU and holds memory in a loop — stand-in for "nightly analytics" (Lab 4).
package main

import (
	"log"
	"os"
	"runtime"
	"strconv"
	"time"
)

func main() {
	workers := envInt("CPU_WORKERS", runtime.NumCPU())
	memMiB := envInt("MEMORY_MIB", 64)
	log.Printf("batch starting: cpu_workers=%d memory_mib=%d", workers, memMiB)

	// Hold a chunk of RAM for the process lifetime (for Lab 4 memory-pressure experiments).
	blob := make([]byte, memMiB<<20)
	for i := 0; i < len(blob); i += 4096 {
		blob[i] = byte(i)
	}

	for i := 0; i < workers; i++ {
		go func(id int) {
			var x uint64
			for {
				x++
				if x%1_000_000 == 0 {
					runtime.Gosched()
				}
				_ = x
			}
		}(i)
	}

	// Touch memory periodically so it stays resident.
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		blob[time.Now().UnixNano()%int64(len(blob))] ^= 1
		log.Printf("batch alive workers=%d mem_mib=%d", workers, memMiB)
	}
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}
