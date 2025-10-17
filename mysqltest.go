package mysqltest

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
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

type Config struct {
	RootUser       string
	RootPassword   string
	PreserveTestDB bool
	Verbose        bool
	MySQLConfig    *mysql.Config
	Queries        []string
}

func newConfig(options []Option) *Config {
	config := &Config{
		RootUser:     "root",
		RootPassword: "root",
		MySQLConfig:  mysql.NewConfig(),
	}
	for _, option := range options {
		option(config)
	}
	return config
}

// Option configures the MySQL test setup.
type Option func(*Config)

// RootUserCredentials sets the root user credentials for MySQL connection.
// If not specified, the default credentials are "root"/"root".
func RootUserCredentials(user, password string) Option {
	return func(c *Config) {
		c.RootUser = user
		c.RootPassword = password
	}
}

// PreserveTestDB controls whether the test database and user are preserved after test completion.
// By default (false), the test database and user are automatically cleaned up when the test finishes.
// When set to true, the database and user will remain in MySQL for debugging or manual inspection.
func PreserveTestDB(preserve bool) Option {
	return func(c *Config) {
		c.PreserveTestDB = preserve
	}
}

// Verbose enables verbose logging of MySQL connection details during setup.
func Verbose() Option {
	return func(c *Config) {
		c.Verbose = true
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

// Query sets a single SQL query to be executed after database setup.
//
// Note: If your query contains multiple statements separated by semicolons,
// you must enable MultiStatements in the MySQL configuration:
//
//	db := mysqltest.SetupDatabase(t,
//		mysqltest.ModifyMySQLConfig(func(cfg *mysql.Config) {
//			cfg.MultiStatements = true
//		}),
//		mysqltest.Query("CREATE TABLE t1 (id INT); INSERT INTO t1 VALUES (1);"))
func Query(query string) Option {
	return func(c *Config) {
		c.Queries = append(c.Queries, query)
	}
}

// Queries sets multiple SQL queries to be executed after database setup.
//
// Note: If any of your queries contain multiple statements separated by semicolons,
// you must enable MultiStatements in the MySQL configuration:
//
//	db := mysqltest.SetupDatabase(t,
//		mysqltest.ModifyMySQLConfig(func(cfg *mysql.Config) {
//			cfg.MultiStatements = true
//		}),
//		mysqltest.Queries([]string{
//			"CREATE TABLE t1 (id INT); INSERT INTO t1 VALUES (1);",
//			"CREATE TABLE t2 (name VARCHAR(50))",
//		}))
func Queries(queries []string) Option {
	return func(c *Config) {
		c.Queries = append(c.Queries, queries...)
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
	rootUserConfig.MySQLConfig.User = rootUserConfig.RootUser
	rootUserConfig.MySQLConfig.Passwd = rootUserConfig.RootPassword

	if rootUserConfig.Verbose {
		t.Logf("mysqltest: Connecting to MySQL as root user - Address: %s, User: %s, DSN: %s",
			rootUserConfig.MySQLConfig.Addr,
			rootUserConfig.MySQLConfig.User,
			rootUserConfig.MySQLConfig.FormatDSN())
	}

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
		if rootUserConfig.PreserveTestDB {
			if rootUserConfig.Verbose {
				t.Logf("mysqltest: database '%v' and user '%v' are preserved",
					testSchema, testUser)
			}
			return
		}
		if err := teardown(db, testUser, testSchema); err != nil {
			t.Fatalf("mysqltest: failed to teardown: %s", err)
		}
	})

	// Execute initial queries using the test user.
	testUserConfig := newConfig(options)
	testUserConfig.MySQLConfig.User = testUser
	testUserConfig.MySQLConfig.Passwd = testPasswd
	testUserConfig.MySQLConfig.DBName = testSchema

	if testUserConfig.Verbose {
		t.Logf("mysqltest: Connecting to MySQL as test user - Address: %s, User: %s, Schema: %s, DSN: %s",
			testUserConfig.MySQLConfig.Addr,
			testUserConfig.MySQLConfig.User,
			testUserConfig.MySQLConfig.DBName,
			testUserConfig.MySQLConfig.FormatDSN())
	}

	testDB, err := sql.Open("mysql", testUserConfig.MySQLConfig.FormatDSN())
	if err != nil {
		t.Fatalf("mysqltest: %v", err)
	}

	for _, query := range testUserConfig.Queries {
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
	dbUser := "mysqltest_" + randomSuffix()
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

// GetEnvOr returns the value of the environment variable named by the key.
// If the variable is not present or is an empty string, it returns defaultValue.
func GetEnvOr(key string, defaultValue string) string {
	val := os.Getenv(key)
	if val == "" {
		val = defaultValue
	}
	return val
}
