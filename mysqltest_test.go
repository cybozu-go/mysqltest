package mysqltest_test

import (
	"database/sql"
	"net"
	"os"
	"testing"
	"time"

	"github.com/cybozu-go/mysqltest"
	"github.com/go-sql-driver/mysql"
)

func getEnv(key string, defaultValue string) string {
	val := os.Getenv(key)
	if val == "" {
		val = defaultValue
	}
	return val
}

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
	rootUser := "root"
	rootPassword := getEnv("MYSQL_ROOT_PASSWORD", "root")
	mysqlPort := getEnv("MYSQL_PORT", "3306")
	mysqlConfig := func(c *mysql.Config) {
		c.Net = "tcp"
		c.Addr = net.JoinHostPort("127.0.0.1", mysqlPort)
	}
	initialQueries := []string{
		"CREATE TABLE todos (" +
			"id INT AUTO_INCREMENT PRIMARY KEY, " +
			"item VARCHAR(255) NOT NULL)",
	}
	conn := mysqltest.SetupDatabase(t,
		mysqltest.RootUserCredentials(rootUser, rootPassword),
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

func ExampleModifyMySQLConfig() {
	mysqltest.ModifyMySQLConfig(func(c *mysql.Config) {
		c.Net = "tcp"
		c.Timeout = 30 * time.Second
		c.ReadTimeout = 10 * time.Second
		c.WriteTimeout = 10 * time.Second
	})
}
