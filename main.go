package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/waterfountain1996/kunkka/internal/torrent"
)

const peerID = "kunkka-eSRTIVSJpKFmS"

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		log.Println("error: torrent file argument not provided")
	}
	filename := os.Args[1]

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	t, err := torrent.FromFile(filename)
	if err != nil {
		log.Fatal(err)
	}

	dl := NewDownloader(t)
	if err := dl.startDownload(ctx); err != nil {
		log.Fatalf("download: %v\n", err)
	}
}
