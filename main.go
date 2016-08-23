package main

import (
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/antonlindstrom/pgstore"
	"github.com/hpcloud/portal-proxy/datastore"
	"github.com/hpcloud/portal-proxy/repository/tokens"
	"github.com/hpcloud/ucpconfig"
	"github.com/labstack/echo"
	"github.com/labstack/echo/engine/standard"
	"github.com/labstack/echo/middleware"
)

// TimeoutBoundary represents the amount of time we'll wait for the database
// server to come online before we bail out.
const TimeoutBoundary = 10

var (
	httpClient = http.Client{}
)

func cleanup(dbc *sql.DB, ss *pgstore.PGStore) {
	log.Println("Attempting to shut down gracefully...")
	log.Println(`--- Closing databaseConnectionPool`)
	dbc.Close()
	log.Println(`--- Closing sessionStore`)
	ss.Close()
	log.Println(`--- Stopping sessionStore cleanup`)
	ss.StopCleanup(ss.Cleanup(time.Minute * 5))
	log.Println("Graceful shut down complete")
}

func main() {
	log.SetOutput(os.Stdout)
	log.Println("Proxy initialization started.")

	// Load the portal configuration from env vars via ucpconfig
	var portalConfig portalConfig
	portalConfig, err := loadPortalConfig(portalConfig)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	log.Println("Proxy configuration loaded.")

	// Initialize the HTTP client
	initializeHTTPClient(portalConfig.SkipTLSVerification,
		time.Duration(portalConfig.HTTPClientTimeoutInSecs)*time.Second)
	log.Println("HTTP client initialized.")

	// Get the encryption key we need for tokens in the database
	portalConfig.EncryptionKeyInBytes, err = getEncryptionKey(portalConfig)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	log.Println("Encryption key set.")

	portalConfig.VCSClientMap, err = getVCSClients(portalConfig)
	if err != nil {
		log.Println("Error parsing VCS clients")
	}

	// Establish a Postgresql connection pool
	var databaseConnectionPool *sql.DB
	databaseConnectionPool, err = initConnPool()
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	defer func() {
		log.Println(`--- Closing databaseConnectionPool`)
		databaseConnectionPool.Close()
	}()
	log.Println("Proxy database connection pool created.")

	// Initialize the Postgres backed session store for Gorilla sessions
	sessionStore, err := initSessionStore(databaseConnectionPool, portalConfig)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	defer func() {
		log.Println(`--- Closing sessionStore`)
		sessionStore.Close()
	}()
	defer func() {
		log.Println(`--- Setting up sessionStore cleanup`)
		sessionStore.StopCleanup(sessionStore.Cleanup(time.Minute * 5))
	}()
	log.Println("Proxy session store initialized.")

	// Setup the global interface for the proxy
	portalProxy := newPortalProxy(portalConfig, databaseConnectionPool, sessionStore)
	log.Println("Proxy initialization complete.")

	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cleanup(databaseConnectionPool, sessionStore)
		os.Exit(1)
	}()

	// Start the proxy
	log.Println("Proxy config at startup")
	log.Printf("%+v\n", portalConfig)
	if err := start(portalProxy); err != nil {
		log.Printf("Unable to start the proxy: %v", err)
		os.Exit(1)
	}
}

func getEncryptionKey(pc portalConfig) ([]byte, error) {
	log.Println("getEncryptionKey")

	// If it exists in "EncryptionKey" we must be in compose; use it.
	if len(pc.EncryptionKey) > 0 {
		key32bytes, err := hex.DecodeString(string(pc.EncryptionKey))
		if err != nil {
			fmt.Println(err)
		}

		return key32bytes, nil
	}

	// Read the key from the shared volume
	key, err := tokens.ReadKey(pc.EncryptionKeyVolume, pc.EncryptionKeyFilename)
	if err != nil {
		log.Printf("Unable to read the encryption key from the shared volume: %v", err)
		return nil, err
	}

	return key, nil
}

func initConnPool() (*sql.DB, error) {
	log.Println("initConnPool")

	// load up postgresql database configuration
	var dc datastore.DatabaseConfig
	dc, err := loadDatabaseConfig(dc)
	if err != nil {
		return nil, err
	}

	// initialize the database connection pool
	var pool *sql.DB
	pool, err = datastore.GetConnection(dc)
	if err != nil {
		return nil, err
	}

	// Ensure Postgres is responsive
	for {

		// establish an outer timeout boundary
		timeout := time.Now().Add(time.Minute * TimeoutBoundary)

		// Ping Postgres
		err = datastore.Ping(pool)
		if err == nil {
			log.Println("Database appears to now be available.")
			break
		}

		// If our timeout boundary has been exceeded, bail out
		if timeout.Sub(time.Now()) < 0 {
			return nil, fmt.Errorf("Timeout boundary of %d minutes has been exceeded. Exiting.", TimeoutBoundary)
		}

		// Circle back and try again
		log.Printf("Waiting for Postgres to be responsive: %+v\n", err)
		time.Sleep(time.Second)
	}

	return pool, nil
}

func initSessionStore(db *sql.DB, pc portalConfig) (*pgstore.PGStore, error) {
	log.Println("initSessionStore")
	store, err := pgstore.NewPGStoreFromPool(db, []byte(pc.SessionStoreSecret))
	if err != nil {
		return nil, err
	}

	return store, nil
}

func loadPortalConfig(pc portalConfig) (portalConfig, error) {
	log.Println("loadPortalConfig")
	if err := ucpconfig.Load(&pc); err != nil {
		return pc, fmt.Errorf("Unable to load portal configuration. %v", err)
	}
	return pc, nil
}

