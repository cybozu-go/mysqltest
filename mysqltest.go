package mysqltest

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	maxPingRetries = 20
	pingInterval   = 500 * time.Millisecond
)

var (
	defaultMySQLHost         = "127.0.0.1"
	defaultMySQLPort         = "3306"
	defaultMySQLRootUser     = "root"
	defaultMySQLRootPassword = "root"
	preserveTestDBEnv        = "PRESERVE_TEST_DB"
)

type Config struct {
	HostEnv         string
	PortEnv         string
	RootUserEnv     string
	RootPasswordEnv string
	MySQLConfig     *mysql.Config
	InitialQueries  []string
}

func newConfig(options []Option) *Config {
	config := &Config{
		HostEnv:         "MYSQL_HOST",
		PortEnv:         "MYSQL_PORT",
		RootUserEnv:     "MYSQL_ROOT_USER",
		RootPasswordEnv: "MYSQL_ROOT_PASSWORD",
		MySQLConfig:     mysql.NewConfig(),
	}
	for _, option := range options {
		option(config)
	}
	return config
}

// Option configures the MySQL test setup.
type Option func(*Config)

// SetHostEnv sets the environment variable name for MySQL host.
func SetHostEnv(env string) Option {
	return func(c *Config) {
		c.HostEnv = env
	}
}

// SetPortEnv sets the environment variable name for MySQL port.
func SetPortEnv(env string) Option {
	return func(c *Config) {
		c.PortEnv = env
	}
}

// SetRootUserEnv sets the environment variable name for MySQL root user.
func SetRootUserEnv(env string) Option {
	return func(c *Config) {
		c.RootUserEnv = env
	}
}

// SetRootPasswordEnv sets the environment variable name for MySQL root password.
func SetRootPasswordEnv(env string) Option {
	return func(c *Config) {
		c.RootPasswordEnv = env
	}
}

// ModifyMySQLConfig applies a modification function to the underlying MySQL configuration
// created by mysql.NewConfig(). Use this to customize connection settings like timeouts or protocol.
//
// Note: Some configuration fields will be overridden by SetupDatabase:
//   - Addr is overridden with values from HostEnv and PortEnv environment variables
//   - User and Passwd are overridden with RootUserEnv and RootPasswordEnv for root connections,
//     or with randomly generated values for test user connections
//   - DBName is overridden with a randomly generated database name for test connections
func ModifyMySQLConfig(f func(*mysql.Config)) Option {
	return func(c *Config) {
		f(c.MySQLConfig)
	}
}

// SetInitialQueries sets SQL queries to be executed after database setup.
func SetInitialQueries(queries []string) Option {
	return func(c *Config) {
		c.InitialQueries = queries
	}
}

// Conn represents a test database connection with credentials and schema information.
type Conn struct {
	DB       *sql.DB
	Schema   string
	User     string
	Password string
}

