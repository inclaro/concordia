package mailer

import "gitea.dev/models/user"

func benchAllowed() {
	_ = user.NewGhostUser()
}
