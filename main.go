package main

import (
	"container/list"
	"errors"
	"fmt"
	"github.com/shopify/stats/env"
	"github.com/tobi/airbrake-go"
	"log"
	"os"
)

var (
	fileWorker         []*S3Connection
	dirWorker          []*S3Connection
	PendingDirectories int

	DirCollector chan string
	NextDir      chan string

	dirWorkersFinished chan int
	fileWorkerQuit     chan int
	dirWorkerQuit      chan int
)

func init() {
	CopyFiles = make(chan string, 1000)
	DeleteFiles = make(chan string, 100)

	fileWorkerQuit = make(chan int)
	dirWorkerQuit = make(chan int)

	dirWorkersFinished = make(chan int)

	DirCollector = make(chan string)
	NextDir = make(chan string)
}

func main() {
	airbrake.Endpoint = "https://exceptions.shopify.com/notifier_api/v2/notices.xml"
	airbrake.ApiKey = "795dbf40b8743457f64fe9b9abc843fa"

	if len(env.Get("log")) > 0 {
		logFile, err := os.OpenFile(env.Get("log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			err = errors.New(fmt.Sprintf("Could not open log file %s for writing: %s", env.Get("log"), err.Error()))
			airbrake.Error(err, nil)
			log.Fatal(err)
		}
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	log := 300

	if log > 0 {
		go statsWorker(log)
	}

	readConfig()

	setup()

	PendingDirectories += 1
	DirCollector <- ""

	<-dirWorkersFinished
	shutdown()
}

func setup() {
	fileWorker = make([]*S3Connection, Config.FileWorkers)
	dirWorker = make([]*S3Connection, Config.DirWorkers)

	Errors = list.New()

	// spawn workers
	log.Printf("Spawning %d file workers", Config.FileWorkers)

	for i := 0; i < Config.FileWorkers; i++ {
		fileWorker[i] = S3Init()
		go fileWorker[i].fileCopier(fileWorkerQuit)
	}

	// 1 worker for the directory queue manager
	go DirManager()

	// N directory workers
	for i := 0; i < Config.DirWorkers; i++ {
		dirWorker[i] = S3Init()
		go dirWorker[i].dirWorker(dirWorkerQuit)
	}
}

func shutdown() {
	log.Printf("Shutting down..")
	close(CopyFiles)
	close(DirCollector)

	printStats()
	finished := 0
	for finished < Config.FileWorkers {
		finished += <-fileWorkerQuit
		log.Printf("File Worker quit..")
	}

	printStats()
	finished = 0
	for finished < Config.DirWorkers {
		finished += <-dirWorkerQuit
		log.Printf("Directory Worker quit..")
	}

	log.Printf("Final stats:")
	printStats()

	if Errors.Len() > 0 {
		log.Printf("%v Errors:", Errors.Len())
		for Errors.Len() > 0 {
			log.Printf("%v", Errors.Remove(Errors.Front()))
		}
	}
}
