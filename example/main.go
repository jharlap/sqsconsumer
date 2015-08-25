package main

import (
	"log"
	"os"
	"os/signal"
	"time"

	"fmt"
	"math/rand"

	goexpvar "expvar"
	"net/http"
	"runtime"

	"github.com/Wattpad/sqsconsumer"
	"github.com/go-kit/kit/metrics"
	"github.com/go-kit/kit/metrics/expvar"
	"golang.org/x/net/context"
)

// build with -ldflags "-X main.revision a123"
var revision = "UNKNOWN"

func init() {
	goexpvar.NewString("version").Set(revision)
}

func main() {
	region := "us-east-1"
	queueName := "push_gcm"
	numFetchers := 1
	numHandlers := 3

	// set up an SQS service instance
	s, err := sqsconsumer.SQSServiceForRegionAndQueue(region, queueName)
	if err != nil {
		log.Printf("Could not set up queue '%s': %s", queueName, err)
		os.Exit(1)
	}

	// set up a context which will gracefully cancel the worker on interrupt
	fetchCtx, cancelFetch := context.WithCancel(context.Background())
	term := make(chan os.Signal, 1)
	signal.Notify(term, os.Interrupt, os.Kill)
	go func() {
		<-term
		log.Println("Starting graceful shutdown")
		cancelFetch()
	}()

	// set up metrics
	exposeMetrics()
	ms := fmt.Sprintf("%s.success", queueName)
	mf := fmt.Sprintf("%s.fail", queueName)
	mt := fmt.Sprintf("%s.time", queueName)
	track := sqsconsumer.TrackMetricsMiddleware(ms, mf, mt)

	// set up middleware stack
	delCtx, cancelDelete := context.WithCancel(context.Background())
	middleware := sqsconsumer.DefaultMiddlewareStack(delCtx, s, numHandlers)
	middleware = append(middleware, track)

	// wrap the handler
	handler := sqsconsumer.ApplyDecoratorsToHandler(processMessage, middleware...)

	// start the consumers
	log.Println("Starting queue consumers")

	e := make(chan error)
	for i := 0; i < numFetchers; i++ {
		go func() {
			// create the consumer and bind it to a queue and processor function
			c := sqsconsumer.NewConsumer(s, handler)

			// start running the consumer with a context that will be cancelled when a graceful shutdown is requested
			e <- c.Run(fetchCtx)
		}()
	}

	// wait for all the consumers to exit cleanly
	for i := 0; i < numFetchers; i++ {
		<-e
	}
	cancelDelete()
	log.Println("Shutdown complete")
}

// processMessage is an example processor function which randomly errors or delays processing and demonstrates using the context
func processMessage(ctx context.Context, msg string) error {
	log.Printf("Starting processMessage for msg %s", msg)

	// simulate random errors and random delays in message processing
	r := rand.Intn(10)
	if r < 3 {
		return fmt.Errorf("a random error processing msg: %s", msg)
		//	} else if r < 6 {
		//		log.Printf("Sleeping for msg %s", msg)
		//		time.Sleep(45 * time.Second)
	}

	// handle cancel requests
	select {
	case <-ctx.Done():
		log.Println("Context done so aborting processing message:", msg)
		return ctx.Err()
	default:
	}

	// do the "work"
	log.Printf("MSG: '%s'", msg)
	return nil
}

func exposeMetrics() {
	v := exposed{
		goroutines: expvar.NewGauge("total_goroutines"),
		uptime:     expvar.NewGauge("process_uptime_seconds"),
	}

	start := time.Now()

	go func() {
		for range time.Tick(5 * time.Second) {
			v.goroutines.Set(float64(runtime.NumGoroutine()))
			v.uptime.Set(time.Since(start).Seconds())
		}
	}()

	log.Println("HTTP expvars on port 8123")
	go http.ListenAndServe("localhost:8123", nil)
}

type exposed struct {
	// static
	version *string

	// dynamic
	goroutines metrics.Gauge
	uptime     metrics.Gauge
}
