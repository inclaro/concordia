package benchpath

import "gitea.dev/services/mailer"

func Hop() {
	_ = mailer.NewNotifier()
}
