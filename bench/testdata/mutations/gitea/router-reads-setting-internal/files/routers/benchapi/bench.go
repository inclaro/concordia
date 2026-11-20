package benchapi

import "gitea.dev/modules/setting"

func benchSetting() {
	_ = setting.AuditRecordEnabled()
}
