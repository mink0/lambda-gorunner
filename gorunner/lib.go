package main

import (
	"log"
	"os"
)

func panic(err error) {
	// enable output
	log.SetOutput(os.Stderr)

	log.Fatal(err)
}

func getEnv(name, fallback string) string {
	value, exists := os.LookupEnv(name)
	if !exists {
		value = fallback
	}

	return value
}
