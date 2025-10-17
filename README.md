# mysqltest

[![Go Reference](https://pkg.go.dev/badge/github.com/cybozu-go/mysqltest.svg)](https://pkg.go.dev/github.com/cybozu-go/mysqltest)

A Go library for creating isolated MySQL test databases with automatic cleanup.

## Features

- **Isolated Test Databases**: Each test gets its own randomly named database and user
- **Automatic Cleanup**: Database and user are automatically removed after tests complete
- **Flexible Configuration**: Customize MySQL connection settings
- **Easy Setup**: Simple API for setting up test databases with initial schema and data

## Installation

```bash
go get github.com/cybozu-go/mysqltest
```

## Quick Start

```go
package main_test

import (
	"database/sql"
	"net"
	"testing"
	"time"

	"github.com/cybozu-go/mysqltest"
	"github.com/go-sql-driver/mysql"
)

type TodoList struct {
	db *sql.DB
}

func (t *TodoList) Add(item string) error {
	_, err := t.db.Exec("INSERT INTO todos (item) VALUES (?)", item)
	return err
}

func (t *TodoList) List() ([]string, error) {
	rows, err := t.db.Query("SELECT item FROM todos")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []string
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func TestAddTodo(t *testing.T) {
	// Setup
	rootUser := "root"
	rootPassword := mysqltest.GetEnvOr("MYSQL_ROOT_PASSWORD", "root")
	mysqlPort := mysqltest.GetEnvOr("MYSQL_PORT", "3306")
	query1 := "CREATE TABLE todos (" +
		"id INT AUTO_INCREMENT PRIMARY KEY, " +
		"item VARCHAR(255) NOT NULL)"
	query2 := "INSERT INTO todos (item) VALUES ('Buy milk')"

	conn := mysqltest.SetupDatabase(t,
		mysqltest.RootUserCredentials(rootUser, rootPassword),
		mysqltest.Verbose(),
		mysqltest.ModifyConfig(func(c *mysql.Config) {
			c.Net = "tcp"
			c.Addr = net.JoinHostPort("127.0.0.1", mysqlPort)
			c.MultiStatements = true
		}),
		mysqltest.Queries([]string{query1, query2}),
	)

	sut := &TodoList{db: conn.DB}

	// Exercise
	err := sut.Add("Walk the dog")
	if err != nil {
		t.Fatal(err)
	}

	// Verify
	actual, err := sut.List()
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"Buy milk", "Walk the dog"}
	if len(actual) != len(expected) {
		t.Fatalf("expected %d items, got %d", len(expected), len(actual))
	}
	for i := range actual {
		if actual[i] != expected[i] {
			t.Fatalf("unexpected item at index %d: got %q, want %q", i, actual[i], expected[i])
		}
	}
}
```

## Configuration

### Configuration Options

You can customize the MySQL connection and test setup using the following options:

#### RootUserCredentials

Set the MySQL root user credentials for database setup. If not specified, the default credentials are `"root"`/`"root"`.

```go
conn := mysqltest.SetupDatabase(t,
    mysqltest.RootUserCredentials("admin", "secret123"),
)
```

You can also use the `GetEnvOr` helper function to read from environment variables:

```go
rootPassword := mysqltest.GetEnvOr("MYSQL_ROOT_PASSWORD", "root")
conn := mysqltest.SetupDatabase(t,
    mysqltest.RootUserCredentials("root", rootPassword),
)
```

#### PreserveTestDB

Preserve the test database and user after test completion for debugging. By default, test databases and users are automatically cleaned up when tests finish.

```go
conn := mysqltest.SetupDatabase(t,
    mysqltest.PreserveTestDB(), // Database won't be cleaned up
)
```

#### Verbose

Enable verbose logging to see MySQL connection details during setup:

```go
conn := mysqltest.SetupDatabase(t,
    mysqltest.Verbose(), // Log connection details
)
```

#### ModifyConfig

Customize the underlying MySQL configuration:

```go
conn := mysqltest.SetupDatabase(t,
    mysqltest.ModifyConfig(func(c *mysql.Config) {
        c.Net = "tcp"
        c.Addr = "127.0.0.1:3306"
		c.MultiStatements = true
        c.Timeout = 10 * time.Second
        c.Params = map[string]string{
            "charset": "utf8mb4",
        }
		c.ParseTime = true
    }),
)
```

#### Query and Queries

Execute SQL statements after database setup:

```go
// Single query
conn := mysqltest.SetupDatabase(t,
    mysqltest.Query("CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(255))"),
)

// Multiple queries
conn := mysqltest.SetupDatabase(t,
    mysqltest.Queries([]string{
        "CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(255))",
        "INSERT INTO products VALUES (1, 'Widget')",
    }),
)
```

**Note**: If your queries contain multiple statements separated by semicolons, you must enable `MultiStatements`:

```go
conn := mysqltest.SetupDatabase(t,
    mysqltest.ModifyConfig(func(c *mysql.Config) {
        c.MultiStatements = true
    }),
    mysqltest.Query("CREATE TABLE t1 (id INT); INSERT INTO t1 VALUES (1);"),
)
```
