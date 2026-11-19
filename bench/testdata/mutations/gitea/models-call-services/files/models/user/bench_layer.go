package user

import "gitea.dev/services/mailer"

func benchLayer() {
	_ = mailer.NewNotifier()
}
