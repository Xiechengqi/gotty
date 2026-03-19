package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	noesctmpl "text/template"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"github.com/sorenisanerd/gotty/bindata"
	"github.com/sorenisanerd/gotty/pkg/homedir"
	"github.com/sorenisanerd/gotty/pkg/randomstring"
	"github.com/sorenisanerd/gotty/webtty"
)

// Server provides a webtty HTTP endpoint.
type Server struct {
	factory Factory
	options *Options

	upgrader         *websocket.Upgrader
	indexTemplate    *template.Template
	titleTemplate    *noesctmpl.Template
	manifestTemplate *template.Template
	sessionManager   *SessionManager

	// API components
	terminalStatus *TerminalStatus
	broadcastCtrl  *BroadcastController
	execManager    *ExecManager
}

// New creates a new instance of Server.
// Server will use the New() of the factory provided to handle each request.
func New(factory Factory, options *Options) (*Server, error) {
	indexData, err := bindata.Fs.ReadFile("static/index.html")
	if err != nil {
		panic("index not found") // must be in bindata
	}
	if options.IndexFile != "" {
		path := homedir.Expand(options.IndexFile)
		indexData, err = os.ReadFile(path)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read custom index file at `%s`", path)
		}
	}
	indexTemplate, err := template.New("index").Parse(string(indexData))
	if err != nil {
		panic("index template parse failed") // must be valid
	}

	manifestData, err := bindata.Fs.ReadFile("static/manifest.json")
	if err != nil {
		panic("manifest not found") // must be in bindata
	}
	manifestTemplate, err := template.New("manifest").Parse(string(manifestData))
	if err != nil {
		panic("manifest template parse failed") // must be valid
	}

	titleTemplate, err := noesctmpl.New("title").Parse(options.TitleFormat)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse window title format `%s`", options.TitleFormat)
	}

	var originChekcer func(r *http.Request) bool
	if options.WSOrigin != "" {
		matcher, err := regexp.Compile(options.WSOrigin)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to compile regular expression of Websocket Origin: %s", options.WSOrigin)
		}
		originChekcer = func(r *http.Request) bool {
			return matcher.MatchString(r.Header.Get("Origin"))
		}
	}

	return &Server{
		factory: factory,
		options: options,

		upgrader: &websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			Subprotocols:    webtty.Protocols,
			CheckOrigin:     originChekcer,
		},
		indexTemplate:    indexTemplate,
		titleTemplate:    titleTemplate,
		manifestTemplate: manifestTemplate,
	}, nil
}

