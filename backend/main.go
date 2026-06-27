package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func helloHandler(w http.ResponseWriter, r *http.Request) {
	// We want to know WHICH server responded to us, so we will print the host/port in the response.
	fmt.Fprintf(w, "Hello from Backend Server running on port %s!\n", r.Host)
}

func main() {
	// 1. Define a command-line flag named "port" with a default value of "8081"
	port := flag.String("port", "8081", "Port for the server to listen on")
	
	// 2. Parse the flags passed in the terminal
	flag.Parse()

	http.HandleFunc("/", helloHandler)

	// 3. Use the dynamic port variable to start the server
	addr := ":" + *port
	log.Printf("Backend Server starting on %s...\n", addr)
	
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
