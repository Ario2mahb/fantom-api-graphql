// Package main implements the API server entry point.
package main

import (
	"fantom-api-graphql/cmd/apiserver/build"
	"fantom-api-graphql/internal/config"
	"fantom-api-graphql/internal/graphql/resolvers"
	"fantom-api-graphql/internal/handlers"
	"fantom-api-graphql/internal/logger"
	"fantom-api-graphql/internal/repository"
	"fantom-api-graphql/internal/svc"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// apiServer implements the API server application
type apiServer struct {
	cfg          *config.Config
	log          logger.Logger
	api          resolvers.ApiResolver
	srv          *http.Server
	isVersionReq bool
}

// init initializes the API server
func (app *apiServer) init() {
	// make sure to capture version request and rescan depth
	flag.BoolVar(&app.isVersionReq, "v", false, "get the application version")

	// get the configuration including parsing the calling flags
	var err error
	app.cfg, err = config.Load()
	if nil != err {
		log.Fatal(err)
		return
	}

	// configure logger based on the configuration
	app.log = logger.New(app.cfg)

	// make sure to pass logger and config to internals
	repository.SetConfig(app.cfg)
	repository.SetLogger(app.log)
	resolvers.SetConfig(app.cfg)
	resolvers.SetLogger(app.log)
	svc.SetConfig(app.cfg)
	svc.SetLogger(app.log)

	// make the HTTP server
	app.makeHttpServer()
}

// run executes the API server function.
func (app *apiServer) run() {
	// print the app version and exit if this is the only thing requested
	build.PrintVersion(app.cfg)
	if app.isVersionReq {
		return
	}

	// make sure to capture terminate signals
	app.observeSignals()

	// run services
	svc.Manager().Run()
	
	// start responding to requests
	app.log.Infof("welcome to Fantom GraphQL API server")
	app.log.Infof("listening for requests on %s", app.cfg.Server.BindAddress)
	log.Fatal(app.srv.ListenAndServe())
}

// makeHttpServer creates and configures the HTTP server to be used to serve incoming requests
func (app *apiServer) makeHttpServer() {
	// create request MUXer
	srvMux := new(http.ServeMux)

	// create HTTP server to handle our requests
	app.srv = &http.Server{
		Addr:              app.cfg.Server.BindAddress,
		ReadTimeout:       time.Second * time.Duration(app.cfg.Server.ReadTimeout),
		WriteTimeout:      time.Second * time.Duration(app.cfg.Server.WriteTimeout),
		IdleTimeout:       time.Second * time.Duration(app.cfg.Server.IdleTimeout),
		ReadHeaderTimeout: time.Second * time.Duration(app.cfg.Server.HeaderTimeout),
		Handler:           srvMux,
	}

	// setup handlers
	app.setupHandlers(srvMux)
}

// setupHandlers initializes an array of handlers for our HTTP API end-points.
func (app *apiServer) setupHandlers(mux *http.ServeMux) {
	// create root resolver
	app.api = resolvers.New()

	// setup GraphQL API handler
	h := http.TimeoutHandler(
		handlers.Api(app.cfg, app.log, app.api),
		time.Second*time.Duration(app.cfg.Server.ResolverTimeout),
		"Service timeout.",
	)
	mux.Handle("/api", h)
	mux.Handle("/graphql", h)

	// setup gas price estimator REST API resolver
	mux.Handle("/json/gas", handlers.GasPrice(app.log))

	// handle GraphiQL interface
	mux.Handle("/graphi", handlers.GraphiHandler(app.cfg.Server.DomainAddress, app.log))
}

// observeSignals setups terminate signals observation.
func (app *apiServer) observeSignals() {
	// log what we do
	app.log.Info("os signals captured")

	// make the signal consumer
	ts := make(chan os.Signal, 1)
	signal.Notify(ts, syscall.SIGINT, syscall.SIGTERM)

	// start monitoring
	go func() {
		// wait for the signal
		<-ts
		app.terminate()

		// we are done
		app.log.Info("api server done")
		os.Exit(0)
	}()
}

// terminate modules of the API server.
func (app *apiServer) terminate() {
	app.log.Notice("api server terminates")

	// terminate responder
	if err := app.srv.Close(); err != nil {
		app.log.Errorf("could not terminate HTTP listener")
	}

	// terminate observers, scanners and dispatchers, etc.
	if mgr := svc.Manager(); mgr != nil {
		mgr.Close()
	}

	// terminate connections to DB, blockchain, etc.
	if repo := repository.R(); repo != nil {
		repo.Close()
	}

	// close resolvers
	app.api.Close()
}
