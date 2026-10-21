package main

import (
	"fmt"
	"net/http"
	"strconv"
)

var memorySink [][]byte

func main() {
	// 1. GET /health — проверка жизнеспособности сервиса
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	})

	// 2. GET /eat?mb=N — выделяет N мегабайт и удерживает их в RAM
	http.HandleFunc("/eat", func(w http.ResponseWriter, r *http.Request) {
		mbStr := r.URL.Query().Get("mb")
		mb, err := strconv.Atoi(mbStr)
		if err != nil || mb <= 0 {
			http.Error(w, "Параметр 'mb' должен быть положительным числом", http.StatusBadRequest)
			return
		}

		for i := 0; i < mb; i++ {
			chunk := make([]byte, 1024*1024)
			for j := range chunk {
				chunk[j] = 1
			}
			memorySink = append(memorySink, chunk)
		}

		fmt.Fprintf(w, "Выделено %d MB. Всего удерживается: %d MB\n", mb, len(memorySink))
	})

	// 3. GET /burn — нагружает одно ядро CPU на 100% в бесконечном цикле
	http.HandleFunc("/burn", func(w http.ResponseWriter, r *http.Request) {
		go func() {
			for {
				// Пустой бесконечный цикл утилизирует 100% одного ядра процессора
			}
		}()
		w.Write([]byte("CPU burn запущен на одном ядре в горутине\n"))
	})

	fmt.Println("Сервер запущен на :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("Ошибка запуска сервера: %v\n", err)
	}
}