func loadDatabaseConfig(dc datastore.DatabaseConfig) (datastore.DatabaseConfig, error) {
	log.Println("loadDatabaseConfig")
	if err := ucpconfig.Load(&dc); err != nil {
		return dc, fmt.Errorf("Unable to load database configuration. %v", err)
	}

	log.Printf("Database Config: %+v\n", dc)

	dc, err := datastore.NewDatabaseConnectionParametersFromConfig(dc)
	if err != nil {
		return dc, fmt.Errorf("Unable to load database configuration. %v", err)
	}

	log.Printf("Database Config: %+v\n", dc)

	return dc, nil
}

func createTempCertFiles(pc portalConfig) (string, string, error) {
	log.Println("createTempCertFiles")
	certFilename := "pproxy.crt"
	certKeyFilename := "pproxy.key"

	// If there's a developer cert/key, use that instead of using what's in the
	// config. This is to bypass an issue with docker-compose not being able to
	// handle multi-line variables in an env_file
	devCertsDir := "dev-certs/"
	_, errDevcert := os.Stat(devCertsDir + certFilename)
	_, errDevkey := os.Stat(devCertsDir + certKeyFilename)
	if errDevcert == nil && errDevkey == nil {
		return devCertsDir + certFilename, devCertsDir + certKeyFilename, nil
	}

	err := ioutil.WriteFile(certFilename, []byte(pc.TLSCert), 0600)
	if err != nil {
		return "", "", err
	}

	err = ioutil.WriteFile(certKeyFilename, []byte(pc.TLSCertKey), 0600)
	if err != nil {
		return "", "", err
	}
	return certFilename, certKeyFilename, nil
}

func newPortalProxy(pc portalConfig, dcp *sql.DB, ss *pgstore.PGStore) *portalProxy {
	log.Println("newPortalProxy")
	pp := &portalProxy{
		Config:                 pc,
		DatabaseConnectionPool: dcp,
		SessionStore:           ss,
	}

	return pp
}

func initializeHTTPClient(skipCertVerification bool, timeoutInSeconds time.Duration) {
	log.Println("initializeHTTPClient")
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if skipCertVerification {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	httpClient.Transport = tr
	httpClient.Timeout = time.Second * timeoutInSeconds
}

func start(p *portalProxy) error {
	log.Println("start")
	e := echo.New()

	// Root level middleware
	e.Use(sessionCleanupMiddleware)
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     p.Config.AllowedOrigins,
		AllowMethods:     []string{echo.GET, echo.PUT, echo.POST, echo.DELETE},
		AllowCredentials: true,
	}))
	e.Use(errorLoggingMiddleware)
	e.Use(retryAfterUpgradeMiddleware)

	p.registerRoutes(e)

	certFile, certKeyFile, err := createTempCertFiles(p.Config)
	if err != nil {
		return err
	}

	engine := standard.WithTLS(p.Config.TLSAddress, certFile, certKeyFile)
	e.Run(engine)

	return nil
}

func (p *portalProxy) registerRoutes(e *echo.Echo) {
	log.Println("registerRoutes")

	e.POST("/v1/auth/login/uaa", p.loginToUAA)
	e.POST("/v1/auth/logout", p.logout)

	sessionGroup := e.Group("/v1")
	sessionGroup.Use(p.sessionMiddleware)

	// Connect to HCF cluster
	sessionGroup.POST("/auth/login/cnsi", p.loginToCNSI)

	// Disconnect HCF cluster
	sessionGroup.POST("/auth/logout/cnsi", p.logoutOfCNSI)

	// Verify Session
	sessionGroup.GET("/auth/session/verify", p.verifySession)

	// CNSI operations
	sessionGroup.GET("/cnsis", p.listCNSIs)
	sessionGroup.GET("/cnsis/registered", p.listRegisteredCNSIs)

	// Applications Log Streams
	sessionGroup.GET("/:cnsiGuid/apps/:appGuid/stream", p.appStream)

	// Stackato info
	sessionGroup.GET("/stackato/info", p.stackatoInfo)

	// Version info
	sessionGroup.GET("/version", p.getVersions)

	// VCS Requests
	vcsGroup := sessionGroup.Group("/vcs")

	// Initiate OAuth flow against VCS on behalf of a user
	vcsGroup.GET("/oauth/auth", p.handleVCSAuth)

	// VCS OAuth callback/response
	vcsGroup.GET("/oauth/callback", p.handleVCSAuthCallback)

	// List VCS clients
	vcsGroup.GET("/clients", p.listVCSClients)

	// Verify existence of VCS token in Session
	vcsGroup.GET("/oauth/verify", p.verifyVCSOAuthToken)

	// Proxy the rest to VCS API
	vcsGroup.Any("/*", p.vcsProxy)

	// This is used for passthru of HCF/HCE requests
	group := sessionGroup.Group("/proxy")
	group.Any("/*", p.proxy)

	// The admin-only routes need to be last as the admin middleware will be
	// applied to any routes below it's instantiation
	adminGroup := sessionGroup
	adminGroup.Use(p.stackatoAdminMiddleware)
	// Register clusters
	adminGroup.POST("/register/hcf", p.registerHCFCluster)
	adminGroup.POST("/register/hce", p.registerHCECluster)

	// TODO(wchrisjohnson): revisit the API and fix these wonky calls.  https://jira.hpcloud.net/browse/TEAMFOUR-620
	adminGroup.POST("/unregister", p.unregisterCluster)
	// sessionGroup.DELETE("/cnsis", p.removeCluster)
}
