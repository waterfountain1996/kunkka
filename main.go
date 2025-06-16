package main

import (
	"log"
	"os"

	"github.com/waterfountain1996/kunkka/internal/torrent"
)

const peerID = "kunkka-eSRTIVSJpKFmS"

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		log.Println("error: torrent file argument not provided")
	}
	filename := os.Args[1]

	f, err := os.Open(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	t, err := torrent.Parse(f)
	if err != nil {
		log.Fatal(err)
	}

	dl := NewDownloader(t)
	if err := dl.Download(); err != nil {
		log.Fatalf("download: %v\n", err)
	}
}
