package main

import (
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/Financial-Times/publish-carousel/cms"
	"github.com/Financial-Times/publish-carousel/native"
	"github.com/Financial-Times/publish-carousel/resources"
	"github.com/Financial-Times/publish-carousel/s3"
	"github.com/Financial-Times/publish-carousel/scheduler"
	"github.com/Financial-Times/publish-carousel/tasks"
	"github.com/Financial-Times/service-status-go/httphandlers"
	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
	cli "gopkg.in/urfave/cli.v1"
)

func init() {
	f := &log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339Nano,
	}

	log.SetFormatter(f)
}

func main() {
	app := cli.NewApp()
	app.Name = "publish-carousel"
	app.Usage = "A microservice that continuously republishes content and annotations available in the native store."

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "cycles",
			Value: "./cycles.yml",
			Usage: "Path to the YML cycle configuration file.",
		},
		cli.StringFlag{
			Name:   "mongo-db",
			Value:  "localhost:27017",
			EnvVar: "MONGO_DB_URL",
			Usage:  "The Mongo DB connection url string (comma delimited).",
		},
		cli.StringFlag{
			Name:   "aws-region",
			Value:  "eu-west-1",
			EnvVar: "AWS_REGION",
			Usage:  "The AWS Region for this cluster.",
		},
		cli.StringFlag{
			Name:   "s3-bucket",
			Value:  "com.ft.universalpublishing.publish-carousel.dynpub-uk",
			EnvVar: "S3_BUCKET",
			Usage:  "The S3 Bucket to save carousel states.",
		},
		cli.IntFlag{
			Name:   "mongo-timeout",
			Value:  30000,
			EnvVar: "MONGO_DB_TIMEOUT",
			Usage:  "The timeout (in milliseconds) for Mongo DB connections.",
		},
	}

	app.Action = func(ctx *cli.Context) {
		log.Info("Starting the Publish Carousel.")

		s3rw := s3.NewReadWriter(ctx.String("aws-region"), ctx.String("s3-bucket"))
		stateRw := scheduler.NewS3MetadataReadWriter(s3rw)

		mongo := native.NewMongoDatabase(ctx.String("mongo-db"), ctx.Int("mongo-timeout"))

		reader := native.NewMongoNativeReader(mongo)
		notifier := cms.NewNotifier()

		task := tasks.NewNativeContentPublishTask(reader, notifier)

		sched, _ := scheduler.LoadSchedulerFromFile(ctx.String("cycles"), mongo, task, stateRw) //TODO: do something with this error

		sched.RestorePreviousState()
		sched.Start()

		shutdown(sched)
		serve(mongo, sched, s3rw)
	}

	app.Run(os.Args)
}

func shutdown(sched scheduler.Scheduler) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go func() {
		for sig := range signals {
			if sig == os.Interrupt {
				log.Info("Saving current carousel state to S3.")
				sched.SaveCycleMetadata()
				os.Exit(0)
			}
		}
	}()
}

func serve(mongo native.DB, sched scheduler.Scheduler, s3rw s3.ReadWriter) {
	r := mux.NewRouter()
	r.HandleFunc(httphandlers.BuildInfoPath, httphandlers.BuildInfoHandler).Methods("GET")
	r.HandleFunc(httphandlers.PingPath, httphandlers.PingHandler).Methods("GET")

	r.HandleFunc(httphandlers.GTGPath, resources.GTG(mongo, s3rw)).Methods("GET")
	r.HandleFunc("/__health", resources.Health(mongo, s3rw)).Methods("GET")

	r.HandleFunc("/cycles", resources.GetCycles(sched)).Methods("GET")
	r.HandleFunc("/cycles", resources.CreateCycle(sched)).Methods("POST")

	r.HandleFunc("/cycles/{id}", resources.GetCycleForID(sched)).Methods("GET")
	r.HandleFunc("/cycles/{id}", resources.DeleteCycle(sched)).Methods("DELETE")

	r.HandleFunc("/cycles/{id}/pause", resources.PauseCycle(sched)).Methods("POST")
	r.HandleFunc("/cycles/{id}/resume", resources.ResumeCycle(sched)).Methods("POST")
	r.HandleFunc("/cycles/{id}/stop", resources.StopCycle(sched)).Methods("POST")

	http.Handle("/", r)
	log.Info("Publish Carousel Started!")

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.WithError(err).Panic("Couldn't set up HTTP listener")
	}
}
