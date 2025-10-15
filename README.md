# mysqltest

[![Go Reference](https://pkg.go.dev/badge/github.com/cybozu-go/mysqltest.svg)](https://pkg.go.dev/github.com/cybozu-go/mysqltest)

A Go library for creating isolated MySQL test databases with automatic cleanup.

## Features

- **Isolated Test Databases**: Each test gets its own randomly named database and user
- **Automatic Cleanup**: Database and user are automatically removed after tests
- **Flexible Configuration**: Customize MySQL connection settings via environment variables or options
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

func TestTodoList(t *testing.T) {
	mysqlConfig := func(c *mysql.Config) {
		c.Net = "tcp"
	}
	initialQueries := []string{
		"CREATE TABLE todos (" +
			"id INT AUTO_INCREMENT PRIMARY KEY, " +
			"item VARCHAR(255) NOT NULL)",
	}
	conn := mysqltest.SetupDatabase(t,
		mysqltest.ModifyMySQLConfig(mysqlConfig),
		mysqltest.SetInitialQueries(initialQueries),
	)

	sut := &TodoList{db: conn.DB}

	err := sut.Add("Buy milk")
	if err != nil {
		t.Fatal(err)
	}

	items, err := sut.List()
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 1 || items[0] != "Buy milk" {
		t.Fatalf("unexpected items: %#v", items)
	}
}
```

## Configuration

### Environment Variables

The library uses the following environment variables with their default values:

| Variable | Default | Description |
|----------|---------|-------------|
| `MYSQL_HOST` | `127.0.0.1` | MySQL server host |
| `MYSQL_PORT` | `3306` | MySQL server port |
| `MYSQL_ROOT_USER` | `root` | MySQL root username |
| `MYSQL_ROOT_PASSWORD` | `root` | MySQL root password |
| `PRESERVE_TEST_DB` | (empty) | If set, test databases won't be cleaned up |

### Configuration Options

You can customize the MySQL connection and test setup using the following options:

#### SetHostEnv / SetPortEnv / SetRootUserEnv / SetRootPasswordEnv

Override the environment variable names used for MySQL connection:

```go
conn := mysqltest.SetupDatabase(t,
    mysqltest.SetHostEnv("MY_MYSQL_HOST"),
    mysqltest.SetPortEnv("MY_MYSQL_PORT"),
    mysqltest.SetRootUserEnv("MY_MYSQL_USER"),
    mysqltest.SetRootPasswordEnv("MY_MYSQL_PASS"),
)
```

#### ModifyMySQLConfig

Customize the underlying MySQL configuration:

```go
conn := mysqltest.SetupDatabase(t,
    mysqltest.ModifyMySQLConfig(func(c *mysql.Config) {
        c.Net = "tcp"
        c.Timeout = 30 * time.Second
        c.ReadTimeout = 10 * time.Second
        c.WriteTimeout = 10 * time.Second
        c.Params = map[string]string{
            "charset": "utf8mb4",
            "parseTime": "true",
        }
    }),
)
```

#### SetInitialQueries

Execute SQL statements after database setup:

```go
initialQueries := []string{
    "CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(255))",
    "INSERT INTO products VALUES (1, 'Widget')",
}

conn := mysqltest.SetupDatabase(t,
    mysqltest.SetInitialQueries(initialQueries),
)
```