// Run starts the main process of the Server.
// The cancelation of ctx will shutdown the server immediately with aborting
// existing connections. Use WithGracefullContext() to support gracefull shutdown.
func (server *Server) Run(ctx context.Context, options ...RunOption) error {
	cctx, cancel := context.WithCancel(ctx)
	opts := &RunOptions{gracefullCtx: context.Background()}
	for _, opt := range options {
		opt(opts)
	}

	// Initialize shared session
	slave, err := server.factory.New(nil, nil)
	if err != nil {
		return errors.Wrapf(err, "failed to create shared terminal")
	}
	defer slave.Close()

	server.sessionManager = NewSessionManager(cctx, slave, server.options)
	if err := server.sessionManager.InitializeTerminal(); err != nil {
		return errors.Wrapf(err, "failed to initialize terminal size")
	}

	// Initialize API components BEFORE starting goroutines to avoid data race
	if server.options.EnableAPI {
		server.terminalStatus = NewTerminalStatus(time.Duration(server.options.UserIdleMs) * time.Millisecond)
		defer server.terminalStatus.Stop()

		server.broadcastCtrl = NewBroadcastController()

		probeTimeout := time.Duration(server.options.ProbeTimeoutMs) * time.Millisecond
		probeManager := NewProbeManager(slave, server.broadcastCtrl, probeTimeout)

		notifyFn := func(execID, status string) {
			payload, _ := json.Marshal(map[string]string{
				"type":    status,
				"exec_id": execID,
			})
			server.sessionManager.NotifyClients('9', payload)
		}

		replayFn := func(raw []byte) {
			encoded := make([]byte, base64.StdEncoding.EncodedLen(len(raw))+1)
			encoded[0] = '1'
			base64.StdEncoding.Encode(encoded[1:], raw)
			server.sessionManager.broadcast <- encoded
		}

		server.execManager = NewExecManager(slave, server.terminalStatus, probeManager, server.broadcastCtrl, notifyFn, replayFn)
		log.Printf("API enabled")
	}

	go server.sessionManager.Run()
	go server.readSlaveOutput(cctx)

	counter := newCounter(time.Duration(server.options.Timeout) * time.Second)

	path := server.options.Path
	if server.options.EnableRandomUrl {
		path = "/" + randomstring.Generate(server.options.RandomUrlLength) + "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}
	handlers := server.setupHandlers(cctx, cancel, path, counter)
	srv, err := server.setupHTTPServer(handlers)
	if err != nil {
		return errors.Wrapf(err, "failed to setup an HTTP server")
	}

	if server.options.PermitWrite {
		log.Printf("Permitting clients to write input to the PTY.")
	}
	if server.options.Once {
		log.Printf("Once option is provided, accepting only one client")
	}

	if server.options.Port == "0" {
		log.Printf("Port number configured to `0`, choosing a random port")
	}
	hostPort := net.JoinHostPort(server.options.Address, server.options.Port)
	listener, err := net.Listen("tcp", hostPort)
	if err != nil {
		return errors.Wrapf(err, "failed to listen at `%s`", hostPort)
	}

	scheme := "http"
	if server.options.EnableTLS {
		scheme = "https"
	}
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	log.Printf("HTTP server is listening at: %s", scheme+"://"+net.JoinHostPort(host, port)+path)
	if server.options.Address == "0.0.0.0" {
		for _, address := range listAddresses() {
			log.Printf("Alternative URL: %s", scheme+"://"+net.JoinHostPort(address, port)+path)
		}
	}

	srvErr := make(chan error, 1)
	go func() {
		var sErr error
		if server.options.EnableTLS {
			crtFile := homedir.Expand(server.options.TLSCrtFile)
			keyFile := homedir.Expand(server.options.TLSKeyFile)
			log.Printf("TLS crt file: " + crtFile)
			log.Printf("TLS key file: " + keyFile)

			sErr = srv.ServeTLS(listener, crtFile, keyFile)
		} else {
			sErr = srv.Serve(listener)
		}
		if sErr != nil {
			srvErr <- sErr
		}
	}()

	go func() {
		select {
		case <-opts.gracefullCtx.Done():
			srv.Shutdown(context.Background())
		case <-cctx.Done():
		}
	}()

	select {
	case err = <-srvErr:
		if err == http.ErrServerClosed { // by gracefull ctx
			err = nil
		} else {
			cancel()
		}
	case <-cctx.Done():
		srv.Close()
		err = cctx.Err()
	}

	conn := counter.count()
	if conn > 0 {
		log.Printf("Waiting for %d connections to be closed", conn)
	}
	counter.wait()

	return err
}

func (server *Server) setupHandlers(ctx context.Context, cancel context.CancelFunc, pathPrefix string, counter *counter) http.Handler {
	fs, err := fs.Sub(bindata.Fs, "static")
	if err != nil {
		log.Fatalf("failed to open static/ subdirectory of embedded filesystem: %v", err)
	}
	staticFileHandler := http.FileServer(http.FS(fs))

	var siteMux = http.NewServeMux()
	siteMux.HandleFunc(pathPrefix, server.handleIndex)
	siteMux.Handle(pathPrefix+"js/", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"favicon.ico", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"icon.svg", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"css/", http.StripPrefix(pathPrefix, staticFileHandler))
	siteMux.Handle(pathPrefix+"icon_192.png", http.StripPrefix(pathPrefix, staticFileHandler))

	siteMux.HandleFunc(pathPrefix+"manifest.json", server.handleManifest)
	siteMux.HandleFunc(pathPrefix+"auth_token.js", server.handleAuthToken)
	siteMux.HandleFunc(pathPrefix+"config.js", server.handleConfig)

	siteHandler := http.Handler(siteMux)

	if server.options.EnableBasicAuth {
		log.Printf("Using Basic Authentication")
		siteHandler = server.wrapBasicAuth(siteHandler, server.options.Credential)
	}

	withGz := gziphandler.GzipHandler(server.wrapHeaders(siteHandler))
	siteHandler = server.wrapLogger(withGz)

	wsMux := http.NewServeMux()
	wsMux.Handle("/", siteHandler)
	wsMux.HandleFunc(pathPrefix+"ws", server.generateHandleWS(ctx, cancel, counter))
	wsMux.HandleFunc(pathPrefix+"asr/ws", server.generateHandleASRWS(ctx))

	// Register API routes
	if server.options.EnableAPI {
		apiPrefix := pathPrefix + "api/v1/"
		wsMux.Handle(apiPrefix+"input", server.wrapAPIAuth(http.HandlerFunc(server.handleAPIInput)))
		wsMux.Handle(apiPrefix+"exec", server.wrapAPIAuth(http.HandlerFunc(server.handleAPIExec)))
		wsMux.Handle(apiPrefix+"exec/stream", server.wrapAPIAuth(http.HandlerFunc(server.handleAPIExecStream)))
		wsMux.Handle(apiPrefix+"output/lines", server.wrapAPIAuth(http.HandlerFunc(server.handleAPIOutputLines)))
		wsMux.Handle(apiPrefix+"status", server.wrapAPIAuth(http.HandlerFunc(server.handleAPIStatus)))
	}

	siteHandler = http.Handler(wsMux)

	return siteHandler
}

func (server *Server) setupHTTPServer(handler http.Handler) (*http.Server, error) {
	srv := &http.Server{
		Handler: handler,
	}

	if server.options.EnableTLSClientAuth {
		tlsConfig, err := server.tlsConfig()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to setup TLS configuration")
		}
		srv.TLSConfig = tlsConfig
	}

	return srv, nil
}

func (server *Server) tlsConfig() (*tls.Config, error) {
	caFile := homedir.Expand(server.options.TLSCACrtFile)
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, errors.New("could not open CA crt file " + caFile)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, errors.New("could not parse CA crt file data in " + caFile)
	}
	tlsConfig := &tls.Config{
		ClientCAs:  caCertPool,
		ClientAuth: tls.RequireAndVerifyClientCert,
	}
	return tlsConfig, nil
}
