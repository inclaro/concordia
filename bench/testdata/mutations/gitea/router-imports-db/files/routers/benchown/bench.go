package benchown

import "gitea.dev/models/db"

func benchOwn() {
	_ = db.BuilderDialect()
}
