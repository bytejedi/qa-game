package config

import (
	"database/sql"
	"log"

	_ "github.com/go-sql-driver/mysql"
)

// DB function
func DB() *sql.DB {

	user := "game"
	password := "secret"
	host := "1.2.3.4"
	port := "3306"
	dbName := "dbname"

	db, _ := sql.Open("mysql", user+":"+password+"@tcp("+host+":"+port+")/"+dbName+"?charset=utf8")
	err := db.Ping()
	if err != nil {
		log.Panicln(err)
	}
	return db
}

// 事务回滚
func TxRollback(tx *sql.Tx) {
	err := tx.Rollback()
	if err != sql.ErrTxDone && err != nil {
		log.Fatalln(err)
	}
}
