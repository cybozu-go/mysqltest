MYSQL_PORT ?= 33060

.PHONY: start-mysql
start-mysql:
	docker run \
		--name todo-db \
		-d \
		--rm \
		-p $(MYSQL_PORT):3306 \
		--tmpfs /var/lib/mysql \
		-e MYSQL_ROOT_PASSWORD=root \
		-e MYSQL_ROOT_HOST=% \
		mysql/mysql-server:8.0.28 \
		--skip-innodb-doublewrite \
		--innodb-flush-log-at-trx-commit=0 \
		--max-connections=1000

.PHONY: stop-mysql
stop-mysql:
	docker rm -fv todo-db
