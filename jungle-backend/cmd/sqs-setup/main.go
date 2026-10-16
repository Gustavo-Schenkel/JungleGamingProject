package main

import (
	"context"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/messaging"
	"log"
	"time"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	q, err := messaging.NewQueue(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err = q.ProvisionQueues(ctx); err != nil {
		log.Fatal(err)
	}
	log.Print("SQS queues provisioned (command FIFO, event FIFO and DLQ); existing messages retained")
}
