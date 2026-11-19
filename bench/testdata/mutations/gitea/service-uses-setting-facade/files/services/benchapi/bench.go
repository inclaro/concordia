package benchapi

import "gitea.dev/modules/setting"

func benchFacade() {
	_, _ = setting.IsRunUserMatchCurrentUser("git")
}