// SetupDatabase creates a test database with random credentials and returns a connection.
// It automatically handles cleanup and applies the provided configuration options.
func SetupDatabase(t *testing.T, options ...Option) *Conn {
	t.Helper()

	// Setup user, schema, and privileges using root user.
	rootUserConfig := newConfig(options)
	overrideConfig(
		rootUserConfig,
		getEnv(rootUserConfig.RootUserEnv, defaultMySQLRootUser),
		getEnv(rootUserConfig.RootPasswordEnv, defaultMySQLRootPassword),
		"",
	)

	// Debug: MySQL connection details
	t.Logf("mysqltest: Connecting to MySQL as root user - Address: %s, User: %s, DSN: %s",
		rootUserConfig.MySQLConfig.Addr,
		rootUserConfig.MySQLConfig.User,
		rootUserConfig.MySQLConfig.FormatDSN())

	db, err := sql.Open("mysql", rootUserConfig.MySQLConfig.FormatDSN())
	if err != nil {
		t.Fatalf("mysqltest: %v", err)
	}
	defer db.Close()

	if err := waitUntilDatabaseAvailable(db); err != nil {
		t.Fatalf("mysqltest: %v", err)
	}

	testUser, testPasswd, err := createRandomUser(db)
	if err != nil {
		t.Fatalf("mysqltest: %v", err)
	}
	testSchema, err := createRandomSchema(db)
	if err != nil {
		t.Fatalf("mysqltest: %v", err)
	}
	if err := grantAllPrivileges(db, testUser, testSchema); err != nil {
		t.Fatalf("mysqltest: %v", err)
	}
	t.Cleanup(func() {
		// Since the DB has already been closed, reopen it.
		db, err := sql.Open("mysql", rootUserConfig.MySQLConfig.FormatDSN())
		if err != nil {
			t.Fatalf("mysqltest: %v", err)
		}
		defer db.Close()
		if os.Getenv(preserveTestDBEnv) != "" {
			t.Logf("mysqltest: database '%v' and user '%v' are preserved", testSchema, testUser)
			return
		}
		if err := teardown(db, testUser, testSchema); err != nil {
			t.Fatalf("mysqltest: failed to teardown: %s", err)
		}
	})

	// Execute initial queries using the test user.
	testUserConfig := newConfig(options)
	overrideConfig(testUserConfig, testUser, testPasswd, testSchema)

	// Debug: MySQL connection details for test user
	t.Logf("mysqltest: Connecting to MySQL as test user - Address: %s, User: %s, Schema: %s, DSN: %s",
		testUserConfig.MySQLConfig.Addr,
		testUserConfig.MySQLConfig.User,
		testUserConfig.MySQLConfig.DBName,
		testUserConfig.MySQLConfig.FormatDSN())

	testDB, err := sql.Open("mysql", testUserConfig.MySQLConfig.FormatDSN())
	if err != nil {
		t.Fatalf("mysqltest: %v", err)
	}

	for _, query := range testUserConfig.InitialQueries {
		if _, err := testDB.Exec(query); err != nil {
			t.Fatalf("mysqltest: %v", err)
		}
	}
	t.Cleanup(func() {
		if err := testDB.Close(); err != nil {
			t.Logf("mysqltest: failed to close database: %s", err)
		}
	})
	return &Conn{
		DB:       testDB,
		Schema:   testSchema,
		User:     testUser,
		Password: testPasswd,
	}
}

func getEnv(key string, defaultValue string) string {
	val := os.Getenv(key)
	if val == "" {
		val = defaultValue
	}
	return val
}

func overrideConfig(config *Config, user, password, schema string) {
	mysqlHost := getEnv(config.HostEnv, defaultMySQLHost)
	mysqlPort := getEnv(config.PortEnv, defaultMySQLPort)

	config.MySQLConfig.Addr = net.JoinHostPort(mysqlHost, mysqlPort)
	config.MySQLConfig.User = user
	config.MySQLConfig.Passwd = password
	config.MySQLConfig.DBName = schema
}

func randomSuffix() string {
	b := make([]byte, 7)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return strings.ToLower(enc.EncodeToString(b))
}

func waitUntilDatabaseAvailable(db *sql.DB) error {
	for range maxPingRetries {
		if err := db.Ping(); err != nil {
			time.Sleep(pingInterval)
			continue
		}
		return nil
	}
	return fmt.Errorf("failed to connect to the database")
}

func createRandomUser(db *sql.DB) (string, string, error) {
	dbUser := randomSuffix()
	dbPassword := randomSuffix()
	query := fmt.Sprintf("CREATE USER '%s'@'%%' IDENTIFIED BY '%s'", dbUser, dbPassword)
	if _, err := db.Exec(query); err != nil {
		return "", "", err
	}
	return dbUser, dbPassword, nil
}

func createRandomSchema(db *sql.DB) (string, error) {
	dbName := "mysqltest_" + randomSuffix()
	if _, err := db.Exec(fmt.Sprintf("CREATE DATABASE `%s`", dbName)); err != nil {
		return "", err
	}
	return dbName, nil
}

func grantAllPrivileges(db *sql.DB, user, dbName string) error {
	query := fmt.Sprintf("GRANT ALL ON `%s`.* TO '%s'@'%%'", dbName, user)
	if _, err := db.Exec(query); err != nil {
		return err
	}
	if _, err := db.Exec("FLUSH PRIVILEGES"); err != nil {
		return err
	}
	return nil
}

func teardown(db *sql.DB, user, dbName string) error {
	if _, err := db.Exec(fmt.Sprintf("DROP USER '%s'@'%%'", user)); err != nil {
		return err
	}
	if _, err := db.Exec(fmt.Sprintf("DROP DATABASE `%s`", dbName)); err != nil {
		return err
	}
	return nil
}
