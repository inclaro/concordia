package base

import "gitea.dev/models/user"

func benchUser() {
	_ = user.NewGhostUser()
}
