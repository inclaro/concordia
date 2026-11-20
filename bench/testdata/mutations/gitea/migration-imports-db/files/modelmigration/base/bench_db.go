package base

import "gitea.dev/models/db"

func benchDB() {
	_ = db.BuilderDialect()
}
