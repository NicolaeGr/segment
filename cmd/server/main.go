package main

import (
	"log"
	"net/http"

	"example.com/segments/internal/web"
)

func main() {
	addr := ":8080"
	log.Printf("segments demo listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, web.Router()); err != nil {
		log.Fatal(err)
	}
}
