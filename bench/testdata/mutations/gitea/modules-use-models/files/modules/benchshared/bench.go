package benchshared

import "gitea.dev/models/user"

func benchShared() {
	_ = user.NewGhostUser()
}
