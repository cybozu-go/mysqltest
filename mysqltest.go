package mysqltest

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	maxPingRetries = 20
	pingInterval   = 500 * time.Millisecond
)

type config struct {
	rootUser       string
	rootPassword   string
	preserveTestDB bool
	verbose        bool
	mysqlConfig    *mysql.Config
	queries        []string
}

func newConfig(options []Option) *config {
	config := &config{
		rootUser:     "root",
		rootPassword: "root",
		mysqlConfig:  mysql.NewConfig(),
	}
	for _, option := range options {
		option(config)
	}
	return config
}

// Option configures the MySQL test setup.
type Option func(*config)

// RootUserCredentials sets the root user credentials for MySQL connection.
// If not specified, the default credentials are "root"/"root".
func RootUserCredentials(user, password string) Option {
	return func(c *config) {
		c.rootUser = user
		c.rootPassword = password
	}
}

// PreserveTestDB controls whether the test database and user are preserved after test completion.
// By default, the test database and user are automatically cleaned up when the test finishes.
// When this option is specified, the database and user will remain in MySQL for debugging or manual inspection.
func PreserveTestDB() Option {
	return func(c *config) {
		c.preserveTestDB = true
	}
}

// Verbose enables verbose logging of MySQL connection details during setup.
func Verbose() Option {
	return func(c *config) {
		c.verbose = true
	}
}

// ModifyConfig applies a modification function to the underlying MySQL configuration
// created by mysql.NewConfig(). Use this to customize connection settings like timeouts or protocol.
//
// Note: Some configuration fields will be overridden by SetupDatabase:
//   - Addr is overridden with values from HostEnv and PortEnv environment variables
//   - User and Passwd are overridden with RootUserEnv and RootPasswordEnv for root connections,
//     or with randomly generated values for test user connections
//   - DBName is overridden with a randomly generated database name for test connections
func ModifyConfig(f func(*mysql.Config)) Option {
	return func(c *config) {
		f(c.mysqlConfig)
	}
}

// Query sets a single SQL query to be executed after database setup.
//
// Note: If your query contains multiple statements separated by semicolons,
// you must enable MultiStatements in the MySQL configuration:
//
//	db := mysqltest.SetupDatabase(t,
//		mysqltest.ModifyConfig(func(cfg *mysql.Config) {
//			cfg.MultiStatements = true
//		}),
//		mysqltest.Query("CREATE TABLE t1 (id INT); INSERT INTO t1 VALUES (1);"))
func Query(query string) Option {
	return func(c *config) {
		c.queries = append(c.queries, query)
	}
}

// Queries sets multiple SQL queries to be executed after database setup.
//
// Note: If any of your queries contain multiple statements separated by semicolons,
// you must enable MultiStatements in the MySQL configuration:
//
//	db := mysqltest.SetupDatabase(t,
//		mysqltest.ModifyConfig(func(cfg *mysql.Config) {
//			cfg.MultiStatements = true
//		}),
//		mysqltest.Queries([]string{
//			"CREATE TABLE t1 (id INT); INSERT INTO t1 VALUES (1);",
//			"CREATE TABLE t2 (name VARCHAR(50))",
//		}))
func Queries(queries []string) Option {
	return func(c *config) {
		c.queries = append(c.queries, queries...)
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
	rootUserConfig.mysqlConfig.User = rootUserConfig.rootUser
	rootUserConfig.mysqlConfig.Passwd = rootUserConfig.rootPassword

	if rootUserConfig.verbose {
		t.Logf("mysqltest: Connecting to MySQL as root user - Address: %s, User: %s, DSN: %s",
			rootUserConfig.mysqlConfig.Addr,
			rootUserConfig.mysqlConfig.User,
			rootUserConfig.mysqlConfig.FormatDSN())
	}

	db, err := sql.Open("mysql", rootUserConfig.mysqlConfig.FormatDSN())
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
		db, err := sql.Open("mysql", rootUserConfig.mysqlConfig.FormatDSN())
		if err != nil {
			t.Fatalf("mysqltest: %v", err)
		}
		defer db.Close()
		if rootUserConfig.preserveTestDB {
			if rootUserConfig.verbose {
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
	testUserConfig.mysqlConfig.User = testUser
	testUserConfig.mysqlConfig.Passwd = testPasswd
	testUserConfig.mysqlConfig.DBName = testSchema

	if testUserConfig.verbose {
		t.Logf("mysqltest: Connecting to MySQL as test user - Address: %s, User: %s, Schema: %s, DSN: %s",
			testUserConfig.mysqlConfig.Addr,
			testUserConfig.mysqlConfig.User,
			testUserConfig.mysqlConfig.DBName,
			testUserConfig.mysqlConfig.FormatDSN())
	}

	testDB, err := sql.Open("mysql", testUserConfig.mysqlConfig.FormatDSN())
	if err != nil {
		t.Fatalf("mysqltest: %v", err)
	}

	for _, query := range testUserConfig.queries {
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
