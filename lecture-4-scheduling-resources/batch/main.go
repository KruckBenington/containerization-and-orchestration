package main

import (
	"flag"
	"fmt"
	"runtime"
	"time"
)

func main() {
	memMB := flag.Int("mem", 128, "Memory to allocate and hold in MB")
	flag.Parse()

	fmt.Printf("Starting batch workload: target memory %d MB...\n", *memMB)

	var sink [][]byte
	for i := 0; i < *memMB; i++ {
		block := make([]byte, 1024*1024)
		for j := range block {
			block[j] = 1
		}
		sink = append(sink, block)
	}

	fmt.Println("Memory allocated. Burning CPU across available cores...")

	cores := runtime.NumCPU()
	for i := 0; i < cores; i++ {
		go func() {
			for {
			}
		}()
	}

	for {
		time.Sleep(10 * time.Second)
	}
}
